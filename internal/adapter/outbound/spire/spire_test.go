package spire

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
			if !spiffeIDsEqual(entry.SpiffeId, filterID) {
				continue
			}
		}
		matched = append(matched, applyEntryOutputMask(entry, req.GetOutputMask()))
	}

	return &entryv1.ListEntriesResponse{Entries: matched}, nil
}

func applyEntryOutputMask(entry *spiretypes.Entry, mask *spiretypes.EntryMask) *spiretypes.Entry {
	if entry == nil {
		return nil
	}
	if mask == nil {
		return entry
	}
	out := &spiretypes.Entry{
		Id: entry.Id,
	}
	if mask.SpiffeId {
		out.SpiffeId = entry.SpiffeId
	}
	if mask.ParentId {
		out.ParentId = entry.ParentId
	}
	if mask.Selectors {
		out.Selectors = entry.Selectors
	}
	return out
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

func TestSpireIdentityManager_RegisterWorkload_MultipleSelectorSets(t *testing.T) {
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
		SelectorSets: [][]string{
			{"windows:process-name:{{.Name}}.exe"},
			{"unix:user:{{.Name}}"},
		},
	}

	mgr, err := NewIdentityManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewIdentityManager failed: %v", err)
	}
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Register with distinct Windows and Linux selector sets.
	identity := model.ServiceIdentity{
		SPIFFEID: "spiffe://omnimesh.local/ns/production/sa/multi-os-service",
		ServiceID: model.ServiceID{
			Namespace: "production",
			Name:      "multi-os-service",
		},
		SelectorSets: [][]string{
			{"windows:user_sid:S-1-5-21-1001", "windows:sha256:abcdef123456"},
			{"unix:user:{{.Name}}", "unix:pid:4321"},
		},
	}

	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("RegisterWorkload failed: %v", err)
	}

	assertMockEntries := func(wantSets [][]*spiretypes.Selector) {
		t.Helper()
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		if len(mockServer.entries) != len(wantSets) {
			t.Fatalf("expected %d SPIRE entries, got %d", len(wantSets), len(mockServer.entries))
		}
		for _, want := range wantSets {
			found := false
			for _, entry := range mockServer.entries {
				if selectorsMatch(entry.Selectors, want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("missing expected SPIRE entry with selectors %v", want)
			}
		}
	}

	initialWant := [][]*spiretypes.Selector{
		{
			{Type: "windows", Value: "user_sid:S-1-5-21-1001"},
			{Type: "windows", Value: "sha256:abcdef123456"},
		},
		{
			{Type: "unix", Value: "user:multi-os-service"},
			{Type: "unix", Value: "pid:4321"},
		},
	}
	assertMockEntries(initialWant)

	// 2. Re-registering with the same selector sets is idempotent.
	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("re-RegisterWorkload failed: %v", err)
	}
	assertMockEntries(initialWant)

	// 3. Updating selector sets replaces stale entries cleanly.
	identity.SelectorSets = [][]string{
		{"windows:user_sid:S-1-5-21-1001", "windows:sha256:999999fedcba"},
		{"unix:user:{{.Name}}", "unix:pid:8765"},
	}
	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("update RegisterWorkload failed: %v", err)
	}
	updatedWant := [][]*spiretypes.Selector{
		{
			{Type: "windows", Value: "user_sid:S-1-5-21-1001"},
			{Type: "windows", Value: "sha256:999999fedcba"},
		},
		{
			{Type: "unix", Value: "user:multi-os-service"},
			{Type: "unix", Value: "pid:8765"},
		},
	}
	assertMockEntries(updatedWant)

	// 4. Deregister removes all entries for the service.
	if err := mgr.DeregisterWorkload(ctx, identity.ServiceID); err != nil {
		t.Fatalf("DeregisterWorkload failed: %v", err)
	}
	mockServer.mu.Lock()
	if len(mockServer.entries) != 0 {
		t.Errorf("expected 0 entries after deregister, got %d", len(mockServer.entries))
	}
	mockServer.mu.Unlock()
}

