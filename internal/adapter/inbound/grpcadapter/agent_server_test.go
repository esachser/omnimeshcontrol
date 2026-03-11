package grpcadapter

import (
	"reflect"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestScopeToProto_IncludesTrafficPolicies(t *testing.T) {
	policy := model.TrafficPolicy{
		Name:          "api-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "api"},
		Retry: &model.RetryPolicy{
			MaxRetries: 2,
			RetryOn:    []string{"5xx"},
		},
	}

	scope := &model.AgentScope{TrafficPolicies: []model.TrafficPolicy{policy}}
	snapshot := scopeToProto(scope)

	if len(snapshot.TrafficPolicies) != 1 {
		t.Fatalf("expected 1 traffic policy, got %d", len(snapshot.TrafficPolicies))
	}

	info := snapshot.TrafficPolicies[0]
	if info.Name != policy.Name {
		t.Fatalf("policy name = %q, want %q", info.Name, policy.Name)
	}
	if info.Namespace != policy.Namespace {
		t.Fatalf("policy namespace = %q, want %q", info.Namespace, policy.Namespace)
	}
	if info.TargetServiceName != policy.TargetService.Name {
		t.Fatalf("target service name = %q, want %q", info.TargetServiceName, policy.TargetService.Name)
	}

	decoded, err := codec.DecodeTrafficPolicySpec(info.Spec, info.Name, info.Namespace)
	if err != nil {
		t.Fatalf("DecodeTrafficPolicySpec() error = %v", err)
	}

	if !reflect.DeepEqual(decoded, &policy) {
		t.Fatalf("decoded policy mismatch\nparsed:   %+v\nexpected: %+v", decoded, policy)
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
}
