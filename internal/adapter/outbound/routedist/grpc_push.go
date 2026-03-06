// Package routedist provides route distribution adapters.
package routedist

import (
	"context"
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
	routes map[string]model.MeshRoute // keyed by ServiceID.String()
	logger *slog.Logger
}

// NewGRPCRouteDistributor creates a new route distributor.
func NewGRPCRouteDistributor(logger *slog.Logger) *GRPCRouteDistributor {
	if logger == nil {
		logger = slog.Default()
	}
	return &GRPCRouteDistributor{
		routes: make(map[string]model.MeshRoute),
		logger: logger,
	}
}

// AnnounceRoute stores or updates a route for future distribution to agents.
func (d *GRPCRouteDistributor) AnnounceRoute(_ context.Context, route model.MeshRoute) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := route.ServiceID.String()
	d.routes[key] = route
	d.logger.Info("route announced",
		"service", key,
		"meshAddr", route.MeshAddr,
		"targetNodes", len(route.TargetNodes),
	)
	return nil
}

// WithdrawRoute removes a route from the distribution state.
func (d *GRPCRouteDistributor) WithdrawRoute(_ context.Context, route model.MeshRoute) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := route.ServiceID.String()
	delete(d.routes, key)
	d.logger.Info("route withdrawn", "service", key)
	return nil
}

// GetRoute returns a currently announced route for a service.
// Returns nil if no route exists.
func (d *GRPCRouteDistributor) GetRoute(serviceID model.ServiceID) *model.MeshRoute {
	d.mu.RLock()
	defer d.mu.RUnlock()

	route, ok := d.routes[serviceID.String()]
	if !ok {
		return nil
	}
	return &route
}

// AllRoutes returns all currently announced routes.
func (d *GRPCRouteDistributor) AllRoutes() []model.MeshRoute {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]model.MeshRoute, 0, len(d.routes))
	for _, route := range d.routes {
		result = append(result, route)
	}
	return result
}

// Compile-time interface check.
var _ outbound.RouteDistributor = (*GRPCRouteDistributor)(nil)
