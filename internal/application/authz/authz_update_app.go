package authz

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

const authzPolicyResourcePrefix = "policy.omnimesh.io/v1/AuthorizationPolicy/"

// AuthzPolicyUpdateService watches authorization policy changes in the
// ResourceStore (via etcd watchers) and broadcasts ScopeUpdate messages to all
// connected agents.
type AuthzPolicyUpdateService struct {
	resources   outbound.ResourceStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger
}

// NewAuthzPolicyUpdateService creates a new AuthzPolicyUpdateService.
func NewAuthzPolicyUpdateService(
	resources outbound.ResourceStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *AuthzPolicyUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthzPolicyUpdateService{
		resources:   resources,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching authorization policy resource events and broadcasting
// scope updates. It blocks until the context is cancelled or the watch channel
// is closed.
func (s *AuthzPolicyUpdateService) Run(ctx context.Context) error {
	if s.resources == nil {
		return fmt.Errorf("authz policy update: resource store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("authz policy update: scope distributor is nil")
	}

	eventCh, err := s.resources.Watch(ctx, authzPolicyResourcePrefix)
	if err != nil {
		return fmt.Errorf("authz policy update: watch: %w", err)
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

func (s *AuthzPolicyUpdateService) handleEvent(ctx context.Context, event outbound.ResourceEvent) {
	if event.Resource == nil {
		s.logger.Warn("authz policy update: resource missing in event, skipping", "key", event.Key)
		return
	}

	policy, err := ResourceToAuthzPolicy(event.Resource)
	if err != nil {
		s.logger.Warn("authz policy update: failed to decode authz policy resource",
			"key", event.Key, "error", err)
		return
	}

	var updateType model.ScopeUpdateType
	switch event.Type {
	case outbound.ResourceEventDelete:
		updateType = model.ScopeUpdateRemove
	default:
		updateType = model.ScopeUpdateModify
	}

	update := model.ScopeUpdate{
		Type:          updateType,
		AuthzPolicies: []model.AuthorizationPolicy{*policy},
	}

	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("authz policy update: broadcast failed",
			"policy", policy.Name,
			"namespace", policy.Namespace,
			"updateType", updateType,
			"error", err,
		)
	}
}
