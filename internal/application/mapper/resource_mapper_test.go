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

func TestServiceToResource_And_ResourceToService_WithSelectorSets(t *testing.T) {
	svc := &model.Service{
		ID: model.ServiceID{
			Namespace: "production",
			Name:      "payment-service",
		},
		Protocol: model.ServiceProtocolGRPC,
		Port:     50051,
		SelectorSets: [][]string{
			{"windows:user_sid:S-1-5-21-1001", "windows:sha256:deadbeef"},
			{"unix:user:payment", "unix:pid:4321"},
		},
	}

	res := ServiceToResource(svc)
	decoded, err := ResourceToService(res)
	if err != nil {
		t.Fatalf("ResourceToService failed: %v", err)
	}

	if !reflect.DeepEqual(decoded.SelectorSets, svc.SelectorSets) {
		t.Errorf("SelectorSets mismatch: got %v, want %v", decoded.SelectorSets, svc.SelectorSets)
	}
}

func TestResourceToService_SelectorSetFormats(t *testing.T) {
	tests := []struct {
		name     string
		specJSON string
		wantSets [][]string
		wantFlat []string
		wantErr  bool
	}{
		{
			name:     "selectorSets array of string arrays",
			specJSON: `{"protocol":"HTTP","port":8080,"selectorSets":[["windows:user_sid:S-1","windows:sha256:abc"],["unix:user:app","unix:pid:99"]]}`,
			wantSets: [][]string{
				{"windows:user_sid:S-1", "windows:sha256:abc"},
				{"unix:user:app", "unix:pid:99"},
			},
		},
		{
			name:     "selectorSets array of objects",
			specJSON: `{"protocol":"HTTP","port":8080,"selectorSets":[{"selectors":["windows:user_sid:S-1","windows:sha256:abc"]},{"selectors":["unix:user:app","unix:pid:99"]}]}`,
			wantSets: [][]string{
				{"windows:user_sid:S-1", "windows:sha256:abc"},
				{"unix:user:app", "unix:pid:99"},
			},
		},
		{
			name:     "nested arrays under selectors",
			specJSON: `{"protocol":"HTTP","port":8080,"selectors":[["windows:user_sid:S-1","windows:sha256:abc"],["unix:user:app","unix:pid:99"]]}`,
			wantSets: [][]string{
				{"windows:user_sid:S-1", "windows:sha256:abc"},
				{"unix:user:app", "unix:pid:99"},
			},
		},
		{
			name:     "legacy flat selectors",
			specJSON: `{"protocol":"HTTP","port":8080,"selectors":["unix:user:app","unix:pid:99"]}`,
			wantFlat: []string{"unix:user:app", "unix:pid:99"},
		},
		{
			name:     "invalid selectorSets element",
			specJSON: `{"protocol":"HTTP","port":8080,"selectorSets":["not-an-array"]}`,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &model.Resource{
				Kind: "Service",
				Metadata: model.ResourceMetadata{
					Name:      "orders",
					Namespace: "production",
				},
				Spec: []byte(tc.specJSON),
			}
			svc, err := ResourceToService(res)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResourceToService() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(svc.SelectorSets, tc.wantSets) {
				t.Errorf("SelectorSets = %v, want %v", svc.SelectorSets, tc.wantSets)
			}
			if !reflect.DeepEqual(svc.Selectors, tc.wantFlat) {
				t.Errorf("Selectors = %v, want %v", svc.Selectors, tc.wantFlat)
			}
		})
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

func TestResourceToService_ProtocolNormalization(t *testing.T) {
	tests := []struct {
		name         string
		rawProtocol  string
		wantProtocol model.ServiceProtocol
	}{
		{name: "empty defaults to auto", rawProtocol: "", wantProtocol: model.ServiceProtocolAuto},
		{name: "auto", rawProtocol: "auto", wantProtocol: model.ServiceProtocolAuto},
		{name: "tcp", rawProtocol: "TCP", wantProtocol: model.ServiceProtocolTCP},
		{name: "http lowercase", rawProtocol: "http", wantProtocol: model.ServiceProtocolHTTP},
		{name: "http1.1 with spaces", rawProtocol: " HTTP/1.1 ", wantProtocol: model.ServiceProtocolHTTP1},
		{name: "http/1.0", rawProtocol: "http/1.0", wantProtocol: model.ServiceProtocolHTTP1},
		{name: "http1.1", rawProtocol: "http1.1", wantProtocol: model.ServiceProtocolHTTP1},
		{name: "http2", rawProtocol: "http2", wantProtocol: model.ServiceProtocolHTTP2},
		{name: "http/2", rawProtocol: "http/2", wantProtocol: model.ServiceProtocolHTTP2},
		{name: "h2c", rawProtocol: "h2c", wantProtocol: model.ServiceProtocolHTTP2},
		{name: "grpc-web", rawProtocol: "grpc-web", wantProtocol: model.ServiceProtocolHTTP2},
		{name: "grpc mixed case", rawProtocol: "gRPC", wantProtocol: model.ServiceProtocolGRPC},
		{name: "grpc lowercase", rawProtocol: "grpc", wantProtocol: model.ServiceProtocolGRPC},
		{name: "unknown falls back to auto", rawProtocol: "mysql", wantProtocol: model.ServiceProtocolAuto},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &model.Resource{
				Kind: "Service",
				Metadata: model.ResourceMetadata{
					Name:      "svc",
					Namespace: "production",
				},
				Spec: []byte(`{"protocol":"` + tc.rawProtocol + `","port":8080}`),
			}
			svc, err := ResourceToService(res)
			if err != nil {
				t.Fatalf("ResourceToService() error = %v", err)
			}
			if svc.Protocol != tc.wantProtocol {
				t.Errorf("Protocol = %v, want %v", svc.Protocol, tc.wantProtocol)
			}

			roundTrip, err := ResourceToService(ServiceToResource(svc))
			if err != nil {
				t.Fatalf("round-trip ResourceToService() error = %v", err)
			}
			if roundTrip.Protocol != tc.wantProtocol {
				t.Errorf("round-trip Protocol = %v, want %v", roundTrip.Protocol, tc.wantProtocol)
			}
		})
	}
}
