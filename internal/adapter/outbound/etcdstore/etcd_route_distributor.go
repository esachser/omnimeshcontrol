package etcdstore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// EtcdRouteDistributor implements the RouteDistributor outbound port backed by etcd.
//
// It persists TargetRoute registrations using the shared StateStore,
// maintaining an in-memory cache that is seeded at startup and kept current via a
// background watch goroutine. This means:
//   - AnnounceRoute / WithdrawRoute are durable — routes survive restarts.
//   - AllRoutes is always a fast, lock-free in-memory read.
//   - DELETE events (including those from lease expiry) are broadcast to agents.
type EtcdRouteDistributor struct {
	stateStore  outbound.StateStore
	distributor outbound.ScopeDistributor // set via SetScopeDistributor; may be nil
	logger      *slog.Logger

	mu     sync.RWMutex
	routes map[string]model.TargetRoute // keyed by full etcd key
}

// NewEtcdRouteDistributor creates a new EtcdRouteDistributor.
// Call Run(ctx) in a goroutine to seed and watch for changes.
func NewEtcdRouteDistributor(stateStore outbound.StateStore, logger *slog.Logger) *EtcdRouteDistributor {
	if logger == nil {
		logger = slog.Default()
	}
	return &EtcdRouteDistributor{
		stateStore: stateStore,
		logger:     logger,
		routes:     make(map[string]model.TargetRoute),
	}
}

// SetScopeDistributor sets the distributor used to broadcast route removal
// events to agents (e.g. when a node's lease expires). Must be called before
// Run() to take effect. Set to nil to disable broadcasting.
func (d *EtcdRouteDistributor) SetScopeDistributor(dist outbound.ScopeDistributor) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.distributor = dist
}

// AnnounceRoute persists a TargetRoute to etcd.
// The in-memory cache is updated by the watch goroutine.
func (d *EtcdRouteDistributor) AnnounceRoute(ctx context.Context, route model.TargetRoute) error {
	// Pass leaseID=0: the distributor announces routes from a global perspective,
	// not tied to a single node's connection lease.
	if err := d.stateStore.PersistTargetRoute(ctx, &route, 0); err != nil {
		return fmt.Errorf("etcd route distributor: announce %q: %w", route.Name, err)
	}
	d.logger.Info("target route announced",
		"namespace", route.Namespace,
		"nodeID", route.NodeID,
		"targetService", route.TargetService,
		"name", route.Name,
	)
	return nil
}

// WithdrawRoute removes a TargetRoute from etcd.
// The in-memory cache is updated by the watch goroutine.
func (d *EtcdRouteDistributor) WithdrawRoute(ctx context.Context, route model.TargetRoute) error {
	if err := d.stateStore.DeleteTargetRoute(ctx, route.Namespace, route.NodeID, route.TargetService, route.Name); err != nil {
		return fmt.Errorf("etcd route distributor: withdraw %q: %w", route.Name, err)
	}
	d.logger.Info("target route withdrawn",
		"namespace", route.Namespace,
		"nodeID", route.NodeID,
		"name", route.Name,
	)
	return nil
}

// AllRoutes returns all currently cached TargetRoutes.
func (d *EtcdRouteDistributor) AllRoutes() []model.TargetRoute {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]model.TargetRoute, 0, len(d.routes))
	for _, r := range d.routes {
		result = append(result, r)
	}
	return result
}

// Run seeds the in-memory cache from etcd and then watches for changes.
// It blocks until ctx is cancelled. Call as a goroutine.
func (d *EtcdRouteDistributor) Run(ctx context.Context) error {
	// Seed cache from current etcd state.
	if err := d.loadAll(ctx); err != nil {
		return fmt.Errorf("etcd route distributor: initial load: %w", err)
	}

	// Watch for subsequent changes.
	eventCh, err := d.stateStore.WatchTargetRoutes(ctx)
	if err != nil {
		return fmt.Errorf("etcd route distributor: watch: %w", err)
	}

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return nil // channel closed — context cancelled
			}
			d.handleEvent(event)
		case <-ctx.Done():
			return nil
		}
	}
}

func (d *EtcdRouteDistributor) loadAll(ctx context.Context) error {
	routes, err := d.stateStore.ListTargetRoutes(ctx, "")
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.routes = make(map[string]model.TargetRoute, len(routes))
	for _, tr := range routes {
		key := formatTargetRouteKey(tr.Namespace, tr.NodeID, tr.TargetService, tr.Name)
		d.routes[key] = *tr
	}

	d.logger.Info("etcd route distributor seeded", "count", len(d.routes))
	return nil
}

func (d *EtcdRouteDistributor) handleEvent(event outbound.TargetRouteStateEvent) {
	d.mu.Lock()

	switch event.Type {
	case outbound.StateEventPut:
		if event.Route != nil {
			d.routes[event.Key] = *event.Route
		}
		d.mu.Unlock()

	case outbound.StateEventDelete:
		// Capture the route before removing it so we can broadcast.
		deleted, had := d.routes[event.Key]
		delete(d.routes, event.Key)
		dist := d.distributor
		d.mu.Unlock()

		if had && dist != nil {
			update := model.ScopeUpdate{
				Type:   model.ScopeUpdateRemove,
				Routes: []model.TargetRoute{deleted},
			}
			if err := dist.BroadcastUpdate(context.Background(), update); err != nil {
				d.logger.Warn("route distributor: broadcast remove failed",
					"route", deleted.Name,
					"nodeID", deleted.NodeID,
					"error", err,
				)
			}
		}
	}
}

// Compile-time interface check.
var _ outbound.RouteDistributor = (*EtcdRouteDistributor)(nil)
