package application

import (
	"context"
	"errors"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestNamespaceAppService_ApplyAndGet(t *testing.T) {
	resources := newMockResourceStore()
	app := NewNamespaceAppService(resources, nil)
	ctx := context.Background()

	ns := &model.Namespace{
		Name:      "production",
		Isolation: model.NamespaceIsolationStrict,
		Labels:    map[string]string{"env": "prod"},
	}

	if err := app.Apply(ctx, ns); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got, err := app.Get(ctx, "production")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "production" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "production")
	}
	if got.Isolation != model.NamespaceIsolationStrict {
		t.Errorf("Get().Isolation = %v, want Strict", got.Isolation)
	}
}

func TestNamespaceAppService_DeleteEmpty(t *testing.T) {
	resources := newMockResourceStore()
	app := NewNamespaceAppService(resources, nil)
	ctx := context.Background()

	app.Apply(ctx, &model.Namespace{Name: "empty-ns"})

	if err := app.Delete(ctx, "empty-ns", false); err != nil {
		t.Fatalf("Delete() empty namespace error = %v", err)
	}

	_, err := app.Get(ctx, "empty-ns")
	if err == nil {
		t.Error("Get() after Delete() should fail")
	}
}

func TestNamespaceAppService_DeleteNonEmpty(t *testing.T) {
	resources := newMockResourceStore()
	app := NewNamespaceAppService(resources, nil)
	ctx := context.Background()

	app.Apply(ctx, &model.Namespace{Name: "non-empty"})

	// Add a service to the namespace.
	resources.Put(ctx, "core.omnimesh.io/v1/Service/non-empty/svc-1", &model.Resource{
		Kind:     "Service",
		Metadata: model.ResourceMetadata{Name: "svc-1", Namespace: "non-empty"},
	})

	err := app.Delete(ctx, "non-empty", false)
	if err == nil {
		t.Error("Delete() non-empty namespace should fail")
	}
	if !errors.Is(err, domain.ErrNamespaceNotEmpty) {
		t.Errorf("Delete() error = %v, want %v", err, domain.ErrNamespaceNotEmpty)
	}

	// Force delete should succeed.
	if err := app.Delete(ctx, "non-empty", true); err != nil {
		t.Fatalf("Delete(force=true) error = %v", err)
	}
}

func TestNamespaceAppService_List(t *testing.T) {
	resources := newMockResourceStore()
	app := NewNamespaceAppService(resources, nil)
	ctx := context.Background()

	for _, name := range []string{"ns-a", "ns-b", "ns-c"} {
		app.Apply(ctx, &model.Namespace{Name: name})
	}

	namespaces, err := app.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(namespaces) != 3 {
		t.Errorf("List() = %d namespaces, want 3", len(namespaces))
	}
}
