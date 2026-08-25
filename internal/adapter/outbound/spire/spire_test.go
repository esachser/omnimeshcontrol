package spire

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	entryv1 "github.com/spiffe/spire-api-sdk/proto/spire/api/server/entry/v1"
	spiretypes "github.com/spiffe/spire-api-sdk/proto/spire/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// mockSpireServer implements entryv1.EntryServer for testing.
type mockSpireServer struct {
	entryv1.UnimplementedEntryServer
	mu      sync.Mutex
	entries map[string]*spiretypes.Entry
	nextID  int
}

func (s *mockSpireServer) BatchCreateEntry(_ context.Context, req *entryv1.BatchCreateEntryRequest) (*entryv1.BatchCreateEntryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []*entryv1.BatchCreateEntryResponse_Result
	for _, entry := range req.Entries {
		exists := false
		var existingEntry *spiretypes.Entry
		for _, e := range s.entries {
			if e.SpiffeId.TrustDomain == entry.SpiffeId.TrustDomain &&
				e.SpiffeId.Path == entry.SpiffeId.Path &&
				e.ParentId.TrustDomain == entry.ParentId.TrustDomain &&
				e.ParentId.Path == entry.ParentId.Path {
				if selectorsMatch(e.Selectors, entry.Selectors) {
					exists = true
					existingEntry = e
					break
				}
			}
		}

		if exists {
			results = append(results, &entryv1.BatchCreateEntryResponse_Result{
				Status: &spiretypes.Status{
					Code:    int32(codes.AlreadyExists),
					Message: "entry already exists",
				},
				Entry: existingEntry,
			})
			continue
		}

		s.nextID++
		newID := fmt.Sprintf("entry-%d", s.nextID)
		created := &spiretypes.Entry{
			Id:        newID,
			SpiffeId:  entry.SpiffeId,
			ParentId:  entry.ParentId,
			Selectors: entry.Selectors,
		}
		s.entries[newID] = created

		results = append(results, &entryv1.BatchCreateEntryResponse_Result{
			Status: &spiretypes.Status{
				Code: int32(codes.OK),
			},
			Entry: created,
		})
	}

	return &entryv1.BatchCreateEntryResponse{Results: results}, nil
}

func (s *mockSpireServer) ListEntries(_ context.Context, req *entryv1.ListEntriesRequest) (*entryv1.ListEntriesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var matched []*spiretypes.Entry
	for _, entry := range s.entries {
		if req.Filter != nil && req.Filter.BySpiffeId != nil {
			filterID := req.Filter.BySpiffeId
			if entry.SpiffeId.TrustDomain != filterID.TrustDomain || entry.SpiffeId.Path != filterID.Path {
				continue
			}
		}
		matched = append(matched, entry)
	}

	return &entryv1.ListEntriesResponse{Entries: matched}, nil
}

func (s *mockSpireServer) BatchDeleteEntry(_ context.Context, req *entryv1.BatchDeleteEntryRequest) (*entryv1.BatchDeleteEntryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []*entryv1.BatchDeleteEntryResponse_Result
	for _, id := range req.Ids {
		if _, ok := s.entries[id]; ok {
			delete(s.entries, id)
			results = append(results, &entryv1.BatchDeleteEntryResponse_Result{
				Id: id,
				Status: &spiretypes.Status{
					Code: int32(codes.OK),
				},
			})
		} else {
			results = append(results, &entryv1.BatchDeleteEntryResponse_Result{
				Id: id,
				Status: &spiretypes.Status{
					Code:    int32(codes.NotFound),
					Message: "entry not found",
				},
			})
		}
	}

	return &entryv1.BatchDeleteEntryResponse{Results: results}, nil
}

