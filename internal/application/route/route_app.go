package route

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// RouteAppService implements the RouteManager inbound port.
type RouteAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	distributor outbound.ScopeDistributor
	targets     inbound.TargetManager
	tracker     service.NodeTracker
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

// SetScopeDistributor sets the scope distributor.
func (s *RouteAppService) SetScopeDistributor(d outbound.ScopeDistributor) {
	s.distributor = d
}

// SetNodeTracker sets the node tracker.
func (s *RouteAppService) SetNodeTracker(t service.NodeTracker) {
	s.tracker = t
}

// Apply creates or updates a target route.
func (s *RouteAppService) Apply(ctx context.Context, route *model.TargetRoute) error {
	if route.Name == "" || route.Namespace == "" || route.NodeID == "" || route.TargetService == "" {
		return fmt.Errorf("route apply: name, namespace, nodeID and targetService are required")
	}

	var leaseID int64
	if s.tracker != nil {
		leaseID = s.tracker.LeaseID(route.NodeID)
	}

	if err := s.state.PersistTargetRoute(ctx, route, leaseID); err != nil {
		return fmt.Errorf("route apply: %w", err)
	}

	s.logger.Info("target route applied", "namespace", route.Namespace, "name", route.Name, "targetService", route.TargetService)

	s.broadcastUpdate(ctx, *route, model.ScopeUpdateModify)

	return nil
}

// Get retrieves a target route by namespace and name.
func (s *RouteAppService) Get(ctx context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error) {
	return s.state.GetTargetRoute(ctx, namespace, nodeId, serviceName, name)
}

// List returns all target routes, optionally filtered by namespace.
func (s *RouteAppService) List(ctx context.Context, namespace string) ([]*model.TargetRoute, error) {
	return s.state.ListTargetRoutes(ctx, namespace)
}

// Delete removes a target route.
func (s *RouteAppService) Delete(ctx context.Context, namespace, name, serviceName, nodeId string) error {
	route, getErr := s.Get(ctx, namespace, name, serviceName, nodeId)

	err := s.state.DeleteTargetRoute(ctx, namespace, nodeId, serviceName, name)
	if err != nil {
		return fmt.Errorf("route delete: %w", err)
	}

	s.logger.Info("target route deleted", "namespace", namespace, "name", name)

	if getErr == nil && route != nil {
		s.broadcastUpdate(ctx, *route, model.ScopeUpdateRemove)
	}

	return nil
}

// DeleteByNodeId broadcasts removal of all routes for a node.
func (s *RouteAppService) DeleteByNodeId(ctx context.Context, nodeId string) error {
	nodeRoutes, listErr := s.state.ListTargetRoutesByNode(ctx, nodeId)
	var routesToDelete []model.TargetRoute
	if listErr == nil {
		for _, rt := range nodeRoutes {
			routesToDelete = append(routesToDelete, *rt)
		}
	}

	s.logger.Info("target routes cleared for node", "nodeId", nodeId)

	for _, route := range routesToDelete {
		s.broadcastUpdate(ctx, route, model.ScopeUpdateRemove)
	}

	return nil
}

func (s *RouteAppService) ClearRoutes(ctx context.Context) error {
	err := s.state.ClearTargetRoutes(ctx)
	if err != nil {
		return fmt.Errorf("route clear: %w", err)
	}

	s.logger.Info("target routes cleared")
	return nil
}

func (s *RouteAppService) broadcastUpdate(ctx context.Context, route model.TargetRoute, updateType model.ScopeUpdateType) {
	if s.distributor == nil {
		return
	}

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
