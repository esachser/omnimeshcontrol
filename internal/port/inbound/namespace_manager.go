package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// NamespaceManager defines the inbound use cases for namespace lifecycle management.
type NamespaceManager interface {
	// Apply creates or updates a namespace.
	Apply(ctx context.Context, namespace *model.Namespace) error
	// Get retrieves a namespace by name.
	// Returns domain.ErrNamespaceNotFound if the namespace does not exist.
	Get(ctx context.Context, name string) (*model.Namespace, error)
	// List returns all namespaces.
	List(ctx context.Context) ([]*model.Namespace, error)
	// Delete removes a namespace. Returns domain.ErrNamespaceNotEmpty if the
	// namespace still contains resources (unless force is true).
	Delete(ctx context.Context, name string, force bool) error
}
