package scope

import (
	"context"
	"net/netip"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/authz"
	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/application/namespace"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestAgentScopeAppService_GetAgentScope_IncludesNamespaces(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	policyStore := apptest.NewMockTrafficPolicyStore()
	healthStore := apptest.NewMockEndpointHealthStore()
	depStore := apptest.NewMockDependencyStore()
	routes := apptest.NewMockRouteDistributor()
	defaultCIDR := netip.MustParsePrefix("100.64.0.0/16")

	nsApp := namespace.NewNamespaceAppService(resources, defaultCIDR, nil)
	authzApp := authz.NewAuthzAppService(resources, depStore, nil, nil)

	ctx := context.Background()

	// Apply two namespaces
	ns1 := &model.Namespace{
		Name:      "prod",
		Isolation: model.NamespaceIsolationStrict,
		CIDR:      netip.MustParsePrefix("100.64.1.0/24"),
		Subnets: []netip.Prefix{
			netip.MustParsePrefix("100.64.1.0/24"),
			netip.MustParsePrefix("100.64.2.0/24"),
		},
	}
	if err := nsApp.Apply(ctx, ns1); err != nil {
		t.Fatalf("nsApp.Apply(ns1) error = %v", err)
	}

	ns2 := &model.Namespace{
		Name:      "dev",
		Isolation: model.NamespaceIsolationOpen,
		CIDR:      netip.MustParsePrefix("100.64.3.0/24"),
	}
	if err := nsApp.Apply(ctx, ns2); err != nil {
		t.Fatalf("nsApp.Apply(ns2) error = %v", err)
	}

	scopeApp := NewAgentScopeAppService(
		resources,
		state,
		policyStore,
		healthStore,
		depStore,
		routes,
		nil,
		authzApp,
		nsApp,
		nil,
	)

	scope, err := scopeApp.GetAgentScope(ctx, "node-1")
	if err != nil {
		t.Fatalf("GetAgentScope error = %v", err)
	}

	if len(scope.Namespaces) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(scope.Namespaces))
	}

	nsMap := make(map[string]model.Namespace)
	for _, ns := range scope.Namespaces {
		nsMap[ns.Name] = ns
	}

	gotProd, ok := nsMap["prod"]
	if !ok {
		t.Fatal("missing 'prod' namespace in scope")
	}
	if gotProd.CIDR.String() != "100.64.1.0/24" {
		t.Errorf("prod CIDR = %v, want 100.64.1.0/24", gotProd.CIDR)
	}
	if len(gotProd.AllSubnets()) != 2 {
		t.Errorf("prod subnets count = %d, want 2", len(gotProd.AllSubnets()))
	}

	gotDev, ok := nsMap["dev"]
	if !ok {
		t.Fatal("missing 'dev' namespace in scope")
	}
	if gotDev.CIDR.String() != "100.64.3.0/24" {
		t.Errorf("dev CIDR = %v, want 100.64.3.0/24", gotDev.CIDR)
	}
	if len(gotDev.AllSubnets()) != 1 {
		t.Errorf("dev subnets count = %d, want 1", len(gotDev.AllSubnets()))
	}
}

func TestAgentScopeAppService_GetAgentScope_FallbackToResources(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	policyStore := apptest.NewMockTrafficPolicyStore()
	healthStore := apptest.NewMockEndpointHealthStore()
	depStore := apptest.NewMockDependencyStore()
	routes := apptest.NewMockRouteDistributor()
	authzApp := authz.NewAuthzAppService(resources, depStore, nil, nil)

	ctx := context.Background()

	// Put namespace directly in resources
	ns := &model.Namespace{
		Name:      "direct-ns",
		Isolation: model.NamespaceIsolationStrict,
		CIDR:      netip.MustParsePrefix("100.64.99.0/24"),
	}
	resources.Put(ctx, "core.omnimesh.io/v1/Namespace/direct-ns", mapper.NamespaceToResource(ns))

	// Instantiate scopeApp with nil namespaces manager
	scopeApp := NewAgentScopeAppService(
		resources,
		state,
		policyStore,
		healthStore,
		depStore,
		routes,
		nil,
		authzApp,
		nil, // nil NamespaceManager
		nil,
	)

	scope, err := scopeApp.GetAgentScope(ctx, "node-1")
	if err != nil {
		t.Fatalf("GetAgentScope error = %v", err)
	}

	if len(scope.Namespaces) != 1 {
		t.Fatalf("expected 1 namespace, got %d", len(scope.Namespaces))
	}
	if scope.Namespaces[0].Name != "direct-ns" {
		t.Errorf("namespace name = %q, want direct-ns", scope.Namespaces[0].Name)
	}
	if scope.Namespaces[0].CIDR.String() != "100.64.99.0/24" {
		t.Errorf("namespace CIDR = %v, want 100.64.99.0/24", scope.Namespaces[0].CIDR)
	}
}

func TestAgentScopeAppService_GetAgentScope_NormalizesServiceProtocols(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	policyStore := apptest.NewMockTrafficPolicyStore()
	healthStore := apptest.NewMockEndpointHealthStore()
	depStore := apptest.NewMockDependencyStore()
	routes := apptest.NewMockRouteDistributor()
	authzApp := authz.NewAuthzAppService(resources, depStore, nil, nil)

	ctx := context.Background()
	rawServices := map[string]string{
		"svc-auto":  `{"port":8080}`,
		"svc-http1": `{"protocol":"http/1.1","port":8081}`,
		"svc-h2c":   `{"protocol":"h2c","port":8082}`,
		"svc-grpc":  `{"protocol":"gRPC","port":50051}`,
		"svc-tcp":   `{"protocol":"tcp","port":6379}`,
	}
	for name, spec := range rawServices {
		resources.Put(ctx, "core.omnimesh.io/v1/Service/prod/"+name, &model.Resource{
			APIVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata: model.ResourceMetadata{
				Name:      name,
				Namespace: "prod",
			},
			Spec: []byte(spec),
		})
	}

	scopeApp := NewAgentScopeAppService(
		resources,
		state,
		policyStore,
		healthStore,
		depStore,
		routes,
		nil,
		authzApp,
		nil,
		nil,
	)

	scope, err := scopeApp.GetAgentScope(ctx, "node-1")
	if err != nil {
		t.Fatalf("GetAgentScope error = %v", err)
	}

	want := map[string]string{
		"svc-auto":  "auto",
		"svc-http1": "http1",
		"svc-h2c":   "http2",
		"svc-grpc":  "http2",
		"svc-tcp":   "tcp",
	}
	if len(scope.Services) != len(want) {
		t.Fatalf("expected %d services, got %d", len(want), len(scope.Services))
	}
	for _, svc := range scope.Services {
		expected, ok := want[svc.ID.Name]
		if !ok {
			t.Errorf("unexpected service %q", svc.ID.Name)
			continue
		}
		if got := svc.Protocol.AgentProtocol(); got != expected {
			t.Errorf("service %q AgentProtocol() = %q, want %q", svc.ID.Name, got, expected)
		}
	}
}