func TestParseSelectorSets(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want [][]string
	}{
		{
			name: "empty",
			raw:  "",
			want: nil,
		},
		{
			name: "single set with multiple selectors",
			raw:  "windows:user_sid:S-1-5-21-1, windows:sha256:abc",
			want: [][]string{{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"}},
		},
		{
			name: "windows and linux sets",
			raw:  "windows:user_sid:S-1-5-21-1,windows:sha256:abc;unix:user:{{.Name}},unix:pid:1234",
			want: [][]string{
				{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
				{"unix:user:{{.Name}}", "unix:pid:1234"},
			},
		},
		{
			name: "ignores empty sets and extra delimiters",
			raw:  " ; windows:process-name:{{.Name}}.exe ; ; unix:user:{{.Name}}, ; ",
			want: [][]string{
				{"windows:process-name:{{.Name}}.exe"},
				{"unix:user:{{.Name}}"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSelectorSets(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseSelectorSets(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

type fakeEntryClient struct {
	entryv1.EntryClient
	mu sync.Mutex

	entries         map[string]*spiretypes.Entry
	nextID          int
	pageSize        int
	dedupSelectors  bool
	listCalls       []*entryv1.ListEntriesRequest
	createCalls     [][]*spiretypes.Entry
	deleteCalls     [][]string
	batchCreateHook func(req *entryv1.BatchCreateEntryRequest) (*entryv1.BatchCreateEntryResponse, error)
	batchDeleteHook func(req *entryv1.BatchDeleteEntryRequest) (*entryv1.BatchDeleteEntryResponse, error)
}

func newFakeEntryClient() *fakeEntryClient {
	return &fakeEntryClient{
		entries: make(map[string]*spiretypes.Entry),
	}
}

func (f *fakeEntryClient) seedEntry(id string, spiffeID, parentID *spiretypes.SPIFFEID, selectors []*spiretypes.Selector) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[id] = &spiretypes.Entry{
		Id:        id,
		SpiffeId:  spiffeID,
		ParentId:  parentID,
		Selectors: selectors,
	}
}

func (f *fakeEntryClient) snapshotEntries() []*spiretypes.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.entries))
	for id := range f.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*spiretypes.Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, f.entries[id])
	}
	return out
}

func (f *fakeEntryClient) ListEntries(_ context.Context, req *entryv1.ListEntriesRequest, _ ...grpc.CallOption) (*entryv1.ListEntriesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCalls = append(f.listCalls, req)

	ids := make([]string, 0, len(f.entries))
	for id := range f.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var matched []*spiretypes.Entry
	for _, id := range ids {
		entry := f.entries[id]
		if req.GetFilter() != nil && req.GetFilter().GetBySpiffeId() != nil {
			if !spiffeIDsEqual(entry.SpiffeId, req.GetFilter().GetBySpiffeId()) {
				continue
			}
		}
		matched = append(matched, applyEntryOutputMask(entry, req.GetOutputMask()))
	}

	pageSize := f.pageSize
	if req.GetPageSize() > 0 && (pageSize == 0 || int(req.GetPageSize()) < pageSize) {
		pageSize = int(req.GetPageSize())
	}

	start := 0
	if req.GetPageToken() != "" {
		parsed, err := strconv.Atoi(req.GetPageToken())
		if err != nil || parsed < 0 || parsed > len(matched) {
			return nil, fmt.Errorf("invalid page token %q", req.GetPageToken())
		}
		start = parsed
	}

	end := len(matched)
	nextToken := ""
	if pageSize > 0 && start+pageSize < len(matched) {
		end = start + pageSize
		nextToken = strconv.Itoa(end)
	}

	return &entryv1.ListEntriesResponse{
		Entries:       matched[start:end],
		NextPageToken: nextToken,
	}, nil
}

func (f *fakeEntryClient) BatchCreateEntry(_ context.Context, req *entryv1.BatchCreateEntryRequest, _ ...grpc.CallOption) (*entryv1.BatchCreateEntryResponse, error) {
	f.mu.Lock()
	f.createCalls = append(f.createCalls, req.GetEntries())
	hook := f.batchCreateHook
	f.mu.Unlock()

	if hook != nil {
		return hook(req)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	results := make([]*entryv1.BatchCreateEntryResponse_Result, 0, len(req.GetEntries()))
	for _, entry := range req.GetEntries() {
		selectors := entry.Selectors
		if f.dedupSelectors {
			selectors = deduplicateProtoSelectors(selectors)
		}

		var existingEntry *spiretypes.Entry
		for _, e := range f.entries {
			if spiffeIDsEqual(e.SpiffeId, entry.SpiffeId) &&
				spiffeIDsEqual(e.ParentId, entry.ParentId) &&
				selectorsEqual(e.Selectors, selectors) {
				existingEntry = e
				break
			}
		}
		if existingEntry != nil {
			results = append(results, &entryv1.BatchCreateEntryResponse_Result{
				Status: &spiretypes.Status{
					Code:    int32(codes.AlreadyExists),
					Message: "entry already exists",
				},
				Entry: existingEntry,
			})
			continue
		}

		f.nextID++
		id := fmt.Sprintf("fake-entry-%d", f.nextID)
		created := &spiretypes.Entry{
			Id:        id,
			SpiffeId:  entry.SpiffeId,
			ParentId:  entry.ParentId,
			Selectors: selectors,
		}
		f.entries[id] = created
		results = append(results, &entryv1.BatchCreateEntryResponse_Result{
			Status: &spiretypes.Status{Code: int32(codes.OK)},
			Entry:  created,
		})
	}

	return &entryv1.BatchCreateEntryResponse{Results: results}, nil
}

func (f *fakeEntryClient) BatchDeleteEntry(_ context.Context, req *entryv1.BatchDeleteEntryRequest, _ ...grpc.CallOption) (*entryv1.BatchDeleteEntryResponse, error) {
	f.mu.Lock()
	f.deleteCalls = append(f.deleteCalls, append([]string(nil), req.GetIds()...))
	hook := f.batchDeleteHook
	f.mu.Unlock()

	if hook != nil {
		return hook(req)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	results := make([]*entryv1.BatchDeleteEntryResponse_Result, 0, len(req.GetIds()))
	for _, id := range req.GetIds() {
		if _, ok := f.entries[id]; ok {
			delete(f.entries, id)
			results = append(results, &entryv1.BatchDeleteEntryResponse_Result{
				Id:     id,
				Status: &spiretypes.Status{Code: int32(codes.OK)},
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

func deduplicateProtoSelectors(in []*spiretypes.Selector) []*spiretypes.Selector {
	seen := make(map[string]struct{}, len(in))
	out := make([]*spiretypes.Selector, 0, len(in))
	for _, s := range in {
		key := s.Type + "\x00" + s.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func newTestManager(t *testing.T, cfg Config, client entryv1.EntryClient) *SpireIdentityManager {
	t.Helper()
	if cfg.TrustDomain == "" {
		cfg.TrustDomain = "omnimesh.local"
	}
	if len(cfg.ParentIDs) == 0 {
		cfg.ParentIDs = []string{"spiffe://omnimesh.local/agents/dev-agent"}
	}
	if len(cfg.SelectorSets) == 0 && len(cfg.Selectors) == 0 {
		cfg.SelectorSets = [][]string{
			{"windows:process-name:{{.Name}}.exe"},
			{"unix:user:{{.Name}}"},
		}
	}
	return &SpireIdentityManager{
		cfg:    cfg,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: client,
	}
}

func TestSpireIdentityManager_Unit_ServiceUpdatesCleanStaleEntries(t *testing.T) {
	serviceID := model.ServiceID{Namespace: "production", Name: "payment-service"}
	spiffeID := "spiffe://omnimesh.local/ns/production/sa/payment-service"

	tests := []struct {
		name              string
		initialCfg        Config
		initialIdentity   model.ServiceIdentity
		updatedCfg        Config
		updatedIdentity   model.ServiceIdentity
		wantCreateCalls   int
		wantDeleteCalls   int
		wantSelectorSets  [][]*spiretypes.Selector
		wantParentIDCount int
	}{
		{
			name: "flat selectors changed replaces old entry",
			initialIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
				Selectors: []string{"windows:user_sid:S-1-5-21-1"},
			},
			updatedIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
				Selectors: []string{"windows:user_sid:S-1-5-21-2", "windows:sha256:new"},
			},
			wantCreateCalls: 1,
			wantDeleteCalls: 1,
			wantSelectorSets: [][]*spiretypes.Selector{
				{{Type: "windows", Value: "user_sid:S-1-5-21-2"}, {Type: "windows", Value: "sha256:new"}},
			},
		},
		{
			name: "shrinking selector sets deletes removed set without creating",
			initialIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1"},
					{"unix:user:payment"},
				},
			},
			updatedIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1"},
				},
			},
			wantCreateCalls: 0,
			wantDeleteCalls: 1,
			wantSelectorSets: [][]*spiretypes.Selector{
				{{Type: "windows", Value: "user_sid:S-1-5-21-1"}},
			},
		},
		{
			name: "clearing custom selectors falls back to default selector sets",
			initialIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
				Selectors: []string{"windows:path:C:\\custom\\app.exe"},
			},
			updatedIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
			},
			wantCreateCalls: 1,
			wantDeleteCalls: 1,
			wantSelectorSets: [][]*spiretypes.Selector{
				{{Type: "windows", Value: "process-name:payment-service.exe"}},
				{{Type: "unix", Value: "user:payment-service"}},
			},
		},
		{
			name: "parent IDs changed removes entries for removed parents",
			initialCfg: Config{
				TrustDomain: "omnimesh.local",
				ParentIDs: []string{
					"spiffe://omnimesh.local/agents/agent-a",
					"spiffe://omnimesh.local/agents/agent-b",
				},
				SelectorSets: [][]string{{"unix:user:{{.Name}}"}},
			},
			initialIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
			},
			updatedCfg: Config{
				TrustDomain: "omnimesh.local",
				ParentIDs: []string{
					"spiffe://omnimesh.local/agents/agent-b",
					"spiffe://omnimesh.local/agents/agent-c",
				},
				SelectorSets: [][]string{{"unix:user:{{.Name}}"}},
			},
			updatedIdentity: model.ServiceIdentity{
				SPIFFEID:  spiffeID,
				ServiceID: serviceID,
			},
			wantCreateCalls:   1,
			wantDeleteCalls:   1,
			wantParentIDCount: 2,
			wantSelectorSets: [][]*spiretypes.Selector{
				{{Type: "unix", Value: "user:payment-service"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeEntryClient()
			ctx := context.Background()

			mgr := newTestManager(t, tc.initialCfg, client)
			if err := mgr.RegisterWorkload(ctx, tc.initialIdentity); err != nil {
				t.Fatalf("initial RegisterWorkload() error = %v", err)
			}

			// Reset call counters before update.
			client.mu.Lock()
			client.createCalls = nil
			client.deleteCalls = nil
			client.mu.Unlock()

			if len(tc.updatedCfg.ParentIDs) > 0 || len(tc.updatedCfg.SelectorSets) > 0 || len(tc.updatedCfg.Selectors) > 0 {
				mgr = newTestManager(t, tc.updatedCfg, client)
			}

			if err := mgr.RegisterWorkload(ctx, tc.updatedIdentity); err != nil {
				t.Fatalf("update RegisterWorkload() error = %v", err)
			}

			client.mu.Lock()
			gotCreateCalls := len(client.createCalls)
			gotDeleteCalls := len(client.deleteCalls)
			client.mu.Unlock()

			if gotCreateCalls != tc.wantCreateCalls {
				t.Errorf("BatchCreateEntry calls = %d, want %d", gotCreateCalls, tc.wantCreateCalls)
			}
			if gotDeleteCalls != tc.wantDeleteCalls {
				t.Errorf("BatchDeleteEntry calls = %d, want %d", gotDeleteCalls, tc.wantDeleteCalls)
			}

			entries := client.snapshotEntries()
			wantCount := len(tc.wantSelectorSets)
			if tc.wantParentIDCount > 0 {
				wantCount *= tc.wantParentIDCount
			}
			if len(entries) != wantCount {
				t.Fatalf("remaining entries = %d, want %d", len(entries), wantCount)
			}
			for _, wantSel := range tc.wantSelectorSets {
				found := false
				for _, e := range entries {
					if selectorsEqual(e.Selectors, wantSel) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected selectors %v in remaining entries", wantSel)
				}
			}
		})
	}
}

func TestSpireIdentityManager_Unit_PaginatedListAndCleanup(t *testing.T) {
	client := newFakeEntryClient()
	client.pageSize = 1
	ctx := context.Background()

	spiffeID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/payment-service")
	parentID, _ := parseSPIFFEID("spiffe://omnimesh.local/agents/dev-agent")

	// Seed 3 stale entries across 3 pages plus 1 unrelated service entry.
	client.seedEntry("stale-1", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:1001"}})
	client.seedEntry("stale-2", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:1002"}})
	client.seedEntry("stale-3", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:1003"}})
	otherID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/other-service")
	client.seedEntry("other-1", otherID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:9999"}})

	mgr := newTestManager(t, Config{}, client)
	identity := model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/payment-service",
		ServiceID: model.ServiceID{Namespace: "production", Name: "payment-service"},
		SelectorSets: [][]string{
			{"unix:uid:2001"},
			{"unix:uid:2002"},
		},
	}

	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("RegisterWorkload() error = %v", err)
	}

	// All 3 stale entries must be deleted; the 2 new ones + 1 unrelated remain.
	entries := client.snapshotEntries()
	if len(entries) != 3 {
		t.Fatalf("entries len = %d, want 3 (2 desired + 1 unrelated)", len(entries))
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Id, "stale-") {
			t.Errorf("stale entry %q was not cleaned up across paginated ListEntries", e.Id)
		}
	}

	// DeregisterWorkload must also paginate to delete all entries for payment-service.
	if err := mgr.DeregisterWorkload(ctx, identity.ServiceID); err != nil {
		t.Fatalf("DeregisterWorkload() error = %v", err)
	}
	entries = client.snapshotEntries()
	if len(entries) != 1 || entries[0].Id != "other-1" {
		t.Fatalf("after DeregisterWorkload entries = %v, want only other-1", entries)
	}
}

func TestSpireIdentityManager_Unit_RemovesDuplicateExistingEntries(t *testing.T) {
	client := newFakeEntryClient()
	ctx := context.Background()

	spiffeID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/order-service")
	parentID, _ := parseSPIFFEID("spiffe://omnimesh.local/agents/dev-agent")
	sel := []*spiretypes.Selector{{Type: "unix", Value: "user:order-service"}}

	client.seedEntry("dup-1", spiffeID, parentID, sel)
	client.seedEntry("dup-2", spiffeID, parentID, sel)

	mgr := newTestManager(t, Config{}, client)
	err := mgr.RegisterWorkload(ctx, model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/order-service",
		ServiceID: model.ServiceID{Namespace: "production", Name: "order-service"},
		Selectors: []string{"unix:user:order-service"},
	})
	if err != nil {
		t.Fatalf("RegisterWorkload() error = %v", err)
	}

	entries := client.snapshotEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after duplicate cleanup, got %d", len(entries))
	}
	if len(client.createCalls) != 0 {
		t.Errorf("expected 0 BatchCreateEntry calls, got %d", len(client.createCalls))
	}
	if len(client.deleteCalls) != 1 || len(client.deleteCalls[0]) != 1 || client.deleteCalls[0][0] != "dup-2" {
		t.Errorf("expected dup-2 to be deleted, got deleteCalls=%v", client.deleteCalls)
	}
}

func TestSpireIdentityManager_Unit_DeduplicatesSelectorsInSet(t *testing.T) {
	client := newFakeEntryClient()
	client.dedupSelectors = true
	ctx := context.Background()

	mgr := newTestManager(t, Config{}, client)
	identity := model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/order-service",
		ServiceID: model.ServiceID{Namespace: "production", Name: "order-service"},
		Selectors: []string{"unix:uid:1000", "unix:uid:1000"},
	}

	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("first RegisterWorkload() error = %v", err)
	}
	if len(client.snapshotEntries()) != 1 {
		t.Fatalf("expected 1 entry after first register, got %d", len(client.snapshotEntries()))
	}

	client.mu.Lock()
	client.createCalls = nil
	client.deleteCalls = nil
	client.mu.Unlock()

	// Re-applying with duplicate selectors in the set must match the deduplicated
	// existing entry and NOT trigger deletion of the live entry.
	if err := mgr.RegisterWorkload(ctx, identity); err != nil {
		t.Fatalf("second RegisterWorkload() error = %v", err)
	}
	if len(client.createCalls) != 0 || len(client.deleteCalls) != 0 {
		t.Errorf("expected idempotent no-op, got createCalls=%d deleteCalls=%d", len(client.createCalls), len(client.deleteCalls))
	}
	if len(client.snapshotEntries()) != 1 {
		t.Fatalf("expected live entry to remain intact, got %d entries", len(client.snapshotEntries()))
	}
}

func TestSpireIdentityManager_Unit_AlreadyExistsProtectsFromDelete(t *testing.T) {
	client := newFakeEntryClient()
	ctx := context.Background()

	spiffeID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/order-service")
	parentID, _ := parseSPIFFEID("spiffe://omnimesh.local/agents/dev-agent")

	// Seed an existing entry with stale selector and one that SPIRE will return as AlreadyExists.
	client.seedEntry("stale-id", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:1"}})
	client.seedEntry("race-id", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:2"}})

	// Force ListEntries to return both entries with stale selectors so diffEntries marks both for deletion,
	// while BatchCreateEntry reports race-id as AlreadyExists.
	client.batchCreateHook = func(req *entryv1.BatchCreateEntryRequest) (*entryv1.BatchCreateEntryResponse, error) {
		return &entryv1.BatchCreateEntryResponse{
			Results: []*entryv1.BatchCreateEntryResponse_Result{
				{
					Status: &spiretypes.Status{Code: int32(codes.AlreadyExists), Message: "already exists"},
					Entry:  &spiretypes.Entry{Id: "race-id"},
				},
			},
		}, nil
	}

	mgr := newTestManager(t, Config{}, client)
	err := mgr.RegisterWorkload(ctx, model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/order-service",
		ServiceID: model.ServiceID{Namespace: "production", Name: "order-service"},
		Selectors: []string{"unix:uid:3"},
	})
	if err != nil {
		t.Fatalf("RegisterWorkload() error = %v", err)
	}

	if len(client.deleteCalls) != 1 {
		t.Fatalf("expected 1 BatchDeleteEntry call, got %d", len(client.deleteCalls))
	}
	if !reflect.DeepEqual(client.deleteCalls[0], []string{"stale-id"}) {
		t.Errorf("deleted IDs = %v, want [stale-id] (race-id must be excluded)", client.deleteCalls[0])
	}
}

func TestSpireIdentityManager_Unit_PartialCreateFailureRollsBackAndKeepsStale(t *testing.T) {
	client := newFakeEntryClient()
	ctx := context.Background()

	spiffeID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/order-service")
	parentID, _ := parseSPIFFEID("spiffe://omnimesh.local/agents/dev-agent")
	client.seedEntry("stale-1", spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: "uid:old"}})

	client.batchCreateHook = func(req *entryv1.BatchCreateEntryRequest) (*entryv1.BatchCreateEntryResponse, error) {
		if len(req.Entries) != 2 {
			t.Fatalf("expected 2 entries in create request, got %d", len(req.Entries))
		}
		client.mu.Lock()
		client.entries["created-1"] = &spiretypes.Entry{
			Id:        "created-1",
			SpiffeId:  req.Entries[0].SpiffeId,
			ParentId:  req.Entries[0].ParentId,
			Selectors: req.Entries[0].Selectors,
		}
		client.mu.Unlock()

		return &entryv1.BatchCreateEntryResponse{
			Results: []*entryv1.BatchCreateEntryResponse_Result{
				{
					Status: &spiretypes.Status{Code: int32(codes.OK)},
					Entry:  &spiretypes.Entry{Id: "created-1"},
				},
				{
					Status: &spiretypes.Status{Code: int32(codes.InvalidArgument), Message: "bad selector"},
				},
			},
		}, nil
	}

	mgr := newTestManager(t, Config{}, client)
	err := mgr.RegisterWorkload(ctx, model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/order-service",
		ServiceID: model.ServiceID{Namespace: "production", Name: "order-service"},
		SelectorSets: [][]string{
			{"unix:uid:1001"},
			{"unix:uid:1002"},
		},
	})
	if err == nil {
		t.Fatal("expected error from partial BatchCreateEntry failure")
	}

	// created-1 must have been rolled back, and stale-1 must still be intact.
	entries := client.snapshotEntries()
	if len(entries) != 1 || entries[0].Id != "stale-1" {
		t.Fatalf("after rollback entries = %v, want only [stale-1]", entries)
	}
	if len(client.deleteCalls) != 1 || !reflect.DeepEqual(client.deleteCalls[0], []string{"created-1"}) {
		t.Errorf("rollback deleteCalls = %v, want [[created-1]]", client.deleteCalls)
	}
}

