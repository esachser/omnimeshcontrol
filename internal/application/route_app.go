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
	resources   outbound.ResourceStore
	state       outbound.StateStore
	distributor outbound.ScopeDistributor
	targets     inbound.TargetManager
	logger      *slog.Logger
}

// NewRouteAppService creates a new RouteAppService.
func NewRouteAppService(resources outbound.ResourceStore, state outbound.StateStore, targets inbound.TargetManager, logger *slog.Logger) *RouteAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RouteAppService{
		resources: resources,
		state:     state,
		targets:   targets,
		logger:    logger,
	}
}

// SetScopeDistributor sets the scope distributor (required due to circular dependency in DI).
func (s *RouteAppService) SetScopeDistributor(d outbound.ScopeDistributor) {
	s.distributor = d
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

	s.broadcastUpdate(ctx, *route, model.ScopeUpdateModify)

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
	// Need the route to broadcast deletion
	route, getErr := s.Get(ctx, namespace, name, serviceName, nodeId)

	key := formatRouteKey(namespace, nodeId, serviceName, name)
	err := s.resources.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("route delete: %w", err)
	}

	s.logger.Info("target route deleted", "namespace", namespace, "name", name)

	if getErr == nil && route != nil {
		s.broadcastUpdate(ctx, *route, model.ScopeUpdateRemove)
	}

	return nil
}

// DeleteByNodeId removes all target routes for a given node id.
func (s *RouteAppService) DeleteByNodeId(ctx context.Context, nodeId string) error {
	key := fmt.Sprintf(keyPrefix+"%s", nodeId)

	// Fetch the routes first so we can broadcast their removal
	resources, listErr := s.resources.List(ctx, key)
	var routesToDelete []model.TargetRoute
	if listErr == nil {
		for _, r := range resources {
			if rt, err := resourceToRoute(r); err == nil && rt != nil {
				routesToDelete = append(routesToDelete, *rt)
			}
		}
	}

	err := s.resources.DeleteByPrefix(ctx, key)
	if err != nil {
		return fmt.Errorf("route delete by node id: %w", err)
	}

	s.logger.Info("target route deleted", "nodeId", nodeId)

	for _, route := range routesToDelete {
		s.broadcastUpdate(ctx, route, model.ScopeUpdateRemove)
	}

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

func (s *RouteAppService) broadcastUpdate(ctx context.Context, route model.TargetRoute, updateType model.ScopeUpdateType) {
	if s.distributor == nil {
		return
	}

	// Calculate correct endpoint address based on targets if possible.
	// Since we broadcast to all agents, the receiver AgentServer will send it.
	// We want to attach the endpoint address or gateway address. But since this is a global broadcast,
	// some agents might be in the same network, some in another.
	// For TargetRoute, in a pure broadcast, we should annotate it.
	// Let's resolve the raw address first.
	if route.Address == "" {
		destNode, err := s.state.GetNode(ctx, route.NodeID)
		if err == nil && destNode != nil {
			route.Address = destNode.Addr
			route.Target = destNode.Target
			if destNode.Target != "" {
				netTarget, tErr := s.targets.Get(ctx, destNode.Target)
				if tErr == nil && netTarget != nil && netTarget.GatewayAddress != "" {
					route.TargetAddress = netTarget.GatewayAddress
				}
			}
		}
	}

	update := model.ScopeUpdate{
		Type:   updateType,
		Routes: []model.TargetRoute{route},
	}

	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("failed to broadcast route scope update", "route", route.Name, "error", err)
	}
}

// Compile-time interface check.
var _ inbound.RouteManager = (*RouteAppService)(nil)
