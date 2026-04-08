package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// HealthUpdateService watches endpoint health changes in the EndpointHealthStore
// (via etcd watch) and broadcasts incremental ScopeUpdates to all connected agents.
//
// In a clustered control plane, every replica watches /omnimesh/state/health/*
// and each one notifies its own locally-connected agents. This ensures all agents
// across all CP replicas receive health updates regardless of which replica
// processed the original health report.
//
// This mirrors the pattern of TrafficPolicyUpdateService and ServiceUpdateService.
type HealthUpdateService struct {
	healthStore outbound.EndpointHealthStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger
}

// NewHealthUpdateService creates a new HealthUpdateService.
func NewHealthUpdateService(
	healthStore outbound.EndpointHealthStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *HealthUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthUpdateService{
		healthStore: healthStore,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching health events and broadcasting scope updates.
// It blocks until the context is cancelled or the watch channel is closed.
func (s *HealthUpdateService) Run(ctx context.Context) error {
	if s.healthStore == nil {
		return fmt.Errorf("health update: health store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("health update: scope distributor is nil")
	}

	eventCh, err := s.healthStore.WatchEndpointHealth(ctx)
	if err != nil {
		return fmt.Errorf("health update: watch: %w", err)
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

func (s *HealthUpdateService) handleEvent(ctx context.Context, event outbound.EndpointHealthEvent) {
	if event.Health == nil {
		s.logger.Warn("health update: missing health in event", "eventType", event.Type)
		return
	}

	updateType := model.ScopeUpdateModify
	if event.Type == outbound.EndpointHealthEventDelete {
		updateType = model.ScopeUpdateRemove
	}

	update := model.ScopeUpdate{
		Type:           updateType,
		EndpointHealth: []model.EndpointHealth{*event.Health},
	}

	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("health update: broadcast failed",
			"service", event.Health.ServiceID,
			"route_name", event.Health.RouteName,
			"error", err,
		)
	}
}
