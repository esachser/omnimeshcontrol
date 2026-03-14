package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// RouteManager defines the inbound use cases for TargetRoute lifecycle management.
type RouteManager interface {
	// Apply creates or updates a target route.
	Apply(ctx context.Context, route *model.TargetRoute) error
	// Get retrieves a target route by namespace and name.
	Get(ctx context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error)
	// List returns all target routes, optionally filtered by namespace.
	List(ctx context.Context, namespace string) ([]*model.TargetRoute, error)
	// Delete removes a target route.
	Delete(ctx context.Context, namespace, name, serviceName, nodeId string) error
	// DeleteByNodeId removes all target routes for a given node id.
	DeleteByNodeId(ctx context.Context, nodeId string) error
	// ClearRoutes removes all target routes.
	ClearRoutes(ctx context.Context) error
}
