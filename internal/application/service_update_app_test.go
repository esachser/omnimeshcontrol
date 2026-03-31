package application

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// mockWatchableResourceStore extends mockResourceStore with a controllable Watch channel.
type mockWatchableResourceStore struct {
	*mockResourceStore
	watchCh chan outbound.ResourceEvent
}

func newMockWatchableResourceStore() *mockWatchableResourceStore {
	return &mockWatchableResourceStore{
		mockResourceStore: newMockResourceStore(),
		watchCh:           make(chan outbound.ResourceEvent, 16),
	}
}

func (m *mockWatchableResourceStore) Watch(_ context.Context, _ string) (<-chan outbound.ResourceEvent, error) {
	return m.watchCh, nil
}

func TestServiceUpdateService_Run_BroadcastsOnPut(t *testing.T) {
	resources := newMockWatchableResourceStore()
	state := newMockStateStore()
	dist := newMockScopeDistributor()

	svc := newServiceUpdateService(resources, state, dist)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	// Emit a PUT event for a service resource.
	resource := serviceToResource(&model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "order-service"},
		Protocol: model.ServiceProtocolHTTP,
		Port:     8080,
	})
	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventPut,
		Key:      serviceResourcePrefix + "production/order-service",
		Resource: resource,
	}
	// Close the channel to signal end of stream → Run() returns nil.
	close(resources.watchCh)

	if err := <-done; err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	dist.mu.Lock()
	broadcasts := dist.broadcasts
	dist.mu.Unlock()

	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast after PUT, got %d", len(broadcasts))
	}
	u := broadcasts[0]
	if u.Type != model.ScopeUpdateModify {
		t.Errorf("broadcast type = %v, want ScopeUpdateModify", u.Type)
	}
	if len(u.Services) != 1 || u.Services[0].ID.Name != "order-service" {
		t.Errorf("unexpected broadcast services: %v", u.Services)
	}
}

func TestServiceUpdateService_Run_BroadcastsOnDelete(t *testing.T) {
	resources := newMockWatchableResourceStore()
	state := newMockStateStore()
	dist := newMockScopeDistributor()

	svc := newServiceUpdateService(resources, state, dist)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	// Emit a DELETE event — ResourceStore.Watch provides the previous value via PrevKV.
	resource := serviceToResource(&model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "gone-service"},
		Protocol: model.ServiceProtocolTCP,
	})
	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      serviceResourcePrefix + "production/gone-service",
		Resource: resource, // populated from PrevKV by the real implementation
	}
	close(resources.watchCh)

	if err := <-done; err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	dist.mu.Lock()
	broadcasts := dist.broadcasts
	dist.mu.Unlock()

	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast after DELETE, got %d", len(broadcasts))
	}
	u := broadcasts[0]
	if u.Type != model.ScopeUpdateRemove {
		t.Errorf("broadcast type = %v, want ScopeUpdateRemove", u.Type)
	}
	if len(u.Services) != 1 || u.Services[0].ID.Name != "gone-service" {
		t.Errorf("unexpected broadcast services: %v", u.Services)
	}
}

func TestServiceUpdateService_Run_SkipsNilResource(t *testing.T) {
	resources := newMockWatchableResourceStore()
	state := newMockStateStore()
	dist := newMockScopeDistributor()

	svc := newServiceUpdateService(resources, state, dist)

	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	// Event with no resource — should be silently skipped.
	resources.watchCh <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      serviceResourcePrefix + "production/missing",
		Resource: nil,
	}
	close(resources.watchCh)

	<-done

	dist.mu.Lock()
	broadcasts := dist.broadcasts
	dist.mu.Unlock()

	if len(broadcasts) != 0 {
		t.Errorf("expected no broadcasts for nil-resource event, got %d", len(broadcasts))
	}
}

// newServiceUpdateService is a test helper that creates a ServiceUpdateService
// using the interface-narrow mock stores.
func newServiceUpdateService(
	resources outbound.ResourceStore,
	state outbound.StateStore,
	dist outbound.ScopeDistributor,
) *ServiceUpdateService {
	return NewServiceUpdateService(resources, state, dist, nil)
}
