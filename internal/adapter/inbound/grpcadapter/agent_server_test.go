package grpcadapter

import (
	"testing"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestScopeToProto_IncludesTrafficPolicies(t *testing.T) {
	policy := model.TrafficPolicy{
		Name:          "api-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "api"},
		Retry: &model.RetryPolicy{
			MaxRetries:  2,
			RetryOn:     []string{"5xx"},
			BackoffBase: 100 * time.Millisecond,
			BackoffMax:  1 * time.Second,
		},
	}

	scope := &model.AgentScope{TrafficPolicies: []model.TrafficPolicy{policy}}
	snapshot := scopeToProto(scope)

	if len(snapshot.TrafficPolicies) != 1 {
		t.Fatalf("expected 1 traffic policy, got %d", len(snapshot.TrafficPolicies))
	}

	info := snapshot.TrafficPolicies[0]

	if info.Name != policy.Name {
		t.Errorf("Name = %q, want %q", info.Name, policy.Name)
	}
	if info.Namespace != policy.Namespace {
		t.Errorf("Namespace = %q, want %q", info.Namespace, policy.Namespace)
	}
	if info.TargetServiceNamespace != policy.TargetService.Namespace {
		t.Errorf("TargetServiceNamespace = %q, want %q", info.TargetServiceNamespace, policy.TargetService.Namespace)
	}
	if info.TargetServiceName != policy.TargetService.Name {
		t.Errorf("TargetServiceName = %q, want %q", info.TargetServiceName, policy.TargetService.Name)
	}

	// Retry sub-message
	if info.Retry == nil {
		t.Fatal("Retry is nil, want non-nil")
	}
	if info.Retry.MaxRetries != int32(policy.Retry.MaxRetries) {
		t.Errorf("Retry.MaxRetries = %d, want %d", info.Retry.MaxRetries, policy.Retry.MaxRetries)
	}
	if len(info.Retry.RetryOn) != 1 || info.Retry.RetryOn[0] != "5xx" {
		t.Errorf("Retry.RetryOn = %v, want [5xx]", info.Retry.RetryOn)
	}
	if info.Retry.BackoffBaseMs != policy.Retry.BackoffBase.Milliseconds() {
		t.Errorf("Retry.BackoffBaseMs = %d, want %d", info.Retry.BackoffBaseMs, policy.Retry.BackoffBase.Milliseconds())
	}
	if info.Retry.BackoffMaxMs != policy.Retry.BackoffMax.Milliseconds() {
		t.Errorf("Retry.BackoffMaxMs = %d, want %d", info.Retry.BackoffMaxMs, policy.Retry.BackoffMax.Milliseconds())
	}

	// Optional sub-messages not set on the policy should be nil.
	if info.CircuitBreaker != nil {
		t.Errorf("CircuitBreaker should be nil, got %+v", info.CircuitBreaker)
	}
	if info.RateLimiter != nil {
		t.Errorf("RateLimiter should be nil, got %+v", info.RateLimiter)
	}
	if info.LoadBalancer != nil {
		t.Errorf("LoadBalancer should be nil, got %+v", info.LoadBalancer)
	}
	if info.OutlierDetection != nil {
		t.Errorf("OutlierDetection should be nil, got %+v", info.OutlierDetection)
	}
}

func TestScopeUpdateToProto_IncludesTrafficPolicies(t *testing.T) {
	policy := model.TrafficPolicy{
		Name:          "api-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "api"},
		CircuitBreaker: &model.CircuitBreakerPolicy{
			FailureThreshold: 5,
			FailureWindow:    30 * time.Second,
			HalfOpenAfter:    10 * time.Second,
		},
	}

	update := model.ScopeUpdate{
		Type:            model.ScopeUpdateModify,
		TrafficPolicies: []model.TrafficPolicy{policy},
	}

	protoUpdate := scopeUpdateToProto(update)
	if len(protoUpdate.TrafficPolicies) != 1 {
		t.Fatalf("expected 1 traffic policy, got %d", len(protoUpdate.TrafficPolicies))
	}

	cb := protoUpdate.TrafficPolicies[0].CircuitBreaker
	if cb == nil {
		t.Fatal("CircuitBreaker is nil, want non-nil")
	}
	if cb.FailureThreshold != int32(policy.CircuitBreaker.FailureThreshold) {
		t.Errorf("FailureThreshold = %d, want %d", cb.FailureThreshold, policy.CircuitBreaker.FailureThreshold)
	}
	if cb.FailureWindowMs != policy.CircuitBreaker.FailureWindow.Milliseconds() {
		t.Errorf("FailureWindowMs = %d, want %d", cb.FailureWindowMs, policy.CircuitBreaker.FailureWindow.Milliseconds())
	}
	if cb.HalfOpenAfterMs != policy.CircuitBreaker.HalfOpenAfter.Milliseconds() {
		t.Errorf("HalfOpenAfterMs = %d, want %d", cb.HalfOpenAfterMs, policy.CircuitBreaker.HalfOpenAfter.Milliseconds())
	}
}

func TestTrafficRoutesToProto(t *testing.T) {
	routes := []model.TrafficRoute{
		{
			Destination: model.TrafficRouteDestination{
				Target: "us-east",
				Labels: map[string]string{"env": "prod"},
			},
			Weight: 80,
		},
		{
			Destination: model.TrafficRouteDestination{
				Target: "us-west",
				Labels: map[string]string{"env": "canary"},
			},
			Weight: 20,
		},
	}

	out := trafficRoutesToProto(routes)
	if len(out) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(out))
	}

	assertRoute := func(idx int, r *omnimeshv1.TrafficRouteInfo, src model.TrafficRoute) {
		t.Helper()
		if r.Weight != int32(src.Weight) {
			t.Errorf("route[%d].Weight = %d, want %d", idx, r.Weight, src.Weight)
		}
		if r.Destination == nil {
			t.Fatalf("route[%d].Destination is nil", idx)
		}
		if r.Destination.Target != src.Destination.Target {
			t.Errorf("route[%d].Destination.Target = %q, want %q", idx, r.Destination.Target, src.Destination.Target)
		}
		for k, v := range src.Destination.Labels {
			if r.Destination.Labels[k] != v {
				t.Errorf("route[%d].Labels[%q] = %q, want %q", idx, k, r.Destination.Labels[k], v)
			}
		}
	}

	assertRoute(0, out[0], routes[0])
	assertRoute(1, out[1], routes[1])
}

