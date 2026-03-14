package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

const keyPrefix = "traffic.omnimesh.io/v1/TargetRoute/"

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

	key := formatRouteKey(route.Namespace, route.NodeID, route.TargetService, route.Name)

	if _, err := s.resources.Put(ctx, key, routeToResource(route)); err != nil {
		return fmt.Errorf("route apply: %w", err)
	}

	s.logger.Info("target route applied", "namespace", route.Namespace, "name", route.Name, "targetService", route.TargetService)
	return nil
}

// Get retrieves a target route by namespace and name.
// Because routes are keyed by {namespace}/{nodeID}/{targetService}/{name},
// it scans all routes in the namespace and returns the first that matches the name.
func (s *RouteAppService) Get(ctx context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error) {
	key := formatRouteKey(namespace, nodeId, serviceName, name)
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("route get: %w", err)
	}
	return resourceToRoute(resource)
}

// List returns all target routes, optionally filtered by namespace.
func (s *RouteAppService) List(ctx context.Context, namespace string) ([]*model.TargetRoute, error) {
	prefix := keyPrefix

	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("route list: %w", err)
	}

	routes := make([]*model.TargetRoute, 0, len(resources))
	for _, r := range resources {
		if namespace != "" && r.Metadata.Namespace != namespace {
			continue
		}
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
func (s *RouteAppService) Delete(ctx context.Context, namespace, name, serviceName, nodeId string) error {
	key := formatRouteKey(namespace, nodeId, serviceName, name)
	err := s.resources.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("route delete: %w", err)
	}

	s.logger.Info("target route deleted", "namespace", namespace, "name", name)
	return nil
}

// DeleteByNodeId removes all target routes for a given node id.
func (s *RouteAppService) DeleteByNodeId(ctx context.Context, nodeId string) error {
	key := fmt.Sprintf(keyPrefix+"%s", nodeId)
	err := s.resources.DeleteByPrefix(ctx, key)
	if err != nil {
		return fmt.Errorf("route delete by node id: %w", err)
	}

	s.logger.Info("target route deleted", "nodeId", nodeId)
	return nil
}

func (s *RouteAppService) ClearRoutes(ctx context.Context) error {
	err := s.resources.DeleteByPrefix(ctx, keyPrefix)
	if err != nil {
		return fmt.Errorf("route clear: %w", err)
	}

	s.logger.Info("target routes cleared")
	return nil
}

func formatRouteKey(namespace, nodeId, serviceName, name string) string {
	return fmt.Sprintf(keyPrefix+"%s/%s/%s/%s", nodeId, namespace, serviceName, name)
}

// Compile-time interface check.
var _ inbound.RouteManager = (*RouteAppService)(nil)
