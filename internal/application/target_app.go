package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// TargetAppService implements the TargetManager inbound port.
type TargetAppService struct {
	resources outbound.ResourceStore
	logger    *slog.Logger
}

// NewTargetAppService creates a new TargetAppService.
func NewTargetAppService(resources outbound.ResourceStore, logger *slog.Logger) *TargetAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TargetAppService{
		resources: resources,
		logger:    logger,
	}
}

// Apply creates or updates a network target.
func (s *TargetAppService) Apply(ctx context.Context, target *model.NetworkTarget) error {
	if target.Name == "" {
		return fmt.Errorf("target apply: name is required")
	}

	key := "core.omnimesh.io/v1/NetworkTarget/" + target.Name

	if _, err := s.resources.Put(ctx, key, targetToResource(target)); err != nil {
		return fmt.Errorf("target apply: %w", err)
	}

	s.logger.Info("network target applied", "name", target.Name, "gatewayAddress", target.GatewayAddress)
	return nil
}

// Get retrieves a network target by name.
func (s *TargetAppService) Get(ctx context.Context, name string) (*model.NetworkTarget, error) {
	key := "core.omnimesh.io/v1/NetworkTarget/" + name
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("target get: %w", err)
	}
	return resourceToTarget(resource)
}

// List returns all network targets.
func (s *TargetAppService) List(ctx context.Context) ([]*model.NetworkTarget, error) {
	resources, err := s.resources.List(ctx, "core.omnimesh.io/v1/NetworkTarget/")
	if err != nil {
		return nil, fmt.Errorf("target list: %w", err)
	}

	targets := make([]*model.NetworkTarget, 0, len(resources))
	for _, r := range resources {
		t, err := resourceToTarget(r)
		if err != nil {
			s.logger.Warn("skipping invalid target resource", "error", err)
			continue
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// Delete removes a network target.
func (s *TargetAppService) Delete(ctx context.Context, name string) error {
	key := "core.omnimesh.io/v1/NetworkTarget/" + name
	if err := s.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("target delete: %w", err)
	}

	s.logger.Info("network target deleted", "name", name)
	return nil
}

// Compile-time interface check.
var _ inbound.TargetManager = (*TargetAppService)(nil)