func TestSpireIdentityManager_Unit_BatchDeleteErrorsAndNotFound(t *testing.T) {
	ctx := context.Background()

	t.Run("NotFound treated as success", func(t *testing.T) {
		client := newFakeEntryClient()
		mgr := newTestManager(t, Config{}, client)
		if err := mgr.deleteEntriesByID(ctx, []string{"missing-1", "", "missing-1"}); err != nil {
			t.Errorf("deleteEntriesByID() error = %v, want nil", err)
		}
		if len(client.deleteCalls) != 1 || !reflect.DeepEqual(client.deleteCalls[0], []string{"missing-1"}) {
			t.Errorf("deleteCalls = %v, want [[missing-1]]", client.deleteCalls)
		}
	})

	t.Run("PermissionDenied returns error", func(t *testing.T) {
		client := newFakeEntryClient()
		client.batchDeleteHook = func(req *entryv1.BatchDeleteEntryRequest) (*entryv1.BatchDeleteEntryResponse, error) {
			return &entryv1.BatchDeleteEntryResponse{
				Results: []*entryv1.BatchDeleteEntryResponse_Result{
					{
						Id:     req.Ids[0],
						Status: &spiretypes.Status{Code: int32(codes.PermissionDenied), Message: "not admin"},
					},
				},
			}, nil
		}
		mgr := newTestManager(t, Config{}, client)
		if err := mgr.deleteEntriesByID(ctx, []string{"entry-1"}); err == nil {
			t.Fatal("expected error when BatchDeleteEntry returns PermissionDenied")
		}
	})
}