func TestRateLimitPolicyToProto(t *testing.T) {
	p := &model.RateLimitPolicy{
		RequestsPerSecond:     500,
		BurstSize:             50,
		MaxConcurrentRequests: 100,
		LimitExceededAction:   model.RateLimitActionQueue,
	}

	info := rateLimitPolicyToProto(p)
	if info == nil {
		t.Fatal("expected non-nil proto, got nil")
	}
	if info.RequestsPerSecond != 500 {
		t.Errorf("RequestsPerSecond = %d, want 500", info.RequestsPerSecond)
	}
	if info.LimitExceededAction != omnimeshv1.RateLimitAction_RATE_LIMIT_ACTION_QUEUE {
		t.Errorf("LimitExceededAction = %v, want QUEUE", info.LimitExceededAction)
	}

	// nil input → nil output
	if rateLimitPolicyToProto(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestLoadBalancerPolicyToProto(t *testing.T) {
	cases := []struct {
		algo     model.LoadBalancerAlgorithm
		expected omnimeshv1.LoadBalancerAlgorithm
	}{
		{model.LoadBalancerWeightedRoundRobin, omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_WEIGHTED_ROUND_ROBIN},
		{model.LoadBalancerLeastConnections, omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_LEAST_CONNECTIONS},
		{model.LoadBalancerRandom, omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_RANDOM},
	}

	for _, tc := range cases {
		info := loadBalancerPolicyToProto(&model.LoadBalancerPolicy{Algorithm: tc.algo})
		if info == nil {
			t.Fatalf("expected non-nil proto for algo %v", tc.algo)
		}
		if info.Algorithm != tc.expected {
			t.Errorf("Algorithm = %v, want %v", info.Algorithm, tc.expected)
		}
	}

	// nil input → nil output
	if loadBalancerPolicyToProto(nil) != nil {
		t.Error("expected nil for nil input")
	}
}
