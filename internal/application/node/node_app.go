package node

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// NodeAppService implements the NodeRegistrar inbound port.
// It coordinates node registration with persistent storage (StateStore)
// and distributed liveness tracking (NodeLivenessTracker via etcd lease).
type NodeAppService struct {
	state        outbound.StateStore
	tracker      service.NodeTracker
	routeManager inbound.RouteManager
	logger       *slog.Logger
}

// NewNodeAppService creates a new NodeAppService.
func NewNodeAppService(state outbound.StateStore, tracker service.NodeTracker, routeManager inbound.RouteManager, logger *slog.Logger) *NodeAppService {
	if logger == nil {
		logger = slog.Default()
	}
	nap := &NodeAppService{
		state:        state,
		tracker:      tracker,
		routeManager: routeManager,
		logger:       logger,
	}
	tracker.RegisterCallback(func(ctx context.Context, nodeID string) {
		nap.logger.Warn("node lost (lease expired or revoked)", "nodeID", nodeID)
	})
	return nap
}

// RegisterNode registers a new agent node.
func (s *NodeAppService) RegisterNode(ctx context.Context, node *model.Node) error {
	if node.ID == "" {
		return fmt.Errorf("node register: %w: node ID is required", domain.ErrInvalidName)
	}

	node.Status = model.NodeStatusActive

	leaseID, err := s.tracker.TrackNode(node.ID)
	if err != nil {
		return fmt.Errorf("node register: %w", err)
	}

	if err := s.state.PersistNode(ctx, node.ID, node, leaseID); err != nil {
		s.tracker.UntrackNode(node.ID)
		return fmt.Errorf("node register: %w", err)
	}

	s.logger.Info("node registered",
		"nodeID", node.ID,
		"platform", node.Platform,
		"addr", node.Addr,
	)
	return nil
}

// DeregisterNode gracefully deregisters a node by revoking its etcd lease.
func (s *NodeAppService) DeregisterNode(ctx context.Context, nodeID string) error {
	s.tracker.UntrackNode(nodeID)

	s.logger.Info("node deregistered", "nodeID", nodeID)
	return nil
}

// DrainNode initiates graceful node draining.
func (s *NodeAppService) DrainNode(ctx context.Context, nodeID string) error {
	node, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("drain node: %w", err)
	}

	node.Status = model.NodeStatusDraining

	leaseID := s.tracker.LeaseID(nodeID)
	if err := s.state.PersistNode(ctx, nodeID, node, leaseID); err != nil {
		return fmt.Errorf("drain node: persist: %w", err)
	}

	s.tracker.OnDisconnect(nodeID)

	s.logger.Info("node draining — lease TTL counting down",
		"nodeID", nodeID,
	)
	return nil
}

// OnStreamDisconnect handles an unexpected agent stream disconnection.
func (s *NodeAppService) OnStreamDisconnect(_ context.Context, nodeID string) error {
	s.tracker.OnDisconnect(nodeID)
	s.logger.Info("node stream disconnected, lease TTL counting down", "nodeID", nodeID)
	return nil
}

// OnStreamReconnect handles an agent reconnecting.
func (s *NodeAppService) OnStreamReconnect(ctx context.Context, nodeID string) error {
	leaseID, err := s.tracker.TrackNode(nodeID)
	if err != nil {
		return fmt.Errorf("node reconnect: %w", err)
	}

	node, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("node reconnect: get node: %w", err)
	}
	node.Status = model.NodeStatusActive
	if err := s.state.PersistNode(ctx, nodeID, node, leaseID); err != nil {
		return fmt.Errorf("node reconnect: persist: %w", err)
	}

	s.logger.Info("node stream reconnected", "nodeID", nodeID)
	return nil
}

// UpdateNodeServices updates the list of services for a registered node.
func (s *NodeAppService) UpdateNodeServices(ctx context.Context, nodeID string, added []model.ServiceID, removed []model.ServiceID) error {
	node, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("update node services: %w", err)
	}
	if node == nil {
		return fmt.Errorf("update node services: %w: node %q not found", domain.ErrNodeNotFound, nodeID)
	}

	svcMap := make(map[model.ServiceID]bool)
	for _, svc := range node.Services {
		svcMap[svc] = true
	}
	for _, svc := range added {
		svcMap[svc] = true
	}
	for _, svc := range removed {
		delete(svcMap, svc)
	}

	node.Services = make([]model.ServiceID, 0, len(svcMap))
	for svc := range svcMap {
		node.Services = append(node.Services, svc)
	}

	leaseID := s.tracker.LeaseID(nodeID)
	if err := s.state.PersistNode(ctx, node.ID, node, leaseID); err != nil {
		return fmt.Errorf("update node services: %w", err)
	}

	s.logger.Info("node services updated",
		"nodeID", node.ID,
		"added", len(added),
		"removed", len(removed),
	)
	return nil
}

// Compile-time interface check.
var _ inbound.NodeRegistrar = (*NodeAppService)(nil)
