package model

import "time"

// TrafficPolicy is the top-level domain object that binds retry, circuit breaking,
// rate limiting, load balancing, and outlier detection configuration to a target service.
type TrafficPolicy struct {
	// Name is the unique name of this traffic policy.
	Name string
	// Namespace is the namespace of this traffic policy.
	Namespace string
	// TargetService identifies which service this policy applies to.
	TargetService ServiceID
	// Retry configures retry behavior. nil means no retries.
	Retry *RetryPolicy
	// CircuitBreaker configures circuit breaker behavior. nil means no circuit breaking.
	CircuitBreaker *CircuitBreakerPolicy
	// RateLimiter configures rate limiting behavior. nil means no rate limiting.
	RateLimiter *RateLimitPolicy
	// LoadBalancer configures load balancing behavior. nil means weighted-round-robin default.
	LoadBalancer *LoadBalancerPolicy
	// OutlierDetection configures global outlier detection. nil means no global outlier detection.
	OutlierDetection *OutlierDetectionPolicy
	// Routes defines how traffic is distributed across targets and label subsets.
	Routes []TrafficRoute
}

// TrafficPolicyID returns the ServiceID-style identifier for this policy.
func (p TrafficPolicy) TrafficPolicyID() ServiceID {
	return ServiceID{Namespace: p.Namespace, Name: p.Name}
}

// RetryPolicy defines how failed requests should be retried.
type RetryPolicy struct {
	// MaxRetries is the maximum number of retry attempts (0 = no retries).
	MaxRetries int
	// RetryOn lists the conditions that trigger a retry (e.g., "connection-failure", "5xx").
	RetryOn []string
	// BackoffBase is the initial backoff duration between retries.
	BackoffBase time.Duration
	// BackoffMax is the maximum backoff duration.
	BackoffMax time.Duration
}

// CircuitBreakerPolicy defines circuit breaker behavior for an endpoint.
type CircuitBreakerPolicy struct {
	// FailureThreshold is the number of consecutive failures before the circuit opens.
	FailureThreshold int
	// FailureWindow is the rolling window in which failures are counted.
	FailureWindow time.Duration
	// HalfOpenAfter is the time after which a tripped circuit transitions to half-open,
	// allowing a probe request.
	HalfOpenAfter time.Duration
}

// RateLimitPolicy defines local (per-agent) rate limiting for a service.
type RateLimitPolicy struct {
	// RequestsPerSecond is the sustained rate limit (token bucket refill rate).
	RequestsPerSecond int
	// BurstSize is the maximum number of tokens that can accumulate (burst tolerance).
	BurstSize int
	// MaxConcurrentRequests is the maximum number of in-flight requests to a service.
	MaxConcurrentRequests int
	// LimitExceededAction is the action to take when the rate limit is exceeded.
	LimitExceededAction RateLimitAction
}

// RateLimitAction determines the behavior when a rate limit is exceeded.
type RateLimitAction int

const (
	// RateLimitActionReject immediately rejects the request.
	RateLimitActionReject RateLimitAction = iota
	// RateLimitActionQueue queues the request for later processing.
	RateLimitActionQueue
)

// LoadBalancerPolicy defines the load balancing algorithm.
type LoadBalancerPolicy struct {
	// Algorithm is the load balancing algorithm to use.
	Algorithm LoadBalancerAlgorithm
}

// LoadBalancerAlgorithm is the load balancing algorithm type.
type LoadBalancerAlgorithm int

const (
	// LoadBalancerWeightedRoundRobin distributes traffic based on endpoint weights.
	LoadBalancerWeightedRoundRobin LoadBalancerAlgorithm = iota
	// LoadBalancerLeastConnections sends traffic to the endpoint with the fewest active connections.
	LoadBalancerLeastConnections
	// LoadBalancerRandom distributes traffic randomly.
	LoadBalancerRandom
)

// String returns the string representation of a LoadBalancerAlgorithm.
func (a LoadBalancerAlgorithm) String() string {
	switch a {
	case LoadBalancerLeastConnections:
		return "least-connections"
	case LoadBalancerRandom:
		return "random"
	default:
		return "weighted-round-robin"
	}
}

// OutlierDetectionPolicy configures global outlier detection at the control plane level.
// The control plane aggregates health signals from all agents and ejects degraded
// endpoints from routing tables cluster-wide.
type OutlierDetectionPolicy struct {
	// ErrorRateThreshold is the error rate above which an endpoint is ejected (e.g., 0.5 = 50%).
	ErrorRateThreshold float64
	// MinReportingAgents is the minimum number of agents that must report data
	// before an ejection decision is made.
	MinReportingAgents int
	// EjectionDuration is how long an endpoint stays ejected before being re-evaluated.
	EjectionDuration time.Duration
	// MaxEjectionPercent is the maximum percentage of endpoints that can be ejected
	// simultaneously (safety valve — prevents ejecting all endpoints).
	MaxEjectionPercent float64
}

// TrafficRouteDestination explicitly defines the target topology and workload
// metadata subset being routed to.
type TrafficRouteDestination struct {
	// Target is the NetworkTarget identifier. If empty, matches any target.
	Target string
	// Labels are the metadata tags for this node, used for subset routing.
	Labels map[string]string
}

// TrafficRoute defines a weighted distribution of traffic to a specific destination.
type TrafficRoute struct {
	// Destination matches endpoints based on Target and Labels.
	Destination TrafficRouteDestination
	// Weight is the relative proportion of traffic this route should receive.
	Weight int
}
