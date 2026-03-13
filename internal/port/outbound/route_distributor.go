package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// RouteDistributor stores and retrieves TargetRoute registrations from agents.
// Each TargetRoute represents a per-node, per-service registration fact.
// Multiple adapters can be active simultaneously.
type RouteDistributor interface {
	// AnnounceRoute registers or updates a TargetRoute (a node's claim to serve a service).
	AnnounceRoute(ctx context.Context, route model.TargetRoute) error
	// WithdrawRoute removes a TargetRoute registration.
	WithdrawRoute(ctx context.Context, route model.TargetRoute) error

	// AllRoutes returns all currently registered TargetRoutes.
	AllRoutes() []model.TargetRoute
}
