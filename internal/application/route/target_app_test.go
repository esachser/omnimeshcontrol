package route

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestTargetAppService_ApplyAndGet(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	app := NewTargetAppService(resources, nil)
	ctx := context.Background()

	target := &model.NetworkTarget{
		Name:           "k8s-cluster-1",
		GatewayAddress: "gateway.k8s-cluster-1.local:8443",
	}

	if err := app.Apply(ctx, target); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got, err := app.Get(ctx, "k8s-cluster-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "k8s-cluster-1" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "k8s-cluster-1")
	}
	if got.GatewayAddress != "gateway.k8s-cluster-1.local:8443" {
		t.Errorf("Get().GatewayAddress = %q, want %q", got.GatewayAddress, "gateway.k8s-cluster-1.local:8443")
	}
}

func TestTargetAppService_ApplyValidation(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	app := NewTargetAppService(resources, nil)
	ctx := context.Background()

	target := &model.NetworkTarget{
		Name: "",
	}

	err := app.Apply(ctx, target)
	if err == nil {
		t.Error("Apply() with missing name should fail")
	}
}

func TestTargetAppService_Delete(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	app := NewTargetAppService(resources, nil)
	ctx := context.Background()

	target := &model.NetworkTarget{Name: "test-target"}
	app.Apply(ctx, target)

	if err := app.Delete(ctx, "test-target"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := app.Get(ctx, "test-target")
	if err == nil {
		t.Error("Get() after Delete() should fail")
	}
}

func TestTargetAppService_List(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	app := NewTargetAppService(resources, nil)
	ctx := context.Background()

	targets := []string{"target-a", "target-b", "target-c"}
	for _, name := range targets {
		app.Apply(ctx, &model.NetworkTarget{Name: name})
	}

	list, err := app.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != len(targets) {
		t.Errorf("List() = %d targets, want %d", len(list), len(targets))
	}
}
