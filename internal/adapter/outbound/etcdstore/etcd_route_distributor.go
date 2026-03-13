package etcdstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// --- TargetRoute codec (local to etcdstore to avoid import cycle) ---

type targetRouteSpec struct {
	TargetService string            `json:"targetService,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	NodeID        string            `json:"nodeID,omitempty"`
}

func routeToResource(rt *model.TargetRoute) *model.Resource {
	spec := targetRouteSpec{
		TargetService: rt.TargetService,
		Labels:        rt.Labels,
		NodeID:        rt.NodeID,
	}
	specJSON, _ := json.Marshal(spec)
	return &model.Resource{
		APIVersion: "traffic.omnimesh.io/v1",
		Kind:       "TargetRoute",
		Metadata: model.ResourceMetadata{
			Name:      rt.Name,
			Namespace: rt.Namespace,
		},
		Spec: specJSON,
	}
}

func resourceToRoute(r *model.Resource) (*model.TargetRoute, error) {
	if r.Kind != "TargetRoute" {
		return nil, fmt.Errorf("expected kind TargetRoute, got %s", r.Kind)
	}
	var spec targetRouteSpec
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal target route spec: %w", err)
		}
	}
	return &model.TargetRoute{
		Name:          r.Metadata.Name,
		Namespace:     r.Metadata.Namespace,
		TargetService: spec.TargetService,
		Labels:        spec.Labels,
		NodeID:        spec.NodeID,
	}, nil
}

const targetRoutePrefix = "traffic.omnimesh.io/v1/TargetRoute/"

// EtcdRouteDistributor implements the RouteDistributor outbound port backed by etcd.
//
// It persists TargetRoute registrations using the shared ResourceStore (same key
// space as RouteAppService), maintaining an in-memory cache that is seeded at
// startup and kept current via a background watch goroutine. This means:
//   - AnnounceRoute / WithdrawRoute are durable — routes survive restarts.
//   - AllRoutes is always a fast, lock-free in-memory read.
//   - Routes written by RouteAppService (e.g. via CLI) are automatically reflected.
type EtcdRouteDistributor struct {
	resources outbound.ResourceStore
	logger    *slog.Logger

	mu     sync.RWMutex
	routes map[string]model.TargetRoute // keyed by etcd key suffix
}

// NewEtcdRouteDistributor creates a new EtcdRouteDistributor.
// Call Run(ctx) in a goroutine to seed and watch for changes.
func NewEtcdRouteDistributor(resources outbound.ResourceStore, logger *slog.Logger) *EtcdRouteDistributor {
	if logger == nil {
		logger = slog.Default()
	}
	return &EtcdRouteDistributor{
		resources: resources,
		logger:    logger,
		routes:    make(map[string]model.TargetRoute),
	}
}

// AnnounceRoute persists a TargetRoute to etcd.
// The in-memory cache is updated by the watch goroutine.
func (d *EtcdRouteDistributor) AnnounceRoute(ctx context.Context, route model.TargetRoute) error {
	key := targetRouteKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	resource := routeToResource(&route)
	if _, err := d.resources.Put(ctx, key, resource); err != nil {
		return fmt.Errorf("etcd route distributor: announce %q: %w", key, err)
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
	key := targetRouteKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	if err := d.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("etcd route distributor: withdraw %q: %w", key, err)
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
	eventCh, err := d.resources.Watch(ctx, targetRoutePrefix)
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
	resources, err := d.resources.List(ctx, targetRoutePrefix)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.routes = make(map[string]model.TargetRoute, len(resources))
	for _, r := range resources {
		tr, err := resourceToRoute(r)
		if err != nil {
			d.logger.Warn("skipping invalid target route resource on load", "error", err)
			continue
		}
		key := targetRouteKey(tr.Namespace, tr.NodeID, tr.TargetService, tr.Name)
		d.routes[key] = *tr
	}

	d.logger.Info("etcd route distributor seeded", "count", len(d.routes))
	return nil
}

func (d *EtcdRouteDistributor) handleEvent(event outbound.ResourceEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch event.Type {
	case outbound.ResourceEventPut:
		if event.Resource != nil {
			tr, err := resourceToRoute(event.Resource)
			if err != nil {
				d.logger.Warn("invalid target route in watch event", "error", err)
				return
			}
			key := targetRouteKey(tr.Namespace, tr.NodeID, tr.TargetService, tr.Name)
			d.routes[key] = *tr
		}
	case outbound.ResourceEventDelete:
		// On delete the resource may still be available (PrevKv). Try to decode key.
		if event.Resource != nil {
			tr, err := resourceToRoute(event.Resource)
			if err == nil {
				key := targetRouteKey(tr.Namespace, tr.NodeID, tr.TargetService, tr.Name)
				delete(d.routes, key)
				return
			}
		}
		// Fall back: delete by etcd key suffix.
		delete(d.routes, event.Key)
	}
}

func targetRouteKey(namespace, nodeID, targetService, name string) string {
	return targetRoutePrefix + namespace + "/" + nodeID + "/" + targetService + "/" + name
}

// Compile-time interface check.
var _ outbound.RouteDistributor = (*EtcdRouteDistributor)(nil)
