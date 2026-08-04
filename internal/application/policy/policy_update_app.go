package policy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// TrafficPolicyUpdateService watches traffic policy changes and broadcasts
// scope updates to connected agents.
type TrafficPolicyUpdateService struct {
	policies    outbound.TrafficPolicyStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger
}

// NewTrafficPolicyUpdateService creates a new TrafficPolicyUpdateService.
func NewTrafficPolicyUpdateService(
	policies outbound.TrafficPolicyStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *TrafficPolicyUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TrafficPolicyUpdateService{
		policies:    policies,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching policy events and broadcasting scope updates.
func (s *TrafficPolicyUpdateService) Run(ctx context.Context) error {
	if s.policies == nil {
		return fmt.Errorf("traffic policy update: policy store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("traffic policy update: scope distributor is nil")
	}

	eventCh, err := s.policies.WatchTrafficPolicies(ctx)
	if err != nil {
		return fmt.Errorf("traffic policy update: watch: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-eventCh:
			if !ok {
				return nil
			}
			if event.Policy == nil {
				s.logger.Warn("traffic policy update: missing policy in event", "eventType", event.Type)
				continue
			}

			updateType := model.ScopeUpdateModify
			if event.Type == outbound.TrafficPolicyEventDelete {
				updateType = model.ScopeUpdateRemove
			}

			update := model.ScopeUpdate{
				Type:            updateType,
				TrafficPolicies: []model.TrafficPolicy{*event.Policy},
			}

			if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
				s.logger.Warn("traffic policy update: broadcast failed", "error", err)
			}
		}
	}
}
