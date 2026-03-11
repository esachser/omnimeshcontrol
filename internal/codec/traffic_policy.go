package codec

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

type trafficRouteDestinationSpec struct {
	Target string            `json:"target,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type trafficRouteSpec struct {
	Destination trafficRouteDestinationSpec `json:"destination,omitempty"`
	Weight      int                         `json:"weight,omitempty"`
}

type retrySpec struct {
	MaxRetries  int      `json:"maxRetries,omitempty"`
	RetryOn     []string `json:"retryOn,omitempty"`
	BackoffBase string   `json:"backoffBase,omitempty"`
	BackoffMax  string   `json:"backoffMax,omitempty"`
}

type circuitBreakerSpec struct {
	FailureThreshold int    `json:"failureThreshold,omitempty"`
	FailureWindow    string `json:"failureWindow,omitempty"`
	HalfOpenAfter    string `json:"halfOpenAfter,omitempty"`
}

type rateLimitSpec struct {
	RequestsPerSecond     int    `json:"requestsPerSecond,omitempty"`
	BurstSize             int    `json:"burstSize,omitempty"`
	MaxConcurrentRequests int    `json:"maxConcurrentRequests,omitempty"`
	LimitExceededAction   string `json:"limitExceededAction,omitempty"`
}

type loadBalancerSpec struct {
	Algorithm string `json:"algorithm,omitempty"`
}

type outlierDetectionSpec struct {
	ErrorRateThreshold float64 `json:"errorRateThreshold,omitempty"`
	MinReportingAgents int     `json:"minReportingAgents,omitempty"`
	EjectionDuration   string  `json:"ejectionDuration,omitempty"`
	MaxEjectionPercent float64 `json:"maxEjectionPercent,omitempty"`
}

type trafficPolicySpec struct {
	TargetService    string                `json:"targetService,omitempty"`
	Routes           []trafficRouteSpec    `json:"routes,omitempty"`
	Retry            *retrySpec            `json:"retry,omitempty"`
	CircuitBreaker   *circuitBreakerSpec   `json:"circuitBreaker,omitempty"`
	RateLimiter      *rateLimitSpec        `json:"rateLimiter,omitempty"`
	LoadBalancer     *loadBalancerSpec     `json:"loadBalancer,omitempty"`
	OutlierDetection *outlierDetectionSpec `json:"outlierDetection,omitempty"`
}

// TrafficPolicyToResource converts a TrafficPolicy into a Resource envelope.
func TrafficPolicyToResource(policy *model.TrafficPolicy) (*model.Resource, error) {
	specJSON, err := EncodeTrafficPolicySpec(policy)
	if err != nil {
		return nil, err
	}

	return &model.Resource{
		APIVersion: "traffic.omnimesh.io/v1",
		Kind:       "TrafficPolicy",
		Metadata: model.ResourceMetadata{
			Name:      policy.Name,
			Namespace: policy.Namespace,
		},
		Spec: specJSON,
	}, nil
}

// ResourceToTrafficPolicy converts a Resource envelope into a TrafficPolicy.
func ResourceToTrafficPolicy(resource *model.Resource) (*model.TrafficPolicy, error) {
	if resource.Kind != "TrafficPolicy" {
		return nil, fmt.Errorf("expected kind TrafficPolicy, got %s", resource.Kind)
	}

	return DecodeTrafficPolicySpec(resource.Spec, resource.Metadata.Name, resource.Metadata.Namespace)
}

// EncodeTrafficPolicySpec encodes a TrafficPolicy into JSON spec bytes.
func EncodeTrafficPolicySpec(policy *model.TrafficPolicy) ([]byte, error) {
	if policy == nil {
		return nil, fmt.Errorf("traffic policy is nil")
	}
	if policy.TargetService.Name == "" || policy.TargetService.Namespace == "" {
		return nil, fmt.Errorf("traffic policy targetService is required")
	}

	spec := trafficPolicySpec{
		TargetService: policy.TargetService.String(),
	}

	for _, route := range policy.Routes {
		spec.Routes = append(spec.Routes, trafficRouteSpec{
			Destination: trafficRouteDestinationSpec{
				Target: route.Destination.Target,
				Labels: route.Destination.Labels,
			},
			Weight: route.Weight,
		})
	}

	if policy.Retry != nil {
		spec.Retry = &retrySpec{
			MaxRetries:  policy.Retry.MaxRetries,
			RetryOn:     append([]string{}, policy.Retry.RetryOn...),
			BackoffBase: durationString(policy.Retry.BackoffBase),
			BackoffMax:  durationString(policy.Retry.BackoffMax),
		}
	}

	if policy.CircuitBreaker != nil {
		spec.CircuitBreaker = &circuitBreakerSpec{
			FailureThreshold: policy.CircuitBreaker.FailureThreshold,
			FailureWindow:    durationString(policy.CircuitBreaker.FailureWindow),
			HalfOpenAfter:    durationString(policy.CircuitBreaker.HalfOpenAfter),
		}
	}

	if policy.RateLimiter != nil {
		spec.RateLimiter = &rateLimitSpec{
			RequestsPerSecond:     policy.RateLimiter.RequestsPerSecond,
			BurstSize:             policy.RateLimiter.BurstSize,
			MaxConcurrentRequests: policy.RateLimiter.MaxConcurrentRequests,
			LimitExceededAction:   rateLimitActionToString(policy.RateLimiter.LimitExceededAction),
		}
	}

	if policy.LoadBalancer != nil {
		spec.LoadBalancer = &loadBalancerSpec{
			Algorithm: loadBalancerAlgorithmToString(policy.LoadBalancer.Algorithm),
		}
	}

	if policy.OutlierDetection != nil {
		spec.OutlierDetection = &outlierDetectionSpec{
			ErrorRateThreshold: policy.OutlierDetection.ErrorRateThreshold,
			MinReportingAgents: policy.OutlierDetection.MinReportingAgents,
			EjectionDuration:   durationString(policy.OutlierDetection.EjectionDuration),
			MaxEjectionPercent: policy.OutlierDetection.MaxEjectionPercent,
		}
	}

	return json.Marshal(spec)
}

// DecodeTrafficPolicySpec decodes JSON spec bytes into a TrafficPolicy.
func DecodeTrafficPolicySpec(specJSON []byte, name, namespace string) (*model.TrafficPolicy, error) {
	var spec trafficPolicySpec
	if len(specJSON) > 0 {
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal traffic policy spec: %w", err)
		}
	}

	if spec.TargetService == "" {
		return nil, fmt.Errorf("traffic policy spec missing targetService")
	}
	serviceID, err := model.ParseServiceID(spec.TargetService)
	if err != nil {
		return nil, fmt.Errorf("invalid targetService: %w", err)
	}

	policy := &model.TrafficPolicy{
		Name:          name,
		Namespace:     namespace,
		TargetService: serviceID,
	}

	if spec.Retry != nil {
		retry := &model.RetryPolicy{
			MaxRetries: spec.Retry.MaxRetries,
			RetryOn:    append([]string{}, spec.Retry.RetryOn...),
		}
		if spec.Retry.BackoffBase != "" {
			backoff, err := time.ParseDuration(spec.Retry.BackoffBase)
			if err != nil {
				return nil, fmt.Errorf("parse retry backoffBase: %w", err)
			}
			retry.BackoffBase = backoff
		}
		if spec.Retry.BackoffMax != "" {
			backoff, err := time.ParseDuration(spec.Retry.BackoffMax)
			if err != nil {
				return nil, fmt.Errorf("parse retry backoffMax: %w", err)
			}
			retry.BackoffMax = backoff
		}
		policy.Retry = retry
	}

	if spec.CircuitBreaker != nil {
		cb := &model.CircuitBreakerPolicy{
			FailureThreshold: spec.CircuitBreaker.FailureThreshold,
		}
		if spec.CircuitBreaker.FailureWindow != "" {
			window, err := time.ParseDuration(spec.CircuitBreaker.FailureWindow)
			if err != nil {
				return nil, fmt.Errorf("parse circuit breaker failureWindow: %w", err)
			}
			cb.FailureWindow = window
		}
		if spec.CircuitBreaker.HalfOpenAfter != "" {
			halfOpen, err := time.ParseDuration(spec.CircuitBreaker.HalfOpenAfter)
			if err != nil {
				return nil, fmt.Errorf("parse circuit breaker halfOpenAfter: %w", err)
			}
			cb.HalfOpenAfter = halfOpen
		}
		policy.CircuitBreaker = cb
	}

	if spec.RateLimiter != nil {
		action, err := parseRateLimitAction(spec.RateLimiter.LimitExceededAction)
		if err != nil {
			return nil, err
		}
		policy.RateLimiter = &model.RateLimitPolicy{
			RequestsPerSecond:     spec.RateLimiter.RequestsPerSecond,
			BurstSize:             spec.RateLimiter.BurstSize,
			MaxConcurrentRequests: spec.RateLimiter.MaxConcurrentRequests,
			LimitExceededAction:   action,
		}
	}

	if spec.LoadBalancer != nil {
		algorithm, err := parseLoadBalancerAlgorithm(spec.LoadBalancer.Algorithm)
		if err != nil {
			return nil, err
		}
		policy.LoadBalancer = &model.LoadBalancerPolicy{Algorithm: algorithm}
	}

	if spec.OutlierDetection != nil {
		od := &model.OutlierDetectionPolicy{
			ErrorRateThreshold: spec.OutlierDetection.ErrorRateThreshold,
			MinReportingAgents: spec.OutlierDetection.MinReportingAgents,
			MaxEjectionPercent: spec.OutlierDetection.MaxEjectionPercent,
		}
		if spec.OutlierDetection.EjectionDuration != "" {
			duration, err := time.ParseDuration(spec.OutlierDetection.EjectionDuration)
			if err != nil {
				return nil, fmt.Errorf("parse outlier detection ejectionDuration: %w", err)
			}
			od.EjectionDuration = duration
		}
		policy.OutlierDetection = od
	}

	for _, route := range spec.Routes {
		policy.Routes = append(policy.Routes, model.TrafficRoute{
			Destination: model.TrafficRouteDestination{
				Target: route.Destination.Target,
				Labels: route.Destination.Labels,
			},
			Weight: route.Weight,
		})
	}

	return policy, nil
}

func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

func rateLimitActionToString(action model.RateLimitAction) string {
	switch action {
	case model.RateLimitActionQueue:
		return "queue"
	default:
		return "reject"
	}
}

func parseRateLimitAction(raw string) (model.RateLimitAction, error) {
	switch strings.ToLower(raw) {
	case "", "reject":
		return model.RateLimitActionReject, nil
	case "queue":
		return model.RateLimitActionQueue, nil
	default:
		return model.RateLimitActionReject, fmt.Errorf("unknown rate limit action %q", raw)
	}
}

func loadBalancerAlgorithmToString(algorithm model.LoadBalancerAlgorithm) string {
	return algorithm.String()
}

func parseLoadBalancerAlgorithm(raw string) (model.LoadBalancerAlgorithm, error) {
	switch strings.ToLower(raw) {
	case "", "weighted-round-robin":
		return model.LoadBalancerWeightedRoundRobin, nil
	case "least-connections":
		return model.LoadBalancerLeastConnections, nil
	case "random":
		return model.LoadBalancerRandom, nil
	default:
		return model.LoadBalancerWeightedRoundRobin, fmt.Errorf("unknown load balancer algorithm %q", raw)
	}
}
