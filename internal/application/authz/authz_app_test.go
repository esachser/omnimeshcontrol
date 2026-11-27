package authz

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
)

func TestAuthzAppService_CreateAndEvaluate(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	depStore := apptest.NewMockDependencyStore()
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
	resources := apptest.NewMockResourceStore()
	depStore := apptest.NewMockDependencyStore()
	evaluator := domainservice.NewAuthzEvaluator()
	app := NewAuthzAppService(resources, depStore, evaluator, nil)
	ctx := context.Background()

	if err := app.Create(ctx, &model.AuthorizationPolicy{
		Name:        "allow-all-prod",
		Namespace:   "production",
		Action:      model.PolicyActionAllow,
		Source:      model.PolicySelector{Namespace: "production", Name: "*"},
		Destination: model.PolicySelector{Namespace: "production", Name: "*"},
	}); err != nil {
		t.Fatalf("Create(allow-all-prod) error = %v", err)
	}

	if err := app.Create(ctx, &model.AuthorizationPolicy{
		Name:        "deny-order-to-payment",
		Namespace:   "production",
		Action:      model.PolicyActionDeny,
		Source:      model.PolicySelector{Namespace: "production", Name: "order-service"},
		Destination: model.PolicySelector{Namespace: "production", Name: "payment-service"},
	}); err != nil {
		t.Fatalf("Create(deny-order-to-payment) error = %v", err)
	}

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

func TestAuthzAppService_CreateUpdateGetList_RichFields(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	depStore := apptest.NewMockDependencyStore()
	evaluator := domainservice.NewAuthzEvaluator()
	app := NewAuthzAppService(resources, depStore, evaluator, nil)
	ctx := context.Background()

	from := time.Date(2026, 3, 4, 16, 0, 0, 0, time.UTC)
	until := time.Date(2026, 3, 4, 18, 0, 0, 0, time.UTC)

	policy := &model.AuthorizationPolicy{
		Name:      "rich-policy",
		Namespace: "production",
		Action:    model.PolicyActionDryRun,
		Source: model.PolicySelector{
			Namespace:   "production",
			Name:        "order-service",
			Labels:      map[string]string{"tier": "app"},
			SpiffeID:    "spiffe://omnimesh.local/ns/production/sa/order-service",
			TrustDomain: "omnimesh.local",
		},
		Destination: model.PolicySelector{
			Namespace: "production",
			Name:      "payment-service",
			Labels:    map[string]string{"pci": "true"},
		},
		DestinationPorts: []int{8080, 50051},
		Rules: []model.AuthzRule{
			{
				HTTP: &model.HTTPAuthzRule{
					Methods:      []string{"POST"},
					PathPrefixes: []string{"/api/v1/charge"},
					Headers:      map[string]string{"x-env": "prod"},
				},
			},
			{
				GRPC: &model.GRPCAuthzRule{
					Services: []string{"payment.v1.PaymentService"},
					Methods:  []string{"Charge"},
				},
			},
		},
		ValidFrom:  &from,
		ValidUntil: &until,
	}

	if err := app.Create(ctx, policy); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := app.Get(ctx, "production", "rich-policy")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(got, policy) {
		t.Fatalf("Get() mismatch\ngot:  %+v\nwant: %+v", got, policy)
	}

	// Update ports and action
	policy.Action = model.PolicyActionAllow
	policy.DestinationPorts = []int{8443}
	if err := app.Update(ctx, policy); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	list, err := app.List(ctx, "production")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].Action != model.PolicyActionAllow || len(list[0].DestinationPorts) != 1 || list[0].DestinationPorts[0] != 8443 {
		t.Fatalf("unexpected List() result: %+v", list)
	}
}
