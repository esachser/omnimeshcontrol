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
	if route.Name == "" || route.Namespace == "" {
		return fmt.Errorf("route apply: name and namespace are required")
	}

	key := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/%s", route.Namespace, route.Name)

	if _, err := s.resources.Put(ctx, key, routeToResource(route)); err != nil {
		return fmt.Errorf("route apply: %w", err)
	}

	s.logger.Info("target route applied", "namespace", route.Namespace, "name", route.Name, "targetService", route.TargetService, "destinationTarget", route.DestinationTarget, "weight", route.Weight)
	return nil
}

// Get retrieves a target route by namespace and name.
func (s *RouteAppService) Get(ctx context.Context, namespace, name string) (*model.TargetRoute, error) {
	key := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/%s", namespace, name)
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("route get: %w", err)
	}
	return resourceToRoute(resource)
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

// Delete removes a target route.
func (s *RouteAppService) Delete(ctx context.Context, namespace, name string) error {
	key := fmt.Sprintf("traffic.omnimesh.io/v1/TargetRoute/%s/%s", namespace, name)
	if err := s.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("route delete: %w", err)
	}

	s.logger.Info("target route deleted", "namespace", namespace, "name", name)
	return nil
}

// Compile-time interface check.
var _ inbound.RouteManager = (*RouteAppService)(nil)
