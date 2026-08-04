package health

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

func TestHealthUpdateService_BroadcastsOnPutEvent(t *testing.T) {
	distributor := apptest.NewMockScopeDistributor()
	logger := slog.Default()

	watchableStore := &watchableMockHealthStore{
		MockEndpointHealthStore: *apptest.NewMockEndpointHealthStore(),
		eventCh:                 make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		RouteName: "edp1",
		Status:    model.EndpointHealthUnhealthy,
		NodeID:    "node-1",
	}
	watchableStore.eventCh <- outbound.EndpointHealthEvent{
		Type:   outbound.EndpointHealthEventPut,
		Health: health,
	}

	time.Sleep(100 * time.Millisecond)

	broadcasts := distributor.GetBroadcasts()

	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(broadcasts))
	}

	bc := broadcasts[0]

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
	distributor := apptest.NewMockScopeDistributor()
	logger := slog.Default()

	watchableStore := &watchableMockHealthStore{
		MockEndpointHealthStore: *apptest.NewMockEndpointHealthStore(),
		eventCh:                 make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		RouteName: "edp1",
		Status:    model.EndpointHealthUnhealthy,
		NodeID:    "node-1",
	}
	watchableStore.eventCh <- outbound.EndpointHealthEvent{
		Type:   outbound.EndpointHealthEventDelete,
		Health: health,
	}

	time.Sleep(100 * time.Millisecond)

	broadcasts := distributor.GetBroadcasts()

	if len(broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(broadcasts))
	}
	if broadcasts[0].Type != model.ScopeUpdateRemove {
		t.Errorf("expected ScopeUpdateRemove for delete event, got %v", broadcasts[0].Type)
	}

	cancel()
	<-done
}

func TestHealthUpdateService_StopsOnContextCancel(t *testing.T) {
	distributor := apptest.NewMockScopeDistributor()
	logger := slog.Default()

	watchableStore := &watchableMockHealthStore{
		MockEndpointHealthStore: *apptest.NewMockEndpointHealthStore(),
		eventCh:                 make(chan outbound.EndpointHealthEvent, 10),
	}

	svc := NewHealthUpdateService(watchableStore, distributor, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

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

type watchableMockHealthStore struct {
	apptest.MockEndpointHealthStore
	eventCh chan outbound.EndpointHealthEvent
}

func (m *watchableMockHealthStore) WatchEndpointHealth(_ context.Context) (<-chan outbound.EndpointHealthEvent, error) {
	return m.eventCh, nil
}
