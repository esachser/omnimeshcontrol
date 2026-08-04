package authz

import (
	"context"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

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

func TestAuthzPolicyUpdateService_BroadcastsOnPut(t *testing.T) {
	events := make(chan outbound.ResourceEvent, 1)
	updates := make(chan model.ScopeUpdate, 1)

	store := &testAuthzResourceStore{ch: events}
	dist := &testScopeDistributor{updates: updates}

	svc := NewAuthzPolicyUpdateService(store, dist, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = svc.Run(ctx)
		close(done)
	}()

	policy := &model.AuthorizationPolicy{
		Name:        "allow-order-to-payment",
		Namespace:   "production",
		Action:      model.PolicyActionAllow,
		Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
		Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
	}
	resource := AuthzPolicyToResource(policy)

	events <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventPut,
		Key:      "policy.omnimesh.io/v1/AuthorizationPolicy/production/allow-order-to-payment",
		Resource: resource,
	}

	select {
	case update := <-updates:
		if update.Type != model.ScopeUpdateModify {
			t.Fatalf("update type = %v, want %v", update.Type, model.ScopeUpdateModify)
		}
		if len(update.AuthzPolicies) != 1 {
			t.Fatalf("expected 1 authz policy, got %d", len(update.AuthzPolicies))
		}
		if update.AuthzPolicies[0].Name != "allow-order-to-payment" {
			t.Fatalf("policy name = %q, want %q", update.AuthzPolicies[0].Name, "allow-order-to-payment")
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

func TestAuthzPolicyUpdateService_BroadcastsOnDelete(t *testing.T) {
	events := make(chan outbound.ResourceEvent, 1)
	updates := make(chan model.ScopeUpdate, 1)

	store := &testAuthzResourceStore{ch: events}
	dist := &testScopeDistributor{updates: updates}

	svc := NewAuthzPolicyUpdateService(store, dist, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = svc.Run(ctx)
		close(done)
	}()

	policy := &model.AuthorizationPolicy{
		Name:        "deny-order-to-payment",
		Namespace:   "production",
		Action:      model.PolicyActionDeny,
		Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
		Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
	}
	resource := AuthzPolicyToResource(policy)

	events <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      "policy.omnimesh.io/v1/AuthorizationPolicy/production/deny-order-to-payment",
		Resource: resource,
	}

	select {
	case update := <-updates:
		if update.Type != model.ScopeUpdateRemove {
			t.Fatalf("update type = %v, want %v", update.Type, model.ScopeUpdateRemove)
		}
		if len(update.AuthzPolicies) != 1 {
			t.Fatalf("expected 1 authz policy, got %d", len(update.AuthzPolicies))
		}
		if update.AuthzPolicies[0].Name != "deny-order-to-payment" {
			t.Fatalf("policy name = %q, want %q", update.AuthzPolicies[0].Name, "deny-order-to-payment")
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

func TestAuthzPolicyUpdateService_SkipsMissingResource(t *testing.T) {
	events := make(chan outbound.ResourceEvent, 1)
	updates := make(chan model.ScopeUpdate, 1)

	store := &testAuthzResourceStore{ch: events}
	dist := &testScopeDistributor{updates: updates}

	svc := NewAuthzPolicyUpdateService(store, dist, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = svc.Run(ctx)
		close(done)
	}()

	events <- outbound.ResourceEvent{
		Type:     outbound.ResourceEventDelete,
		Key:      "policy.omnimesh.io/v1/AuthorizationPolicy/production/some-policy",
		Resource: nil,
	}

	select {
	case <-updates:
		t.Fatal("expected no broadcast for nil resource")
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	close(events)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after context cancel")
	}
}

func TestAuthzPolicyUpdateService_NilDependencies(t *testing.T) {
	svc := NewAuthzPolicyUpdateService(nil, &testScopeDistributor{}, nil)
	if err := svc.Run(context.Background()); err == nil {
		t.Fatal("expected error for nil resource store")
	}

	svc = NewAuthzPolicyUpdateService(&testAuthzResourceStore{}, nil, nil)
	if err := svc.Run(context.Background()); err == nil {
		t.Fatal("expected error for nil distributor")
	}
}

type testAuthzResourceStore struct {
	apptest.MockResourceStore
	ch chan outbound.ResourceEvent
}

func (s *testAuthzResourceStore) Watch(_ context.Context, _ string) (<-chan outbound.ResourceEvent, error) {
	return s.ch, nil
}
