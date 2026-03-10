package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// NodeAppService implements the NodeRegistrar inbound port.
// It coordinates node registration with persistent storage (StateStore)
// and in-memory liveness tracking (NodeLivenessTracker).
type NodeAppService struct {
	state   outbound.StateStore
	tracker NodeTracker
	logger  *slog.Logger
}

// NodeTracker is the interface for the in-memory liveness tracker.
// This decouples the application service from the concrete adapter implementation.
type NodeTracker interface {
	TrackNode(nodeID string)
	OnDisconnect(nodeID string)
	UntrackNode(nodeID string)
	IsAlive(nodeID string) bool
}

// NewNodeAppService creates a new NodeAppService.
func NewNodeAppService(state outbound.StateStore, tracker NodeTracker, logger *slog.Logger) *NodeAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeAppService{
		state:   state,
		tracker: tracker,
		logger:  logger,
	}
}

// RegisterNode registers a new agent node.
func (s *NodeAppService) RegisterNode(ctx context.Context, node *model.Node) error {
	if node.ID == "" {
		return fmt.Errorf("node register: %w: node ID is required", domain.ErrInvalidName)
	}

	node.Status = model.NodeStatusActive

	if err := s.state.PersistNode(ctx, node.ID, node); err != nil {
		return fmt.Errorf("node register: %w", err)
	}

	s.tracker.TrackNode(node.ID)

	s.logger.Info("node registered",
		"nodeID", node.ID,
		"platform", node.Platform,
		"addr", node.Addr,
	)
	return nil
}

// DeregisterNode gracefully deregisters a node.
func (s *NodeAppService) DeregisterNode(ctx context.Context, nodeID string) error {
	s.tracker.UntrackNode(nodeID)

	if err := s.state.DeregisterNode(ctx, nodeID); err != nil {
		return fmt.Errorf("node deregister: %w", err)
	}

	s.logger.Info("node deregistered", "nodeID", nodeID)
	return nil
}

// OnStreamDisconnect handles an unexpected agent stream disconnection.
func (s *NodeAppService) OnStreamDisconnect(_ context.Context, nodeID string) error {
	s.tracker.OnDisconnect(nodeID)
	s.logger.Info("node stream disconnected, grace period started", "nodeID", nodeID)
	return nil
}

// OnStreamReconnect handles an agent reconnecting within the grace period.
func (s *NodeAppService) OnStreamReconnect(_ context.Context, nodeID string) error {
	s.tracker.TrackNode(nodeID)
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

	if err := s.state.PersistNode(ctx, node.ID, node); err != nil {
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
