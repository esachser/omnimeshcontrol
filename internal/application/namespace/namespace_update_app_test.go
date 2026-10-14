package namespace

import (
	"context"
	"net/netip"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

type mockWatchableResourceStore struct {
	*apptest.MockResourceStore
	watchCh chan outbound.ResourceEvent
}

func newMockWatchableResourceStore() *mockWatchableResourceStore {
	return &mockWatchableResourceStore{
		MockResourceStore: apptest.NewMockResourceStore(),
		watchCh:           make(chan outbound.ResourceEvent, 16),
	}
}

func (m *mockWatchableResourceStore) Watch(_ context.Context, _ string) (<-chan outbound.ResourceEvent, error) {
	return m.watchCh, nil
}

func TestNamespaceUpdateService_Run_BroadcastsOnPut(t *testing.T) {
	resources := newMockWatchableResourceStore()
	dist := apptest.NewMockScopeDistributor()

	svc := NewNamespaceUpdateService(resources, dist, nil)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	// Emit a PUT event for a namespace resource.
	ns := &model.Namespace{
		Name:      "production",
		Isolation: model.NamespaceIsolationStrict,
		CIDR:      netip.MustParsePrefix("100.64.1.0/24"),
		Subnets: []netip.Prefix{
			netip.MustParsePrefix("100.64.1.0/24"),
		},
	}
	resource := mapper.NamespaceToResource(ns)
	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventPut,
		Key:      namespaceResourcePrefix + "production",
		Resource: resource,
	}
	close(resources.watchCh)

	if err := <-done; err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	broadcasts := dist.GetBroadcasts()
	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast after PUT, got %d", len(broadcasts))
	}
	u := broadcasts[0]
	if u.Type != model.ScopeUpdateModify {
		t.Errorf("broadcast type = %v, want ScopeUpdateModify", u.Type)
	}
	if len(u.Namespaces) != 1 || u.Namespaces[0].Name != "production" {
		t.Errorf("unexpected broadcast namespaces: %v", u.Namespaces)
	}
	if u.Namespaces[0].CIDR.String() != "100.64.1.0/24" {
		t.Errorf("unexpected CIDR: %v", u.Namespaces[0].CIDR)
	}
	if len(u.Namespaces[0].Subnets) != 1 || u.Namespaces[0].Subnets[0].String() != "100.64.1.0/24" {
		t.Errorf("unexpected Subnets: %v", u.Namespaces[0].Subnets)
	}
}

func TestNamespaceUpdateService_Run_BroadcastsOnDelete(t *testing.T) {
	resources := newMockWatchableResourceStore()
	dist := apptest.NewMockScopeDistributor()

	svc := NewNamespaceUpdateService(resources, dist, nil)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	ns := &model.Namespace{
		Name:      "decommissioned",
		Isolation: model.NamespaceIsolationOpen,
		CIDR:      netip.MustParsePrefix("100.64.2.0/24"),
	}
	resource := mapper.NamespaceToResource(ns)
	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      namespaceResourcePrefix + "decommissioned",
		Resource: resource,
	}
	close(resources.watchCh)

	if err := <-done; err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	broadcasts := dist.GetBroadcasts()
	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast after DELETE, got %d", len(broadcasts))
	}
	u := broadcasts[0]
	if u.Type != model.ScopeUpdateRemove {
		t.Errorf("broadcast type = %v, want ScopeUpdateRemove", u.Type)
	}
	if len(u.Namespaces) != 1 || u.Namespaces[0].Name != "decommissioned" {
		t.Errorf("unexpected broadcast namespaces: %v", u.Namespaces)
	}
}

func TestNamespaceUpdateService_Run_SkipsNilResource(t *testing.T) {
	resources := newMockWatchableResourceStore()
	dist := apptest.NewMockScopeDistributor()

	svc := NewNamespaceUpdateService(resources, dist, nil)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      namespaceResourcePrefix + "missing",
		Resource: nil,
	}
	close(resources.watchCh)

	<-done

	broadcasts := dist.GetBroadcasts()
	if len(broadcasts) != 0 {
		t.Errorf("expected no broadcasts for nil-resource event, got %d", len(broadcasts))
	}
}
