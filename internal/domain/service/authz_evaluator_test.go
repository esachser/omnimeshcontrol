package service

import (
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestAuthzEvaluator_Evaluate(t *testing.T) {
	e := NewAuthzEvaluator()

	orderService := model.ServiceID{Namespace: "production", Name: "order-service"}
	paymentService := model.ServiceID{Namespace: "production", Name: "payment-service"}
	monitoringService := model.ServiceID{Namespace: "monitoring", Name: "prometheus"}
	unknownService := model.ServiceID{Namespace: "production", Name: "unknown"}

	tests := []struct {
		name        string
		source      model.ServiceID
		destination model.ServiceID
		policies    []model.AuthorizationPolicy
		want        EvaluateResult
	}{
		{
			name:        "no policies → default deny",
			source:      orderService,
			destination: paymentService,
			policies:    nil,
			want:        EvaluateResultDeny,
		},
		{
			name:        "explicit ALLOW matches → allow",
			source:      orderService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "allow-order-to-payment",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
					Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
				},
			},
			want: EvaluateResultAllow,
		},
		{
			name:        "explicit DENY overrides ALLOW",
			source:      orderService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "deny-all",
					Namespace:   "production",
					Action:      model.PolicyActionDeny,
					Source:      model.PolicySelector{Namespace: "production", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
				},
				{
					Name:        "allow-order-to-payment",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
					Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
				},
			},
			want: EvaluateResultDeny,
		},
		{
			name:        "wildcard source ALLOW matches any service in namespace",
			source:      unknownService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "allow-prod-to-payment",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
				},
			},
			want: EvaluateResultAllow,
		},
		{
			name:        "cross-namespace deny",
			source:      monitoringService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "allow-prod-to-prod",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "*"},
				},
			},
			want: EvaluateResultDeny, // monitoringService is not in "production" namespace
		},
		{
			name:        "wildcard both source and destination",
			source:      orderService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "allow-all-prod",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "*"},
				},
			},
			want: EvaluateResultAllow,
		},
		{
			name:        "DENY does not match → falls through to ALLOW",
			source:      orderService,
			destination: paymentService,
			policies: []model.AuthorizationPolicy{
				{
					Name:        "deny-monitoring",
					Namespace:   "production",
					Action:      model.PolicyActionDeny,
					Source:      model.PolicySelector{Namespace: "monitoring", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "*"},
				},
				{
					Name:        "allow-prod",
					Namespace:   "production",
					Action:      model.PolicyActionAllow,
					Source:      model.PolicySelector{Namespace: "production", Name: "*"},
					Destination: model.PolicySelector{Namespace: "production", Name: "*"},
				},
			},
			want: EvaluateResultAllow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.Evaluate(tt.source, tt.destination, tt.policies)
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}
