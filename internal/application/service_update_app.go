package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

const serviceResourcePrefix = "core.omnimesh.io/v1/Service/"

// ServiceUpdateService watches Service resource changes in the ResourceStore and
// broadcasts incremental ScopeUpdates to all connected agents.
//
// This mirrors TrafficPolicyUpdateService: every control-plane replica watches the
// same etcd prefix, so service create/update/delete events are reflected on every
// replica and each one notifies its own locally-connected agents. This is correct
// for a clustered control plane.
type ServiceUpdateService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger
}

// NewServiceUpdateService creates a new ServiceUpdateService.
func NewServiceUpdateService(
	resources outbound.ResourceStore,
	state outbound.StateStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *ServiceUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ServiceUpdateService{
		resources:   resources,
		state:       state,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching service resource events and broadcasting scope updates.
// It blocks until the context is cancelled or the watch channel is closed.
func (s *ServiceUpdateService) Run(ctx context.Context) error {
	if s.resources == nil {
		return fmt.Errorf("service update: resource store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("service update: scope distributor is nil")
	}

	eventCh, err := s.resources.Watch(ctx, serviceResourcePrefix)
	if err != nil {
		return fmt.Errorf("service update: watch: %w", err)
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

func (s *ServiceUpdateService) handleEvent(ctx context.Context, event outbound.ResourceEvent) {
	if event.Resource == nil {
		// This can happen for a delete event when PrevKV is unavailable.
		// We cannot reconstruct the service, so we skip. Agents that
		// reconnect will get the correct scope via GetAgentScope.
		s.logger.Warn("service update: resource missing in event, skipping", "key", event.Key)
		return
	}

	svc, err := resourceToService(event.Resource)
	if err != nil {
		s.logger.Warn("service update: failed to decode service resource", "key", event.Key, "error", err)
		return
	}

	var updateType model.ScopeUpdateType
	switch event.Type {
	case outbound.ResourceEventDelete:
		updateType = model.ScopeUpdateRemove
	default:
		updateType = model.ScopeUpdateModify

		// Populate the mesh address for PUT events so agents receive a fully
		// hydrated service model (same as what GetAgentScope returns).
		if s.state != nil {
			if addr, err := s.state.GetIPAllocation(ctx, svc.ID); err == nil {
				svc.MeshAddr = addr
			}
		}
	}

	update := model.ScopeUpdate{
		Type:     updateType,
		Services: []model.Service{*svc},
	}

	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("service update: broadcast failed",
			"service", svc.ID,
			"updateType", updateType,
			"error", err,
		)
	}
}
