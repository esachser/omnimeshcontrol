package codec

import (
	"reflect"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestTrafficPolicySpec_RoundTrip(t *testing.T) {
	policy := &model.TrafficPolicy{
		Name:          "orders-policy",
		Namespace:     "prod",
		TargetService: model.ServiceID{Namespace: "prod", Name: "orders"},
		Retry: &model.RetryPolicy{
			MaxRetries:  3,
			RetryOn:     []string{"connection-failure", "5xx"},
			BackoffBase: 150 * time.Millisecond,
			BackoffMax:  2 * time.Second,
		},
		CircuitBreaker: &model.CircuitBreakerPolicy{
			FailureThreshold: 5,
			FailureWindow:    30 * time.Second,
			HalfOpenAfter:    10 * time.Second,
		},
		RateLimiter: &model.RateLimitPolicy{
			RequestsPerSecond:     120,
			BurstSize:             200,
			MaxConcurrentRequests: 50,
			LimitExceededAction:   model.RateLimitActionQueue,
		},
		LoadBalancer: &model.LoadBalancerPolicy{
			Algorithm: model.LoadBalancerRandom,
		},
		OutlierDetection: &model.OutlierDetectionPolicy{
			ErrorRateThreshold: 0.5,
			MinReportingAgents: 3,
			EjectionDuration:   45 * time.Second,
			MaxEjectionPercent: 0.25,
		},
		Routes: []model.TrafficRoute{
			{
				Destination: model.TrafficRouteDestination{
					Target: "aws-east-1",
					Labels: map[string]string{"version": "v1"},
				},
				Weight: 80,
			},
			{
				Destination: model.TrafficRouteDestination{
					Target: "aws-east-1",
					Labels: map[string]string{"version": "v2"},
				},
				Weight: 20,
			},
		},
	}

	spec, err := EncodeTrafficPolicySpec(policy)
	if err != nil {
		t.Fatalf("EncodeTrafficPolicySpec() error = %v", err)
	}

	decoded, err := DecodeTrafficPolicySpec(spec, policy.Name, policy.Namespace)
	if err != nil {
		t.Fatalf("DecodeTrafficPolicySpec() error = %v", err)
	}

	if !reflect.DeepEqual(policy, decoded) {
		t.Fatalf("round trip mismatch\noriginal: %+v\nparsed:   %+v", policy, decoded)
	}
}

func TestTrafficPolicySpec_InvalidAction(t *testing.T) {
	spec := []byte(`{"targetService":"default/api","rateLimiter":{"requestsPerSecond":1,"burstSize":1,"limitExceededAction":"drop"}}`)
	_, err := DecodeTrafficPolicySpec(spec, "api-policy", "default")
	if err == nil {
		t.Fatal("expected error for invalid rate limiter action")
	}
}
