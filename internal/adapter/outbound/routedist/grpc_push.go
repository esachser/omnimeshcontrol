// Package routedist provides route distribution adapters.
package routedist

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// GRPCRouteDistributor implements the RouteDistributor outbound port.
// In the MVP, it stores pending route updates in-memory. The actual push
// to agents happens via the bidirectional gRPC stream in Phase 4.
type GRPCRouteDistributor struct {
	mu     sync.RWMutex
	routes map[string]model.TargetRoute // keyed by namespace/nodeID/targetService/name
	logger *slog.Logger
}

// NewGRPCRouteDistributor creates a new route distributor.
func NewGRPCRouteDistributor(logger *slog.Logger) *GRPCRouteDistributor {
	if logger == nil {
		logger = slog.Default()
	}
	return &GRPCRouteDistributor{
		routes: make(map[string]model.TargetRoute),
		logger: logger,
	}
}

// routeKey returns a unique key for a TargetRoute.
func routeKey(route model.TargetRoute) string {
	return fmt.Sprintf("%s/%s/%s/%s", route.Namespace, route.NodeID, route.TargetService, route.Name)
}

// AnnounceRoute stores or updates a TargetRoute registration.
func (d *GRPCRouteDistributor) AnnounceRoute(_ context.Context, route model.TargetRoute) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := routeKey(route)
	d.routes[key] = route
	d.logger.Info("target route announced",
		"namespace", route.Namespace,
		"nodeID", route.NodeID,
		"targetService", route.TargetService,
		"name", route.Name,
	)
	return nil
}

// WithdrawRoute removes a TargetRoute registration.
func (d *GRPCRouteDistributor) WithdrawRoute(_ context.Context, route model.TargetRoute) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := routeKey(route)
	delete(d.routes, key)
	d.logger.Info("target route withdrawn",
		"namespace", route.Namespace,
		"nodeID", route.NodeID,
		"name", route.Name,
	)
	return nil
}

// AllRoutes returns all currently registered TargetRoutes.
func (d *GRPCRouteDistributor) AllRoutes() []model.TargetRoute {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]model.TargetRoute, 0, len(d.routes))
	for _, route := range d.routes {
		result = append(result, route)
	}
	return result
}

// Compile-time interface check.
var _ outbound.RouteDistributor = (*GRPCRouteDistributor)(nil)
