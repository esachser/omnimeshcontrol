package application

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestRouteAppService_ApplyAndGet(t *testing.T) {
	resources := newMockResourceStore()
	app := NewRouteAppService(resources, nil)
	ctx := context.Background()

	route := &model.TargetRoute{
		Name:              "order-route",
		Namespace:         "production",
		TargetService:     "order-service",
		DestinationTarget: "k8s-cluster-1",
		Weight:            80,
		Failover: &model.FailoverPolicy{
			ActiveWhenTargetIsDown: "k8s-cluster-2",
		},
	}

	if err := app.Apply(ctx, route); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got, err := app.Get(ctx, "production", "order-route")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.Name != "order-route" || got.Namespace != "production" {
		t.Errorf("Get() metadata mismatch: got %v/%v", got.Namespace, got.Name)
	}
	if got.TargetService != "order-service" || got.DestinationTarget != "k8s-cluster-1" {
		t.Errorf("Get() specs mismatch")
	}
	if got.Weight != 80 {
		t.Errorf("Get().Weight = %d, want 80", got.Weight)
	}
	if got.Failover == nil || got.Failover.ActiveWhenTargetIsDown != "k8s-cluster-2" {
		t.Errorf("Get().Failover mismatch")
	}
}

func TestRouteAppService_ApplyValidation(t *testing.T) {
	resources := newMockResourceStore()
	app := NewRouteAppService(resources, nil)
	ctx := context.Background()

	tests := []struct {
		name      string
		route     *model.TargetRoute
		wantError bool
	}{
		{
			name: "missing name",
			route: &model.TargetRoute{
				Namespace: "default",
			},
			wantError: true,
		},
		{
			name: "missing namespace",
			route: &model.TargetRoute{
				Name: "route-1",
			},
			wantError: true,
		},
		{
			name: "valid route",
			route: &model.TargetRoute{
				Name:      "route-1",
				Namespace: "default",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := app.Apply(ctx, tt.route)
			if (err != nil) != tt.wantError {
				t.Errorf("Apply() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestRouteAppService_Delete(t *testing.T) {
	resources := newMockResourceStore()
	app := NewRouteAppService(resources, nil)
	ctx := context.Background()

	route := &model.TargetRoute{Name: "test-route", Namespace: "default"}
	app.Apply(ctx, route)

	if err := app.Delete(ctx, "default", "test-route"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := app.Get(ctx, "default", "test-route")
	if err == nil {
		t.Error("Get() after Delete() should fail")
	}
}

func TestRouteAppService_List(t *testing.T) {
	resources := newMockResourceStore()
	app := NewRouteAppService(resources, nil)
	ctx := context.Background()

	// Apply routes in different namespaces
	app.Apply(ctx, &model.TargetRoute{Name: "route-a", Namespace: "ns1"})
	app.Apply(ctx, &model.TargetRoute{Name: "route-b", Namespace: "ns1"})
	app.Apply(ctx, &model.TargetRoute{Name: "route-c", Namespace: "ns2"})

	// List all
	all, err := app.List(ctx, "")
	if err != nil {
		t.Fatalf("List() all error = %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List() all = %d routes, want 3", len(all))
	}

	// List by namespace
	ns1, err := app.List(ctx, "ns1")
	if err != nil {
		t.Fatalf("List() ns1 error = %v", err)
	}
	if len(ns1) != 2 {
		t.Errorf("List() ns1 = %d routes, want 2", len(ns1))
	}
}