func selectorsMatch(a, b []*spiretypes.Selector) bool {
	if len(a) != len(b) {
		return false
	}
	for _, sa := range a {
		found := false
		for _, sb := range b {
			if sa.Type == sb.Type && sa.Value == sb.Value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestSpireIdentityManager_RegisterAndDeregister(t *testing.T) {
	// Start Mock SPIRE Server on random free port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	server := grpc.NewServer()
	mockServer := &mockSpireServer{entries: make(map[string]*spiretypes.Entry)}
	entryv1.RegisterEntryServer(server, mockServer)

	go func() {
		_ = server.Serve(lis)
	}()
	defer server.Stop()

	// Initialize Manager
	cfg := Config{
		ServerAddr:  "tcp://" + lis.Addr().String(),
		TrustDomain: "omnimesh.local",
		ParentIDs:   []string{"spiffe://omnimesh.local/agents/agent-1"},
		Selectors:   []string{"windows:process-name:{{.Name}}.exe", "unix:user:{{.Name}}"},
	}

	mgr, err := NewIdentityManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewIdentityManager failed: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Test RegisterWorkload (Fresh)
	identity := model.ServiceIdentity{
		SPIFFEID: "spiffe://omnimesh.local/ns/production/sa/payment-service",
		ServiceID: model.ServiceID{
			Namespace: "production",
			Name:      "payment-service",
		},
	}

	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("RegisterWorkload failed: %v", err)
	}

	// Verify entry exists in mock SPIRE
	mockServer.mu.Lock()
	if len(mockServer.entries) != 1 {
		t.Errorf("Expected 1 entry in mock server, got %d", len(mockServer.entries))
	}
	var registeredEntry *spiretypes.Entry
	for _, entry := range mockServer.entries {
		registeredEntry = entry
	}
	mockServer.mu.Unlock()

	if registeredEntry == nil {
		t.Fatal("Registered entry is nil")
	}

	if registeredEntry.SpiffeId.TrustDomain != "omnimesh.local" || registeredEntry.SpiffeId.Path != "/ns/production/sa/payment-service" {
		t.Errorf("Unexpected SPIFFE ID in entry: %v", registeredEntry.SpiffeId)
	}

	// Verify selectors are resolved correctly
	expectedSelectors := []*spiretypes.Selector{
		{Type: "windows", Value: "process-name:payment-service.exe"},
		{Type: "unix", Value: "user:payment-service"},
	}
	if !selectorsMatch(registeredEntry.Selectors, expectedSelectors) {
		t.Errorf("Selectors mismatch. Got %v, want %v", registeredEntry.Selectors, expectedSelectors)
	}

	// 2. Test RegisterWorkload (AlreadyExists)
	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Errorf("RegisterWorkload failed on already-existing entry: %v", err)
	}

	// 3. Test DeregisterWorkload
	if err := mgr.DeregisterWorkload(ctx, identity.ServiceID); err != nil {
		t.Fatalf("DeregisterWorkload failed: %v", err)
	}

	// Verify entry deleted
	mockServer.mu.Lock()
	if len(mockServer.entries) != 0 {
		t.Errorf("Expected 0 entries in mock server after deregistration, got %d", len(mockServer.entries))
	}
	mockServer.mu.Unlock()
}

func TestSpireIdentityManager_RegisterWorkload_CustomSelectors(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	server := grpc.NewServer()
	mockServer := &mockSpireServer{entries: make(map[string]*spiretypes.Entry)}
	entryv1.RegisterEntryServer(server, mockServer)

	go func() {
		_ = server.Serve(lis)
	}()
	defer server.Stop()

	cfg := Config{
		ServerAddr:  "tcp://" + lis.Addr().String(),
		TrustDomain: "omnimesh.local",
		ParentIDs:   []string{"spiffe://omnimesh.local/agents/agent-1"},
		Selectors:   []string{"windows:process-name:default-fallback.exe"},
	}

	mgr, err := NewIdentityManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewIdentityManager failed: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	identity := model.ServiceIdentity{
		SPIFFEID: "spiffe://omnimesh.local/ns/production/sa/custom-service",
		ServiceID: model.ServiceID{
			Namespace: "production",
			Name:      "custom-service",
		},
		Selectors: []string{
			"windows:process-name:custom-binary.exe",
			"windows:path:C:\\services\\{{.Name}}\\app.exe",
			"unix:uid:1005",
		},
	}

	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("RegisterWorkload failed: %v", err)
	}

	mockServer.mu.Lock()
	if len(mockServer.entries) != 1 {
		t.Fatalf("Expected 1 entry in mock server, got %d", len(mockServer.entries))
	}
	var registeredEntry *spiretypes.Entry
	for _, entry := range mockServer.entries {
		registeredEntry = entry
	}
	mockServer.mu.Unlock()

	expectedSelectors := []*spiretypes.Selector{
		{Type: "windows", Value: "process-name:custom-binary.exe"},
		{Type: "windows", Value: "path:C:\\services\\custom-service\\app.exe"},
		{Type: "unix", Value: "uid:1005"},
	}

	if !selectorsMatch(registeredEntry.Selectors, expectedSelectors) {
		t.Errorf("Custom selectors mismatch. Got %v, want %v", registeredEntry.Selectors, expectedSelectors)
	}
}
