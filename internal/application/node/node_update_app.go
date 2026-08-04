package node

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// NodeUpdateService watches node state changes in the StateStore (via etcd watch)
// and broadcasts incremental ScopeUpdates to all connected agents when a node
// enters the DRAINING state.
type NodeUpdateService struct {
	stateStore  outbound.StateStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger

	drainingNodes sync.Map
}

// NewNodeUpdateService creates a new NodeUpdateService.
func NewNodeUpdateService(
	stateStore outbound.StateStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *NodeUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeUpdateService{
		stateStore:  stateStore,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching node events and broadcasting scope updates.
// It blocks until the context is cancelled or the watch channel is closed.
func (s *NodeUpdateService) Run(ctx context.Context) error {
	if s.stateStore == nil {
		return fmt.Errorf("node update: state store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("node update: scope distributor is nil")
	}

	eventCh, err := s.stateStore.WatchNodes(ctx)
	if err != nil {
		return fmt.Errorf("node update: watch: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-eventCh:
			if !ok {
				return nil
			}
			s.handleEvent(ctx, event)
		}
	}
}

func (s *NodeUpdateService) handleEvent(ctx context.Context, event outbound.NodeStateEvent) {
	switch event.Type {
	case outbound.StateEventPut:
		if event.Node == nil {
			s.logger.Warn("node update: missing node in PUT event", "key", event.Key)
			return
		}

		if event.Node.Status != model.NodeStatusDraining {
			return
		}

		nodeID := event.Node.ID
		s.drainingNodes.Store(nodeID, struct{}{})

		routes, err := s.stateStore.ListTargetRoutesByNode(ctx, nodeID)
		if err != nil {
			s.logger.Warn("node update: list routes for draining node",
				"nodeID", nodeID,
				"error", err,
			)
			return
		}

		var routeModels []model.TargetRoute
		for _, r := range routes {
			routeModels = append(routeModels, *r)
		}

		update := model.ScopeUpdate{
			Type:   model.ScopeUpdateRemove,
			Routes: routeModels,
		}

		if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
			s.logger.Warn("node update: drain broadcast failed",
				"nodeID", nodeID,
				"routes", len(routeModels),
				"error", err,
			)
		} else {
			s.logger.Info("node drain broadcast sent",
				"nodeID", nodeID,
				"routes", len(routeModels),
			)
		}

	case outbound.StateEventDelete:
		if event.Node != nil {
			s.drainingNodes.Delete(event.Node.ID)
		}
	}
}

// IsNodeDraining reports whether the given node is currently in the draining state.
func (s *NodeUpdateService) IsNodeDraining(nodeID string) bool {
	_, ok := s.drainingNodes.Load(nodeID)
	return ok
}
