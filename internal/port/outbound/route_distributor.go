package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// RouteDistributor announces and withdraws mesh routes to/from agent nodes.
// Multiple adapters can be active simultaneously (e.g., BGP + gRPC push).
type RouteDistributor interface {
	// AnnounceRoute announces a new or updated route to all relevant nodes.
	AnnounceRoute(ctx context.Context, route model.MeshRoute) error
	// WithdrawRoute withdraws a route, indicating the service is no longer reachable
	// via the specified nodes.
	WithdrawRoute(ctx context.Context, route model.MeshRoute) error

	// AllRoutes returns all currently announced routes.
	AllRoutes() []model.MeshRoute
}
