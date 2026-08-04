package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// NamespaceAppService implements the NamespaceManager inbound port.
type NamespaceAppService struct {
	resources   outbound.ResourceStore
	defaultCIDR netip.Prefix
	logger      *slog.Logger
}

// NewNamespaceAppService creates a new NamespaceAppService.
// defaultCIDR is used when a namespace is created without an explicit CIDR.
func NewNamespaceAppService(resources outbound.ResourceStore, defaultCIDR netip.Prefix, logger *slog.Logger) *NamespaceAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NamespaceAppService{
		resources:   resources,
		defaultCIDR: defaultCIDR,
		logger:      logger,
	}
}

// Apply creates or updates a namespace.
func (s *NamespaceAppService) Apply(ctx context.Context, namespace *model.Namespace) error {
	if namespace.Name == "" {
		return fmt.Errorf("namespace apply: %w: name is required", domain.ErrInvalidName)
	}

	key := "core.omnimesh.io/v1/Namespace/" + namespace.Name

	// Check if namespace already exists (for CIDR immutability).
	existingResource, err := s.resources.Get(ctx, key)
	if err == nil {
		// Namespace exists — enforce CIDR immutability.
		existing, mapErr := mapper.ResourceToNamespace(existingResource)
		if mapErr != nil {
			return fmt.Errorf("namespace apply: %w", mapErr)
		}

		if existing.CIDR.IsValid() && namespace.CIDR.IsValid() && existing.CIDR != namespace.CIDR {
			return fmt.Errorf("namespace apply %q: CIDR cannot be changed after creation (current: %s, requested: %s)",
				namespace.Name, existing.CIDR, namespace.CIDR)
		}
		// Preserve existing CIDR on update if not specified.
		if !namespace.CIDR.IsValid() {
			namespace.CIDR = existing.CIDR
		}
	} else {
		// New namespace — assign default CIDR if not specified.
		if !namespace.CIDR.IsValid() {
			namespace.CIDR = s.defaultCIDR
		}
	}

	// Validate CIDR.
	if !namespace.CIDR.IsValid() {
		return fmt.Errorf("namespace apply %q: %w: CIDR is required", namespace.Name, domain.ErrInvalidCIDR)
	}

	if _, err := s.resources.Put(ctx, key, mapper.NamespaceToResource(namespace)); err != nil {
		return fmt.Errorf("namespace apply: %w", err)
	}

	s.logger.Info("namespace applied",
		"name", namespace.Name,
		"isolation", namespace.Isolation,
		"cidr", namespace.CIDR,
	)
	return nil
}

// Get retrieves a namespace by name.
func (s *NamespaceAppService) Get(ctx context.Context, name string) (*model.Namespace, error) {
	key := "core.omnimesh.io/v1/Namespace/" + name
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("namespace get: %w", domain.ErrNamespaceNotFound)
	}
	return mapper.ResourceToNamespace(resource)
}

// List returns all namespaces.
func (s *NamespaceAppService) List(ctx context.Context) ([]*model.Namespace, error) {
	resources, err := s.resources.List(ctx, "core.omnimesh.io/v1/Namespace/")
	if err != nil {
		return nil, fmt.Errorf("namespace list: %w", err)
	}

	namespaces := make([]*model.Namespace, 0, len(resources))
	for _, r := range resources {
		ns, err := mapper.ResourceToNamespace(r)
		if err != nil {
			s.logger.Warn("skipping invalid namespace resource", "error", err)
			continue
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

// Delete removes a namespace.
func (s *NamespaceAppService) Delete(ctx context.Context, name string, force bool) error {
	if !force {
		// Check namespace is empty (no services).
		svcPrefix := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/", name)
		services, err := s.resources.List(ctx, svcPrefix)
		if err != nil {
			return fmt.Errorf("namespace delete: %w", err)
		}
		if len(services) > 0 {
			return fmt.Errorf("namespace delete %q: %w (contains %d service(s))",
				name, domain.ErrNamespaceNotEmpty, len(services))
		}
	}

	key := "core.omnimesh.io/v1/Namespace/" + name
	if err := s.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("namespace delete: %w", err)
	}

	s.logger.Info("namespace deleted", "name", name, "force", force)
	return nil
}

// Compile-time interface check.
var _ inbound.NamespaceManager = (*NamespaceAppService)(nil)
