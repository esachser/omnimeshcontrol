package service

import (
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestPolicyValidator_Validate(t *testing.T) {
	v := NewPolicyValidator()

	tests := []struct {
		name     string
		policy   *model.TrafficPolicy
		protocol model.ServiceProtocol
		wantErr  bool
	}{
		{
			name:     "nil policy fields are valid",
			policy:   &model.TrafficPolicy{},
			protocol: model.ServiceProtocolTCP,
			wantErr:  false,
		},
		{
			name: "valid retry for HTTP",
			policy: &model.TrafficPolicy{
				Retry: &model.RetryPolicy{
					MaxRetries:  3,
					RetryOn:     []string{"5xx", "connection-failure"},
					BackoffBase: 100 * time.Millisecond,
					BackoffMax:  5 * time.Second,
				},
			},
			protocol: model.ServiceProtocolHTTP,
			wantErr:  false,
		},
		{
			name: "connection-failure retry allowed on TCP",
			policy: &model.TrafficPolicy{
				Retry: &model.RetryPolicy{
					MaxRetries: 2,
					RetryOn:    []string{"connection-failure"},
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  false,
		},
		{
			name: "5xx retry rejected on TCP",
			policy: &model.TrafficPolicy{
				Retry: &model.RetryPolicy{
					MaxRetries: 2,
					RetryOn:    []string{"5xx"},
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  true,
		},
		{
			name: "negative MaxRetries rejected",
			policy: &model.TrafficPolicy{
				Retry: &model.RetryPolicy{
					MaxRetries: -1,
				},
			},
			protocol: model.ServiceProtocolHTTP,
			wantErr:  true,
		},
		{
			name: "BackoffBase > BackoffMax rejected",
			policy: &model.TrafficPolicy{
				Retry: &model.RetryPolicy{
					MaxRetries:  1,
					BackoffBase: 10 * time.Second,
					BackoffMax:  1 * time.Second,
				},
			},
			protocol: model.ServiceProtocolHTTP,
			wantErr:  true,
		},
		{
			name: "valid circuit breaker",
			policy: &model.TrafficPolicy{
				CircuitBreaker: &model.CircuitBreakerPolicy{
					FailureThreshold: 5,
					FailureWindow:    30 * time.Second,
					HalfOpenAfter:    15 * time.Second,
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  false,
		},
		{
			name: "zero FailureThreshold rejected",
			policy: &model.TrafficPolicy{
				CircuitBreaker: &model.CircuitBreakerPolicy{
					FailureThreshold: 0,
					FailureWindow:    30 * time.Second,
					HalfOpenAfter:    15 * time.Second,
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  true,
		},
		{
			name: "valid rate limit",
			policy: &model.TrafficPolicy{
				RateLimiter: &model.RateLimitPolicy{
					RequestsPerSecond: 100,
					BurstSize:         150,
				},
			},
			protocol: model.ServiceProtocolHTTP,
			wantErr:  false,
		},
		{
			name: "BurstSize < RequestsPerSecond rejected",
			policy: &model.TrafficPolicy{
				RateLimiter: &model.RateLimitPolicy{
					RequestsPerSecond: 100,
					BurstSize:         50,
				},
			},
			protocol: model.ServiceProtocolHTTP,
			wantErr:  true,
		},
		{
			name: "valid outlier detection",
			policy: &model.TrafficPolicy{
				OutlierDetection: &model.OutlierDetectionPolicy{
					ErrorRateThreshold: 0.5,
					MinReportingAgents: 3,
					EjectionDuration:   30 * time.Second,
					MaxEjectionPercent: 0.5,
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  false,
		},
		{
			name: "ErrorRateThreshold > 1 rejected",
			policy: &model.TrafficPolicy{
				OutlierDetection: &model.OutlierDetectionPolicy{
					ErrorRateThreshold: 1.5,
					MinReportingAgents: 3,
					EjectionDuration:   30 * time.Second,
					MaxEjectionPercent: 0.5,
				},
			},
			protocol: model.ServiceProtocolTCP,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.policy, tt.protocol)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
