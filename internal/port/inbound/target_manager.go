package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// TargetManager defines the inbound use cases for NetworkTarget lifecycle management.
type TargetManager interface {
	// Apply creates or updates a network target.
	Apply(ctx context.Context, target *model.NetworkTarget) error
	// Get retrieves a network target by name.
	Get(ctx context.Context, name string) (*model.NetworkTarget, error)
	// List returns all network targets.
	List(ctx context.Context) ([]*model.NetworkTarget, error)
	// Delete removes a network target.
	Delete(ctx context.Context, name string) error
}
