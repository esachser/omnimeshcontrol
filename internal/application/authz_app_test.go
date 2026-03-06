package application

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
)

func TestAuthzAppService_CreateAndEvaluate(t *testing.T) {
	resources := newMockResourceStore()
	depStore := newMockDependencyStore()
	evaluator := domainservice.NewAuthzEvaluator()
	app := NewAuthzAppService(resources, depStore, evaluator, nil)
	ctx := context.Background()

	// Create an ALLOW policy.
	policy := &model.AuthorizationPolicy{
		Name:        "allow-order-to-payment",
		Namespace:   "production",
		Action:      model.PolicyActionAllow,
		Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
		Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
	}

	if err := app.Create(ctx, policy); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Evaluate: order→payment should be allowed.
	allowed, err := app.Evaluate(ctx,
		model.ServiceID{Namespace: "production", Name: "order-service"},
		model.ServiceID{Namespace: "production", Name: "payment-service"},
	)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !allowed {
		t.Error("Evaluate() = false, want true")
	}

	// Evaluate: unknown→payment should be denied (default deny).
	denied, err := app.Evaluate(ctx,
		model.ServiceID{Namespace: "production", Name: "unknown"},
		model.ServiceID{Namespace: "production", Name: "payment-service"},
	)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if denied {
		t.Error("Evaluate() = true for unknown source, want false (default deny)")
	}
}

func TestAuthzAppService_DenyOverridesAllow(t *testing.T) {
	resources := newMockResourceStore()
	depStore := newMockDependencyStore()
	evaluator := domainservice.NewAuthzEvaluator()
	app := NewAuthzAppService(resources, depStore, evaluator, nil)
	ctx := context.Background()

	// Create ALLOW wildcard.
	app.Create(ctx, &model.AuthorizationPolicy{
		Name:        "allow-all-prod",
		Namespace:   "production",
		Action:      model.PolicyActionAllow,
		Source:      model.PolicySelector{Namespace: "production", Name: "*"},
		Destination: model.PolicySelector{Namespace: "production", Name: "*"},
	})

	// Create DENY for specific service.
	app.Create(ctx, &model.AuthorizationPolicy{
		Name:        "deny-order-to-payment",
		Namespace:   "production",
		Action:      model.PolicyActionDeny,
		Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
		Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
	})

	// order→payment should be denied (DENY overrides ALLOW).
	allowed, err := app.Evaluate(ctx,
		model.ServiceID{Namespace: "production", Name: "order-service"},
		model.ServiceID{Namespace: "production", Name: "payment-service"},
	)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if allowed {
		t.Error("Evaluate() = true, want false (DENY should override ALLOW)")
	}
}
