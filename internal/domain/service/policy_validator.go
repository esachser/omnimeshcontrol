package service

import (
	"fmt"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// PolicyValidator validates that a traffic policy is compatible with
// the target service's protocol. L7-only features (HTTP retries on specific
// status codes, header-based routing) are rejected for TCP services.
type PolicyValidator struct{}

// NewPolicyValidator creates a new PolicyValidator.
func NewPolicyValidator() *PolicyValidator {
	return &PolicyValidator{}
}

// Validate checks a traffic policy for internal consistency and protocol compatibility.
func (v *PolicyValidator) Validate(policy *model.TrafficPolicy, protocol model.ServiceProtocol) error {
	if err := v.validateRetry(policy.Retry, protocol); err != nil {
		return err
	}
	if err := v.validateCircuitBreaker(policy.CircuitBreaker); err != nil {
		return err
	}
	if err := v.validateRateLimit(policy.RateLimiter); err != nil {
		return err
	}
	if err := v.validateOutlierDetection(policy.OutlierDetection); err != nil {
		return err
	}
	return nil
}

func (v *PolicyValidator) validateRetry(retry *model.RetryPolicy, protocol model.ServiceProtocol) error {
	if retry == nil {
		return nil
	}
	if retry.MaxRetries < 0 {
		return fmt.Errorf("%w: MaxRetries must be >= 0", domain.ErrInvalidRetryConfig)
	}
	if retry.BackoffBase < 0 {
		return fmt.Errorf("%w: BackoffBase must be >= 0", domain.ErrInvalidRetryConfig)
	}
	if retry.BackoffMax < 0 {
		return fmt.Errorf("%w: BackoffMax must be >= 0", domain.ErrInvalidRetryConfig)
	}
	if retry.BackoffMax > 0 && retry.BackoffBase > retry.BackoffMax {
		return fmt.Errorf("%w: BackoffBase must be <= BackoffMax", domain.ErrInvalidRetryConfig)
	}

	// Check for L7-specific retry-on conditions on TCP services.
	if !protocol.IsL7() && len(retry.RetryOn) > 0 {
		for _, condition := range retry.RetryOn {
			if condition != "connection-failure" {
				return fmt.Errorf(
					"%w: retry condition %q requires L7 protocol, but service uses %s",
					domain.ErrProtocolMismatch, condition, protocol,
				)
			}
		}
	}
	return nil
}

func (v *PolicyValidator) validateCircuitBreaker(cb *model.CircuitBreakerPolicy) error {
	if cb == nil {
		return nil
	}
	if cb.FailureThreshold <= 0 {
		return fmt.Errorf("%w: FailureThreshold must be > 0", domain.ErrInvalidCBConfig)
	}
	if cb.FailureWindow <= 0 {
		return fmt.Errorf("%w: FailureWindow must be > 0", domain.ErrInvalidCBConfig)
	}
	if cb.HalfOpenAfter <= 0 {
		return fmt.Errorf("%w: HalfOpenAfter must be > 0", domain.ErrInvalidCBConfig)
	}
	return nil
}

func (v *PolicyValidator) validateRateLimit(rl *model.RateLimitPolicy) error {
	if rl == nil {
		return nil
	}
	if rl.RequestsPerSecond <= 0 {
		return fmt.Errorf("%w: RequestsPerSecond must be > 0", domain.ErrInvalidRateLimitConfig)
	}
	if rl.BurstSize <= 0 {
		return fmt.Errorf("%w: BurstSize must be > 0", domain.ErrInvalidRateLimitConfig)
	}
	if rl.BurstSize < rl.RequestsPerSecond {
		return fmt.Errorf("%w: BurstSize must be >= RequestsPerSecond", domain.ErrInvalidRateLimitConfig)
	}
	return nil
}

func (v *PolicyValidator) validateOutlierDetection(od *model.OutlierDetectionPolicy) error {
	if od == nil {
		return nil
	}
	if od.ErrorRateThreshold <= 0 || od.ErrorRateThreshold > 1.0 {
		return fmt.Errorf("%w: ErrorRateThreshold must be between 0 and 1", domain.ErrInvalidCBConfig)
	}
	if od.MinReportingAgents <= 0 {
		return fmt.Errorf("%w: MinReportingAgents must be > 0", domain.ErrInvalidCBConfig)
	}
	if od.EjectionDuration <= 0 {
		return fmt.Errorf("%w: EjectionDuration must be > 0", domain.ErrInvalidCBConfig)
	}
	if od.MaxEjectionPercent <= 0 || od.MaxEjectionPercent > 1.0 {
		return fmt.Errorf("%w: MaxEjectionPercent must be between 0 and 1", domain.ErrInvalidCBConfig)
	}
	return nil
}
