package application

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
	// When a node's lease expires or is revoked, notify app logic (logging, etc.).
	// Etcd already handles cleanup of node state and route keys automatically.
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

	// Grant the etcd lease first — the returned LeaseID is attached to the
	// node state key (and later to route keys) so they are cleaned up automatically.
	leaseID, err := s.tracker.TrackNode(node.ID)
	if err != nil {
		return fmt.Errorf("node register: %w", err)
	}

	if err := s.state.PersistNode(ctx, node.ID, node, leaseID); err != nil {
		s.tracker.UntrackNode(node.ID) // revoke lease on failure
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
// Revoking the lease causes etcd to atomically delete the node state key,
// all route keys, and the liveness key across every CP replica.
func (s *NodeAppService) DeregisterNode(ctx context.Context, nodeID string) error {
	s.tracker.UntrackNode(nodeID) // revokes lease → etcd deletes all attached keys

	s.logger.Info("node deregistered", "nodeID", nodeID)
	return nil
}

// DrainNode initiates graceful node draining.
// Sets the node status to DRAINING and stops the keepalive so the lease TTL
// counts down (grace period). The NodeUpdateService watcher detects the status
// change and broadcasts a single route-removal event to all agents.
func (s *NodeAppService) DrainNode(ctx context.Context, nodeID string) error {
	node, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("drain node: %w", err)
	}

	node.Status = model.NodeStatusDraining

	// Re-persist with the same lease — the key stays attached and will be
	// cleaned up when the lease expires after the grace period.
	leaseID := s.tracker.LeaseID(nodeID)
	if err := s.state.PersistNode(ctx, nodeID, node, leaseID); err != nil {
		return fmt.Errorf("drain node: persist: %w", err)
	}

	// Stop the keepalive — the lease TTL now counts down. This IS the grace
	// period during which existing connections can complete.
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
// Grants a fresh lease, re-attaches the node state key to it.
func (s *NodeAppService) OnStreamReconnect(ctx context.Context, nodeID string) error {
	leaseID, err := s.tracker.TrackNode(nodeID)
	if err != nil {
		return fmt.Errorf("node reconnect: %w", err)
	}

	// Re-persist node state attached to the new lease.
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

	// Update services list
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

	// Re-persist with the existing lease so the key stays attached.
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
