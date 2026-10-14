package namespace

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

const namespaceResourcePrefix = "core.omnimesh.io/v1/Namespace/"

// NamespaceUpdateService watches Namespace resource changes in the ResourceStore and
// broadcasts incremental ScopeUpdates to all connected agents.
type NamespaceUpdateService struct {
	resources   outbound.ResourceStore
	distributor outbound.ScopeDistributor
	logger      *slog.Logger
}

// NewNamespaceUpdateService creates a new NamespaceUpdateService.
func NewNamespaceUpdateService(
	resources outbound.ResourceStore,
	distributor outbound.ScopeDistributor,
	logger *slog.Logger,
) *NamespaceUpdateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NamespaceUpdateService{
		resources:   resources,
		distributor: distributor,
		logger:      logger,
	}
}

// Run starts watching namespace resource events and broadcasting scope updates.
// It blocks until the context is cancelled or the watch channel is closed.
func (s *NamespaceUpdateService) Run(ctx context.Context) error {
	if s.resources == nil {
		return fmt.Errorf("namespace update: resource store is nil")
	}
	if s.distributor == nil {
		return fmt.Errorf("namespace update: scope distributor is nil")
	}

	eventCh, err := s.resources.Watch(ctx, namespaceResourcePrefix)
	if err != nil {
		return fmt.Errorf("namespace update: watch: %w", err)
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

func (s *NamespaceUpdateService) handleEvent(ctx context.Context, event outbound.ResourceEvent) {
	if event.Resource == nil {
		s.logger.Warn("namespace update: resource missing in event, skipping", "key", event.Key)
		return
	}

	ns, err := mapper.ResourceToNamespace(event.Resource)
	if err != nil {
		s.logger.Warn("namespace update: failed to decode namespace resource", "key", event.Key, "error", err)
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
		Type:       updateType,
		Namespaces: []model.Namespace{*ns},
	}

	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("namespace update: broadcast failed",
			"namespace", ns.Name,
			"updateType", updateType,
			"error", err,
		)
	}
}
