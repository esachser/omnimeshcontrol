package application

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

var svcTestCIDR = netip.MustParsePrefix("100.64.0.0/10")

func newTestServiceApp(t *testing.T) (*ServiceAppService, *mockResourceStore, *mockStateStore, *mockDNSManager, *mockRouteDistributor) {
	t.Helper()
	resources := newMockResourceStore()
	state := newMockStateStore()
	dns := newMockDNSManager()
	routes := newMockRouteDistributor()
	identity := newMockIdentityManager()
	nsApp := NewNamespaceAppService(resources, svcTestCIDR, nil)

	svc := NewServiceAppService(ServiceAppServiceConfig{
		Resources:  resources,
		State:      state,
		DNS:        dns,
		Routes:     routes,
		Identity:   identity,
		Namespaces: nsApp,
		DNSZone:    "omnimesh.local",
	})
	return svc, resources, state, dns, routes
}

func TestServiceAppService_Apply(t *testing.T) {
	app, resources, state, dns, routes := newTestServiceApp(t)
	ctx := context.Background()

	// Create namespace first.
	nsApp := NewNamespaceAppService(resources, svcTestCIDR, nil)
	if err := nsApp.Apply(ctx, &model.Namespace{Name: "production"}); err != nil {
		t.Fatalf("namespace apply: %v", err)
	}

	svc := &model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "order-service"},
		Protocol: model.ServiceProtocolHTTP,
		Port:     8080,
	}

	if err := app.Apply(ctx, svc); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify IP was allocated.
	if !svc.MeshAddr.IsValid() {
		t.Error("Apply() did not allocate mesh IP")
	}

	// Verify IP is in state store.
	addr, err := state.GetIPAllocation(ctx, svc.ID)
	if err != nil {
		t.Fatalf("IP not in state store: %v", err)
	}
	if addr != svc.MeshAddr {
		t.Errorf("state IP = %v, want %v", addr, svc.MeshAddr)
	}

	// Verify DNS record was created.
	dns.mu.Lock()
	fqdn := "order-service.production.omnimesh.local"
	dnsAddr, ok := dns.records[fqdn]
	dns.mu.Unlock()
	if !ok {
		t.Error("DNS record not created")
	}
	if dnsAddr != svc.MeshAddr {
		t.Errorf("DNS addr = %v, want %v", dnsAddr, svc.MeshAddr)
	}

	// Verify route was announced.
	routes.mu.Lock()
	_, routeOk := routes.routes[svc.ID.String()]
	routes.mu.Unlock()
	if !routeOk {
		t.Error("route not announced")
	}
}

func TestServiceAppService_Apply_MissingNamespace(t *testing.T) {
	app, _, _, _, _ := newTestServiceApp(t)
	ctx := context.Background()

	svc := &model.Service{
		ID: model.ServiceID{Namespace: "nonexistent", Name: "test"},
	}

	err := app.Apply(ctx, svc)
	if err == nil {
		t.Error("Apply() should fail when namespace doesn't exist")
	}
	if !errors.Is(err, domain.ErrNamespaceNotFound) {
		t.Errorf("Apply() error = %v, want %v", err, domain.ErrNamespaceNotFound)
	}
}

func TestServiceAppService_Apply_IdempotentIP(t *testing.T) {
	app, resources, _, _, _ := newTestServiceApp(t)
	ctx := context.Background()

	nsApp := NewNamespaceAppService(resources, svcTestCIDR, nil)
	nsApp.Apply(ctx, &model.Namespace{Name: "production"})

	svc := &model.Service{
		ID: model.ServiceID{Namespace: "production", Name: "idempotent"},
	}

	// First apply.
	if err := app.Apply(ctx, svc); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	firstAddr := svc.MeshAddr

	// Second apply should use the same IP.
	if err := app.Apply(ctx, svc); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if svc.MeshAddr != firstAddr {
		t.Errorf("second Apply() IP = %v, want %v (same as first)", svc.MeshAddr, firstAddr)
	}
}

func TestServiceAppService_GetAndDelete(t *testing.T) {
	app, resources, _, _, _ := newTestServiceApp(t)
	ctx := context.Background()

	nsApp := NewNamespaceAppService(resources, svcTestCIDR, nil)
	nsApp.Apply(ctx, &model.Namespace{Name: "production"})

	svc := &model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "to-delete"},
		Protocol: model.ServiceProtocolGRPC,
	}
	app.Apply(ctx, svc)

	// Get.
	got, err := app.Get(ctx, "production", "to-delete")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID.Name != "to-delete" {
		t.Errorf("Get() name = %q", got.ID.Name)
	}

	// Delete.
	if err := app.Delete(ctx, "production", "to-delete"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Get should fail.
	_, err = app.Get(ctx, "production", "to-delete")
	if err == nil {
		t.Error("Get() after Delete() should fail")
	}
}

func TestServiceAppService_List(t *testing.T) {
	app, resources, _, _, _ := newTestServiceApp(t)
	ctx := context.Background()

	nsApp := NewNamespaceAppService(resources, svcTestCIDR, nil)
	nsApp.Apply(ctx, &model.Namespace{Name: "production"})

	for _, name := range []string{"svc-a", "svc-b", "svc-c"} {
		app.Apply(ctx, &model.Service{
			ID: model.ServiceID{Namespace: "production", Name: name},
		})
	}

	services, err := app.List(ctx, "production")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(services) != 3 {
		t.Errorf("List() = %d services, want 3", len(services))
	}
}
