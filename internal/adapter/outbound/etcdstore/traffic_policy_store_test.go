package etcdstore

import (
	"context"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

func TestEtcdTrafficPolicyStore_CRUD(t *testing.T) {
	embedded, cleanup := testEmbedded(t)
	defer cleanup()

	resourceStore := NewEtcdResourceStore(embedded.Client())
	store := NewEtcdTrafficPolicyStore(resourceStore)
	ctx := context.Background()

	policy := &model.TrafficPolicy{
		Name:          "orders-policy",
		Namespace:     "production",
		TargetService: model.ServiceID{Namespace: "production", Name: "orders"},
		Retry: &model.RetryPolicy{
			MaxRetries: 1,
			RetryOn:    []string{"connection-failure"},
		},
	}

	if err := store.PutTrafficPolicy(ctx, policy); err != nil {
		t.Fatalf("PutTrafficPolicy() error = %v", err)
	}

	got, err := store.GetTrafficPolicy(ctx, "production", "orders-policy")
	if err != nil {
		t.Fatalf("GetTrafficPolicy() error = %v", err)
	}
	if got.Name != policy.Name {
		t.Fatalf("GetTrafficPolicy() name = %q, want %q", got.Name, policy.Name)
	}

	list, err := store.ListTrafficPolicies(ctx, "production")
	if err != nil {
		t.Fatalf("ListTrafficPolicies() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListTrafficPolicies() len = %d, want 1", len(list))
	}

	bySvc, err := store.GetPolicyByService(ctx, policy.TargetService)
	if err != nil {
		t.Fatalf("GetPolicyByService() error = %v", err)
	}
	if bySvc.Name != policy.Name {
		t.Fatalf("GetPolicyByService() name = %q, want %q", bySvc.Name, policy.Name)
	}

	if err := store.DeleteTrafficPolicy(ctx, "production", "orders-policy"); err != nil {
		t.Fatalf("DeleteTrafficPolicy() error = %v", err)
	}
}

func TestEtcdTrafficPolicyStore_Watch(t *testing.T) {
	embedded, cleanup := testEmbedded(t)
	defer cleanup()

	resourceStore := NewEtcdResourceStore(embedded.Client())
	store := NewEtcdTrafficPolicyStore(resourceStore)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh, err := store.WatchTrafficPolicies(ctx)
	if err != nil {
		t.Fatalf("WatchTrafficPolicies() error = %v", err)
	}

	policy := &model.TrafficPolicy{
		Name:          "watch-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "api"},
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = store.PutTrafficPolicy(context.Background(), policy)
		time.Sleep(100 * time.Millisecond)
		_ = store.DeleteTrafficPolicy(context.Background(), "default", "watch-policy")
	}()

	receivedPut := false
	receivedDelete := false

	for !receivedPut || !receivedDelete {
		select {
		case event := <-eventCh:
			switch event.Type {
			case outbound.TrafficPolicyEventPut:
				receivedPut = true
			case outbound.TrafficPolicyEventDelete:
				receivedDelete = true
			}
		case <-ctx.Done():
			t.Fatalf("watch timed out waiting for events")
		}
	}
}
