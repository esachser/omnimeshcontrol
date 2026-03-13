package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// RouteAppService implements the RouteManager inbound port.
type RouteAppService struct {
	resources outbound.ResourceStore
	logger    *slog.Logger
}

// NewRouteAppService creates a new RouteAppService.
func NewRouteAppService(resources outbound.ResourceStore, logger *slog.Logger) *RouteAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RouteAppService{
		resources: resources,
		logger:    logger,
	}
}

// Apply creates or updates a target route.
func (s *RouteAppService) Apply(ctx context.Context, route *model.TargetRoute) error {
	if route.Name == "" || route.Namespace == "" || route.NodeID == "" || route.TargetService == "" {
		return fmt.Errorf("route apply: name, namespace, nodeID and targetService are required")
	}

	key := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/%s/%s/%s", route.Namespace, route.NodeID, route.TargetService, route.Name)

	if _, err := s.resources.Put(ctx, key, routeToResource(route)); err != nil {
		return fmt.Errorf("route apply: %w", err)
	}

	s.logger.Info("target route applied", "namespace", route.Namespace, "name", route.Name, "targetService", route.TargetService)
	return nil
}

// Get retrieves a target route by namespace and name.
// Because routes are keyed by {namespace}/{nodeID}/{targetService}/{name},
// it scans all routes in the namespace and returns the first that matches the name.
func (s *RouteAppService) Get(ctx context.Context, namespace, name string) (*model.TargetRoute, error) {
	prefix := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/", namespace)
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("route get: %w", err)
	}
	for _, r := range resources {
		if r.Metadata.Name == name {
			return resourceToRoute(r)
		}
	}
	return nil, fmt.Errorf("route get: %s/%s not found", namespace, name)
}

// List returns all target routes, optionally filtered by namespace.
func (s *RouteAppService) List(ctx context.Context, namespace string) ([]*model.TargetRoute, error) {
	prefix := "traffic.omnimesh.io/v1/TargetRoute/"
	if namespace != "" {
		prefix += namespace + "/"
	}

	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("route list: %w", err)
	}

	routes := make([]*model.TargetRoute, 0, len(resources))
	for _, r := range resources {
		rt, err := resourceToRoute(r)
		if err != nil {
			s.logger.Warn("skipping invalid route resource", "error", err)
			continue
		}
		routes = append(routes, rt)
	}
	return routes, nil
}

// Delete removes a target route by namespace and name.
// It scans all routes in the namespace, finds the one with the matching name,
// and deletes it using the full 4-part key.
func (s *RouteAppService) Delete(ctx context.Context, namespace, name string) error {
	prefix := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/", namespace)
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("route delete: list: %w", err)
	}
	for _, r := range resources {
		if r.Metadata.Name == name {
			rt, mErr := resourceToRoute(r)
			if mErr != nil {
				return fmt.Errorf("route delete: decode: %w", mErr)
			}
			key := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/%s/%s/%s",
				rt.Namespace, rt.NodeID, rt.TargetService, rt.Name)
			if err := s.resources.Delete(ctx, key); err != nil {
				return fmt.Errorf("route delete: %w", err)
			}
			s.logger.Info("target route deleted", "namespace", namespace, "name", name)
			return nil
		}
	}
	return fmt.Errorf("route delete: %s/%s not found", namespace, name)
}

// Compile-time interface check.
var _ inbound.RouteManager = (*RouteAppService)(nil)
