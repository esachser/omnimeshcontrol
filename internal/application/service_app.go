// Package application contains the application services that orchestrate
// domain logic via port interfaces. These services implement the inbound ports.
package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// ServiceAppService implements the ServiceManager inbound port.
// It orchestrates: validate → allocate IP → register identity → persist → DNS → route.
type ServiceAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	dns         outbound.DNSRecordManager
	routes      outbound.RouteDistributor
	identity    outbound.IdentityManager
	ipAllocator *domainservice.IPAllocator
	dnsZone     string
	logger      *slog.Logger
}

// ServiceAppServiceConfig holds dependencies for ServiceAppService.
type ServiceAppServiceConfig struct {
	Resources   outbound.ResourceStore
	State       outbound.StateStore
	DNS         outbound.DNSRecordManager
	Routes      outbound.RouteDistributor
	Identity    outbound.IdentityManager
	IPAllocator *domainservice.IPAllocator
	DNSZone     string
	Logger      *slog.Logger
}

// NewServiceAppService creates a new ServiceAppService.
func NewServiceAppService(cfg ServiceAppServiceConfig) *ServiceAppService {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ServiceAppService{
		resources:   cfg.Resources,
		state:       cfg.State,
		dns:         cfg.DNS,
		routes:      cfg.Routes,
		identity:    cfg.Identity,
		ipAllocator: cfg.IPAllocator,
		dnsZone:     cfg.DNSZone,
		logger:      cfg.Logger,
	}
}

// Apply creates or updates a service.
func (s *ServiceAppService) Apply(ctx context.Context, service *model.Service) error {
	if service.ID.Name == "" {
		return fmt.Errorf("service apply: %w: name is required", domain.ErrInvalidName)
	}
	if service.ID.Namespace == "" {
		return fmt.Errorf("service apply: %w: namespace is required", domain.ErrInvalidNamespace)
	}

	// Check namespace exists.
	nsKey := "core.omnimesh.io/v1/Namespace/" + service.ID.Namespace
	if _, err := s.resources.Get(ctx, nsKey); err != nil {
		return fmt.Errorf("service apply: namespace %q: %w", service.ID.Namespace, domain.ErrNamespaceNotFound)
	}

	// Allocate mesh IP (idempotent — check if already allocated).
	existing, err := s.state.GetIPAllocation(ctx, service.ID)
	if err != nil {
		// No allocation yet: allocate a new one.
		addr, allocErr := s.state.AllocateIP(ctx, service.ID, s.ipAllocator.CIDR())
		if allocErr != nil {
			return fmt.Errorf("service apply: %w", allocErr)
		}
		service.MeshAddr = addr
	} else {
		service.MeshAddr = existing
	}

	// Register SPIFFE identity.
	if s.identity != nil {
		spiffeID := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", s.dnsZone, service.ID.Namespace, service.ID.Name)
		identity := model.ServiceIdentity{
			SPIFFEID:  spiffeID,
			ServiceID: service.ID,
		}
		if err := s.identity.RegisterWorkload(ctx, identity); err != nil {
			return fmt.Errorf("service apply: register identity: %w", err)
		}
	}

	// Persist service resource.
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", service.ID.Namespace, service.ID.Name)
	if _, err := s.resources.Put(ctx, svcKey, serviceToResource(service)); err != nil {
		return fmt.Errorf("service apply: persist: %w", err)
	}

	// Update DNS.
	fqdn := service.ID.Name + "." + service.ID.Namespace + "." + s.dnsZone
	if err := s.dns.UpsertRecord(ctx, fqdn, service.MeshAddr); err != nil {
		return fmt.Errorf("service apply: dns: %w", err)
	}

	// Announce route (with empty TargetNodes for now, will be populated when agents report).
	route := model.MeshRoute{
		ServiceID: service.ID,
		MeshAddr:  service.MeshAddr,
	}
	if err := s.routes.AnnounceRoute(ctx, route); err != nil {
		return fmt.Errorf("service apply: announce route: %w", err)
	}

	s.logger.Info("service applied",
		"namespace", service.ID.Namespace,
		"name", service.ID.Name,
		"meshAddr", service.MeshAddr,
	)
	return nil
}

// Get retrieves a service by namespace and name.
func (s *ServiceAppService) Get(ctx context.Context, namespace, name string) (*model.Service, error) {
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", namespace, name)
	resource, err := s.resources.Get(ctx, svcKey)
	if err != nil {
		return nil, fmt.Errorf("service get: %w", domain.ErrServiceNotFound)
	}

	svc, err := resourceToService(resource)
	if err != nil {
		return nil, fmt.Errorf("service get: %w", err)
	}

	// Populate mesh address from state store.
	addr, addrErr := s.state.GetIPAllocation(ctx, svc.ID)
	if addrErr == nil {
		svc.MeshAddr = addr
	}

	return svc, nil
}

// List returns all services in a namespace.
func (s *ServiceAppService) List(ctx context.Context, namespace string) ([]*model.Service, error) {
	prefix := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/", namespace)
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("service list: %w", err)
	}

	services := make([]*model.Service, 0, len(resources))
	for _, r := range resources {
		svc, err := resourceToService(r)
		if err != nil {
			s.logger.Warn("skipping invalid service resource", "error", err)
			continue
		}
		addr, addrErr := s.state.GetIPAllocation(ctx, svc.ID)
		if addrErr == nil {
			svc.MeshAddr = addr
		}
		services = append(services, svc)
	}
	return services, nil
}

// Delete removes a service.
func (s *ServiceAppService) Delete(ctx context.Context, namespace, name string) error {
	svcID := model.ServiceID{Namespace: namespace, Name: name}

	// Get mesh address for DNS/route cleanup.
	addr, _ := s.state.GetIPAllocation(ctx, svcID)

	// Withdraw route.
	if addr.IsValid() {
		route := model.MeshRoute{ServiceID: svcID, MeshAddr: addr}
		if err := s.routes.WithdrawRoute(ctx, route); err != nil {
			s.logger.Warn("failed to withdraw route during delete", "error", err)
		}
	}

	// Remove DNS record.
	fqdn := name + "." + namespace + "." + s.dnsZone
	if err := s.dns.DeleteRecord(ctx, fqdn); err != nil {
		s.logger.Warn("failed to delete DNS record during delete", "error", err)
	}

	// Deregister identity.
	if s.identity != nil {
		if err := s.identity.DeregisterWorkload(ctx, svcID); err != nil {
			s.logger.Warn("failed to deregister identity during delete", "error", err)
		}
	}

	// Release IP.
	if err := s.state.ReleaseIP(ctx, svcID); err != nil {
		s.logger.Warn("failed to release IP during delete", "error", err)
	}

	// Delete resource.
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", namespace, name)
	if err := s.resources.Delete(ctx, svcKey); err != nil {
		return fmt.Errorf("service delete: %w", err)
	}

	s.logger.Info("service deleted", "namespace", namespace, "name", name)
	return nil
}

// Compile-time interface check.
var _ inbound.ServiceManager = (*ServiceAppService)(nil)
