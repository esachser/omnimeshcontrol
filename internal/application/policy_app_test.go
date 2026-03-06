package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
)

func TestTrafficPolicyAppService_Create(t *testing.T) {
	resources := newMockResourceStore()
	policyStore := newMockTrafficPolicyStore()
	validator := domainservice.NewPolicyValidator()
	app := NewTrafficPolicyAppService(policyStore, resources, validator, nil)
	ctx := context.Background()

	// Create the target service.
	resources.Put(ctx, "core.omnimesh.io/v1/Service/production/order-service", serviceToResource(&model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "order-service"},
		Protocol: model.ServiceProtocolHTTP,
	}))

	policy := &model.TrafficPolicy{
		Name:          "order-policy",
		Namespace:     "production",
		TargetService: model.ServiceID{Namespace: "production", Name: "order-service"},
		Retry: &model.RetryPolicy{
			MaxRetries:  3,
			RetryOn:     []string{"5xx"},
			BackoffBase: 100 * time.Millisecond,
			BackoffMax:  5 * time.Second,
		},
	}

	if err := app.CreateTrafficPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateTrafficPolicy() error = %v", err)
	}

	// Verify stored.
	got, err := app.GetTrafficPolicy(ctx, "production", "order-policy")
	if err != nil {
		t.Fatalf("GetTrafficPolicy() error = %v", err)
	}
	if got.Name != "order-policy" {
		t.Errorf("GetTrafficPolicy().Name = %q", got.Name)
	}
}

func TestTrafficPolicyAppService_Create_Conflict(t *testing.T) {
	resources := newMockResourceStore()
	policyStore := newMockTrafficPolicyStore()
	validator := domainservice.NewPolicyValidator()
	app := NewTrafficPolicyAppService(policyStore, resources, validator, nil)
	ctx := context.Background()

	svcID := model.ServiceID{Namespace: "production", Name: "order-service"}
	resources.Put(ctx, "core.omnimesh.io/v1/Service/production/order-service", serviceToResource(&model.Service{
		ID:       svcID,
		Protocol: model.ServiceProtocolHTTP,
	}))

	policy := &model.TrafficPolicy{
		Name: "p1", Namespace: "production", TargetService: svcID,
	}
	app.CreateTrafficPolicy(ctx, policy)

	// Second policy for the same service should fail.
	policy2 := &model.TrafficPolicy{
		Name: "p2", Namespace: "production", TargetService: svcID,
	}
	err := app.CreateTrafficPolicy(ctx, policy2)
	if !errors.Is(err, domain.ErrPolicyConflict) {
		t.Errorf("CreateTrafficPolicy() duplicate error = %v, want %v", err, domain.ErrPolicyConflict)
	}
}

func TestTrafficPolicyAppService_Create_ProtocolMismatch(t *testing.T) {
	resources := newMockResourceStore()
	policyStore := newMockTrafficPolicyStore()
	validator := domainservice.NewPolicyValidator()
	app := NewTrafficPolicyAppService(policyStore, resources, validator, nil)
	ctx := context.Background()

	// TCP service.
	resources.Put(ctx, "core.omnimesh.io/v1/Service/production/tcp-svc", serviceToResource(&model.Service{
		ID:       model.ServiceID{Namespace: "production", Name: "tcp-svc"},
		Protocol: model.ServiceProtocolTCP,
	}))

	policy := &model.TrafficPolicy{
		Name:          "bad-policy",
		Namespace:     "production",
		TargetService: model.ServiceID{Namespace: "production", Name: "tcp-svc"},
		Retry: &model.RetryPolicy{
			MaxRetries: 3,
			RetryOn:    []string{"5xx"}, // L7 feature on TCP → should fail.
		},
	}

	err := app.CreateTrafficPolicy(ctx, policy)
	if !errors.Is(err, domain.ErrProtocolMismatch) {
		t.Errorf("CreateTrafficPolicy() error = %v, want %v", err, domain.ErrProtocolMismatch)
	}
}