func TestSpireIdentityManager_Unit_BatchChunking(t *testing.T) {
	client := newFakeEntryClient()
	ctx := context.Background()

	spiffeID, _ := parseSPIFFEID("spiffe://omnimesh.local/ns/production/sa/chunked")
	parentID, _ := parseSPIFFEID("spiffe://omnimesh.local/agents/dev-agent")
	for i := 1; i <= 5; i++ {
		client.seedEntry(fmt.Sprintf("old-%d", i), spiffeID, parentID, []*spiretypes.Selector{{Type: "unix", Value: fmt.Sprintf("uid:%d", i)}})
	}

	mgr := newTestManager(t, Config{}, client)
	mgr.batchSize = 2

	err := mgr.RegisterWorkload(ctx, model.ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/production/sa/chunked",
		ServiceID: model.ServiceID{Namespace: "production", Name: "chunked"},
		SelectorSets: [][]string{
			{"unix:uid:101"},
			{"unix:uid:102"},
			{"unix:uid:103"},
			{"unix:uid:104"},
			{"unix:uid:105"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterWorkload() error = %v", err)
	}

	if len(client.createCalls) != 3 {
		t.Errorf("create batch calls = %d, want 3 (2+2+1)", len(client.createCalls))
	}
	if len(client.deleteCalls) != 3 {
		t.Errorf("delete batch calls = %d, want 3 (2+2+1)", len(client.deleteCalls))
	}

	entries := client.snapshotEntries()
	if len(entries) != 5 {
		t.Fatalf("remaining entries = %d, want 5", len(entries))
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Id, "old-") {
			t.Errorf("stale entry %q survived chunked delete", e.Id)
		}
	}
}

func TestSpireHelpers_NormalizationAndValidation(t *testing.T) {
	t.Run("ParseParentIDs", func(t *testing.T) {
		got := ParseParentIDs(" spiffe://omnimesh.local/agents/a , , spiffe://omnimesh.local/agents/b , spiffe://omnimesh.local/agents/a ")
		want := []string{
			"spiffe://omnimesh.local/agents/a",
			"spiffe://omnimesh.local/agents/b",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParseParentIDs() = %v, want %v", got, want)
		}
	})

	t.Run("SPIFFE ID normalization", func(t *testing.T) {
		a, err := parseSPIFFEID("  spiffe://OmniMesh.LOCAL/ns/prod/sa/api/  ")
		if err != nil {
			t.Fatalf("parseSPIFFEID() error = %v", err)
		}
		b := &spiretypes.SPIFFEID{TrustDomain: "omnimesh.local", Path: "ns/prod/sa/api"}
		if !spiffeIDsEqual(a, b) {
			t.Errorf("spiffeIDsEqual(%+v, %+v) = false, want true", a, b)
		}

		for _, bad := range []string{"", "http://omnimesh.local/a", "spiffe://", "spiffe:///no-td"} {
			if _, err := parseSPIFFEID(bad); err == nil {
				t.Errorf("parseSPIFFEID(%q) succeeded, want error", bad)
			}
		}
	})

	t.Run("resolveSelectors rejects empty type or value", func(t *testing.T) {
		mgr := newTestManager(t, Config{}, newFakeEntryClient())
		for _, bad := range []string{":value", "type:", "nocolon"} {
			if _, err := mgr.resolveSelectors("svc", "ns", []string{bad}); err == nil {
				t.Errorf("resolveSelectors(%q) succeeded, want error", bad)
			}
		}
	})
}
