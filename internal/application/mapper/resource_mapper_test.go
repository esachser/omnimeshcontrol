package mapper

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestTrafficPolicyToResource(t *testing.T) {
	policy := &model.TrafficPolicy{
		Name:          "payment-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "payment-service"},
		Retry: &model.RetryPolicy{
			MaxRetries:  2,
			RetryOn:     []string{"connection-failure", "5xx"},
			BackoffBase: 100 * time.Millisecond,
			BackoffMax:  2 * time.Second,
		},
		CircuitBreaker: &model.CircuitBreakerPolicy{
			FailureThreshold: 5,
			FailureWindow:    30 * time.Second,
			HalfOpenAfter:    10 * time.Second,
		},
		RateLimiter: &model.RateLimitPolicy{
			RequestsPerSecond:     100,
			BurstSize:             200,
			MaxConcurrentRequests: 50,
			LimitExceededAction:   model.RateLimitActionReject,
		},
		LoadBalancer: &model.LoadBalancerPolicy{
			Algorithm: model.LoadBalancerWeightedRoundRobin,
		},
		Routes: []model.TrafficRoute{
			{
				Destination: model.TrafficRouteDestination{Target: "aws-east-1", Labels: map[string]string{"version": "v1"}},
				Weight:      80,
			},
			{
				Destination: model.TrafficRouteDestination{Target: "aws-east-1", Labels: map[string]string{"version": "v2"}},
				Weight:      20,
			},
		},
	}

	resource, err := TrafficPolicyToResource(policy)
	if err != nil {
		t.Fatalf("TrafficPolicyToResource() error = %v", err)
	}

	if resource.Kind != "TrafficPolicy" {
		t.Errorf("expected kind TrafficPolicy, got %q", resource.Kind)
	}
	if resource.Metadata.Name != "payment-policy" {
		t.Errorf("expected name payment-policy, got %q", resource.Metadata.Name)
	}

	decodedPolicy, err := ResourceToTrafficPolicy(resource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(policy, decodedPolicy) {
		t.Errorf("expected policy %+v, got %+v", policy, decodedPolicy)
	}
}

func TestResourceToTrafficPolicy_InvalidKind(t *testing.T) {
	resource := &model.Resource{
		Kind: "Service",
	}

	_, err := ResourceToTrafficPolicy(resource)
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}

func TestServiceToResource_And_ResourceToService_WithSelectors(t *testing.T) {
	svc := &model.Service{
		ID: model.ServiceID{
			Namespace: "production",
			Name:      "order-service",
		},
		Protocol: model.ServiceProtocolHTTP,
		Port:     8080,
		Labels: map[string]string{
			"app": "orders",
		},
		Dependencies: []model.ServiceID{
			{Namespace: "production", Name: "payment-service"},
		},
		Selectors: []string{
			"windows:process-name:orders.exe",
			"windows:path:C:\\apps\\orders.exe",
		},
	}

	res := ServiceToResource(svc)
	if res.Kind != "Service" {
		t.Errorf("expected Kind 'Service', got %q", res.Kind)
	}
	if res.Metadata.Name != "order-service" || res.Metadata.Namespace != "production" {
		t.Errorf("unexpected metadata: %+v", res.Metadata)
	}

	decodedSvc, err := ResourceToService(res)
	if err != nil {
		t.Fatalf("ResourceToService failed: %v", err)
	}

	if decodedSvc.ID != svc.ID {
		t.Errorf("ServiceID mismatch: got %+v, want %+v", decodedSvc.ID, svc.ID)
	}
	if decodedSvc.Protocol != svc.Protocol {
		t.Errorf("Protocol mismatch: got %v, want %v", decodedSvc.Protocol, svc.Protocol)
	}
	if decodedSvc.Port != svc.Port {
		t.Errorf("Port mismatch: got %d, want %d", decodedSvc.Port, svc.Port)
	}
	if !reflect.DeepEqual(decodedSvc.Selectors, svc.Selectors) {
		t.Errorf("Selectors mismatch: got %v, want %v", decodedSvc.Selectors, svc.Selectors)
	}
}

func TestResourceToService_InvalidKind(t *testing.T) {
	resource := &model.Resource{
		Kind: "TrafficPolicy",
	}

	_, err := ResourceToService(resource)
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}

func TestNamespaceToResource_And_ResourceToNamespace_WithSubnets(t *testing.T) {
	ns := &model.Namespace{
		Name:      "test-ns",
		Isolation: model.NamespaceIsolationStrict,
		Labels: map[string]string{
			"env": "staging",
		},
		CIDR: netip.MustParsePrefix("100.64.10.0/24"),
		Subnets: []netip.Prefix{
			netip.MustParsePrefix("100.64.10.0/24"),
			netip.MustParsePrefix("100.64.20.0/24"),
		},
	}

	res := NamespaceToResource(ns)
	if res.Kind != "Namespace" {
		t.Errorf("expected Kind 'Namespace', got %q", res.Kind)
	}
	if res.Metadata.Name != "test-ns" {
		t.Errorf("expected name 'test-ns', got %q", res.Metadata.Name)
	}

	decoded, err := ResourceToNamespace(res)
	if err != nil {
		t.Fatalf("ResourceToNamespace failed: %v", err)
	}

	if decoded.Name != ns.Name {
		t.Errorf("Name mismatch: got %q, want %q", decoded.Name, ns.Name)
	}
	if decoded.Isolation != ns.Isolation {
		t.Errorf("Isolation mismatch: got %v, want %v", decoded.Isolation, ns.Isolation)
	}
	if decoded.CIDR != ns.CIDR {
		t.Errorf("CIDR mismatch: got %v, want %v", decoded.CIDR, ns.CIDR)
	}
	if len(decoded.Subnets) != len(ns.Subnets) {
		t.Fatalf("Subnets length mismatch: got %d, want %d", len(decoded.Subnets), len(ns.Subnets))
	}
	for i := range decoded.Subnets {
		if decoded.Subnets[i] != ns.Subnets[i] {
			t.Errorf("Subnets[%d] mismatch: got %v, want %v", i, decoded.Subnets[i], ns.Subnets[i])
		}
	}
}

