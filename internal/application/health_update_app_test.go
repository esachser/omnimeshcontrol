package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

func TestHealthUpdateService_BroadcastsOnPutEvent(t *testing.T) {
	distributor := newMockScopeDistributor()
	logger := slog.Default()

	// Create a health store mock that supports a controllable watch channel.
	watchableStore := &watchableMockHealthStore{
		mockEndpointHealthStore: *newMockEndpointHealthStore(),
		eventCh:                make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start the service in a goroutine.
	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	// Send a health put event.
	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		Address:   "10.0.1.5:8080",
		Status:    model.EndpointHealthUnhealthy,
		NodeID:    "node-1",
	}
	watchableStore.eventCh <- outbound.EndpointHealthEvent{
		Type:   outbound.EndpointHealthEventPut,
		Health: health,
	}

	// Give the goroutine time to process.
	time.Sleep(100 * time.Millisecond)

	distributor.mu.Lock()
	broadcasts := len(distributor.broadcasts)
	distributor.mu.Unlock()

	if broadcasts != 1 {
		t.Fatalf("expected 1 broadcast, got %d", broadcasts)
	}

	distributor.mu.Lock()
	bc := distributor.broadcasts[0]
	distributor.mu.Unlock()

	if bc.Type != model.ScopeUpdateModify {
		t.Errorf("expected ScopeUpdateModify, got %v", bc.Type)
	}
	if len(bc.EndpointHealth) != 1 {
		t.Fatalf("expected 1 health entry, got %d", len(bc.EndpointHealth))
	}

	cancel()
	<-done
}

func TestHealthUpdateService_BroadcastsOnDeleteEvent(t *testing.T) {
	distributor := newMockScopeDistributor()
	logger := slog.Default()

	watchableStore := &watchableMockHealthStore{
		mockEndpointHealthStore: *newMockEndpointHealthStore(),
		eventCh:                make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	// Send a delete event.
	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		Address:   "10.0.1.5:8080",
		Status:    model.EndpointHealthUnhealthy,
		NodeID:    "node-1",
	}
	watchableStore.eventCh <- outbound.EndpointHealthEvent{
		Type:   outbound.EndpointHealthEventDelete,
		Health: health,
	}

	time.Sleep(100 * time.Millisecond)

	distributor.mu.Lock()
	defer distributor.mu.Unlock()

	if len(distributor.broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(distributor.broadcasts))
	}
	if distributor.broadcasts[0].Type != model.ScopeUpdateRemove {
		t.Errorf("expected ScopeUpdateRemove for delete event, got %v", distributor.broadcasts[0].Type)
	}

	cancel()
	<-done
}

func TestHealthUpdateService_StopsOnContextCancel(t *testing.T) {
	distributor := newMockScopeDistributor()
	logger := slog.Default()

	watchableStore := &watchableMockHealthStore{
		mockEndpointHealthStore: *newMockEndpointHealthStore(),
		eventCh:                make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	// Cancel immediately.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error on cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// watchableMockHealthStore extends mockEndpointHealthStore with a controllable event channel.
type watchableMockHealthStore struct {
	mockEndpointHealthStore
	eventCh chan outbound.EndpointHealthEvent
}

func (m *watchableMockHealthStore) WatchEndpointHealth(_ context.Context) (<-chan outbound.EndpointHealthEvent, error) {
	return m.eventCh, nil
}
