// Package inbound defines the driving port interfaces — use cases exposed to
// external actors (gRPC handlers, CLI, etc). Implementations live in internal/application/.
package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// ServiceManager defines the inbound use cases for service lifecycle management.
type ServiceManager interface {
	// Apply creates or updates a service. On creation, allocates a mesh IP,
	// registers the SPIFFE identity, updates DNS, and announces the route.
	Apply(ctx context.Context, service *model.Service) error
	// Get retrieves a service by namespace and name.
	// Returns domain.ErrServiceNotFound if the service does not exist.
	Get(ctx context.Context, namespace, name string) (*model.Service, error)
	// List returns all services in the given namespace.
	List(ctx context.Context, namespace string) ([]*model.Service, error)
	// Delete removes a service, releasing its mesh IP, deregistering identity,
	// removing DNS records, and withdrawing routes.
	Delete(ctx context.Context, namespace, name string) error
}
