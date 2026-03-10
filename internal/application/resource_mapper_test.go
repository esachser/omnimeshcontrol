package application

import (
	"reflect"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestTrafficPolicyToResource(t *testing.T) {
	policy := &model.TrafficPolicy{
		Name:          "payment-policy",
		Namespace:     "default",
		TargetService: model.ServiceID{Namespace: "default", Name: "payment-service"},
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

	resource := trafficPolicyToResource(policy)

	if resource.Kind != "TrafficPolicy" {
		t.Errorf("expected kind TrafficPolicy, got %q", resource.Kind)
	}
	if resource.Metadata.Name != "payment-policy" {
		t.Errorf("expected name payment-policy, got %q", resource.Metadata.Name)
	}

	decodedPolicy, err := resourceToTrafficPolicy(resource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(policy, decodedPolicy) {
		t.Errorf("expected policy %+v, got %+v", policy, decodedPolicy)
	}
}

func TestResourceToTrafficPolicy_InvalidProtocol(t *testing.T) {
	resource := &model.Resource{
		Kind: "Service",
	}

	_, err := resourceToTrafficPolicy(resource)
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}
