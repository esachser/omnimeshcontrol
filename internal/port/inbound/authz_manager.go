package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// AuthorizationPolicyManager defines the inbound use cases for authorization
// policy management. Authorization policies control service-to-service access
// using a DENY-first → ALLOW-list → default-DENY evaluation chain.
type AuthorizationPolicyManager interface {
	// Create creates a new authorization policy.
	// Returns domain.ErrDuplicatePolicy if a policy with the same name already exists.
	Create(ctx context.Context, policy *model.AuthorizationPolicy) error
	// Update updates an existing authorization policy.
	Update(ctx context.Context, policy *model.AuthorizationPolicy) error
	// Get retrieves an authorization policy by namespace and name.
	Get(ctx context.Context, namespace, name string) (*model.AuthorizationPolicy, error)
	// List returns all authorization policies in the given namespace.
	List(ctx context.Context, namespace string) ([]*model.AuthorizationPolicy, error)
	// Delete removes an authorization policy and updates the dependency graph.
	Delete(ctx context.Context, namespace, name string) error
	// Evaluate checks whether traffic from source to destination is authorized.
	Evaluate(ctx context.Context, source, destination model.ServiceID) (bool, error)
}
