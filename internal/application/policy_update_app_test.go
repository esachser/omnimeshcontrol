package application

import (
	"context"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

type testPolicyStore struct {
	ch chan outbound.TrafficPolicyEvent
}

func (s *testPolicyStore) PutTrafficPolicy(_ context.Context, _ *model.TrafficPolicy) error {
	return nil
}

func (s *testPolicyStore) GetTrafficPolicy(_ context.Context, _, _ string) (*model.TrafficPolicy, error) {
	return nil, nil
}

func (s *testPolicyStore) GetPolicyByService(_ context.Context, _ model.ServiceID) (*model.TrafficPolicy, error) {
	return nil, nil
}

func (s *testPolicyStore) ListTrafficPolicies(_ context.Context, _ string) ([]*model.TrafficPolicy, error) {
	return nil, nil
}

func (s *testPolicyStore) DeleteTrafficPolicy(_ context.Context, _, _ string) error {
	return nil
}

func (s *testPolicyStore) WatchTrafficPolicies(_ context.Context) (<-chan outbound.TrafficPolicyEvent, error) {
	return s.ch, nil
}

type testScopeDistributor struct {
	updates chan model.ScopeUpdate
}

func (d *testScopeDistributor) PushUpdate(_ context.Context, _ string, _ model.ScopeUpdate) error {
	return nil
}

func (d *testScopeDistributor) BroadcastUpdate(_ context.Context, update model.ScopeUpdate) error {
	d.updates <- update
	return nil
}

func TestTrafficPolicyUpdateService_BroadcastsUpdates(t *testing.T) {
	policy := &model.TrafficPolicy{
		Name:          "policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "api"},
	}

	events := make(chan outbound.TrafficPolicyEvent, 1)
	updates := make(chan model.ScopeUpdate, 1)

	store := &testPolicyStore{ch: events}
	dist := &testScopeDistributor{updates: updates}

	service := NewTrafficPolicyUpdateService(store, dist, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = service.Run(ctx)
		close(done)
	}()

	events <- outbound.TrafficPolicyEvent{Type: outbound.TrafficPolicyEventPut, Policy: policy}

	select {
	case update := <-updates:
		if update.Type != model.ScopeUpdateModify {
			t.Fatalf("update type = %v, want %v", update.Type, model.ScopeUpdateModify)
		}
		if len(update.TrafficPolicies) != 1 {
			t.Fatalf("expected 1 policy, got %d", len(update.TrafficPolicies))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast update")
	}

	cancel()
	close(events)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after context cancel")
	}
}
