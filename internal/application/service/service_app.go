package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// ServiceAppService implements the ServiceManager inbound port.
// It orchestrates: validate → allocate IP → register identity → persist → DNS.
type ServiceAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	dns         outbound.DNSRecordManager
	identity    outbound.IdentityManager
	namespaces  inbound.NamespaceManager
	dnsZone     string
	trustDomain string
	logger      *slog.Logger
	metrics     outbound.MetricsRecorder
	tracer      outbound.TraceExporter
}

// ServiceAppServiceConfig holds dependencies for ServiceAppService.
type ServiceAppServiceConfig struct {
	Resources  outbound.ResourceStore
	State      outbound.StateStore
	DNS        outbound.DNSRecordManager
	Identity   outbound.IdentityManager
	Namespaces inbound.NamespaceManager
	DNSZone    string
	// TrustDomain is the SPIFFE trust domain used when building workload SPIFFE IDs.
	// Defaults to DNSZone when empty.
	TrustDomain string
	Logger      *slog.Logger
	Metrics     outbound.MetricsRecorder
	Tracer      outbound.TraceExporter
}

// NewServiceAppService creates a new ServiceAppService.
func NewServiceAppService(cfg ServiceAppServiceConfig) *ServiceAppService {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	trustDomain := cfg.TrustDomain
	if trustDomain == "" {
		trustDomain = cfg.DNSZone
	}
	return &ServiceAppService{
		resources:   cfg.Resources,
		state:       cfg.State,
		dns:         cfg.DNS,
		identity:    cfg.Identity,
		namespaces:  cfg.Namespaces,
		dnsZone:     cfg.DNSZone,
		trustDomain: trustDomain,
		logger:      cfg.Logger,
		metrics:     telemetry.EnsureMetrics(cfg.Metrics),
		tracer:      telemetry.EnsureTracer(cfg.Tracer),
	}
}

// SetTelemetry injects optional metrics and tracing ports.
func (s *ServiceAppService) SetTelemetry(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) {
	s.metrics = telemetry.EnsureMetrics(metrics)
	s.tracer = telemetry.EnsureTracer(tracer)
}

// Apply creates or updates a service.
func (s *ServiceAppService) Apply(ctx context.Context, service *model.Service) error {
	ctx, span := s.tracer.StartSpan(ctx, "ServiceAppService.Apply", map[string]string{
		"omnimesh.namespace": service.ID.Namespace,
		"omnimesh.service":   service.ID.Name,
	})
	defer span.End()

	if service.ID.Name == "" {
		err := fmt.Errorf("service apply: %w: name is required", domain.ErrInvalidName)
		span.SetError(err)
		return err
	}
	if service.ID.Namespace == "" {
		err := fmt.Errorf("service apply: %w: namespace is required", domain.ErrInvalidNamespace)
		span.SetError(err)
		return err
	}
	if err := service.ValidateSelectors(); err != nil {
		err = fmt.Errorf("service apply: %w: %w", domain.ErrInvalidSelectors, err)
		span.SetError(err)
		return err
	}

	// Get namespace (validates existence + provides CIDR for IP allocation).
	ns, err := s.namespaces.Get(ctx, service.ID.Namespace)
	if err != nil {
		err = fmt.Errorf("service apply: namespace %q: %w", service.ID.Namespace, domain.ErrNamespaceNotFound)
		span.SetError(err)
		return err
	}

	// Allocate mesh IP (idempotent — check if already allocated).
	existing, err := s.state.GetIPAllocation(ctx, service.ID)
	if err != nil {
		// No allocation yet: allocate a new one using namespace's CIDR.
		addr, allocErr := s.state.AllocateIP(ctx, service.ID, ns.CIDR)
		if allocErr != nil {
			err = fmt.Errorf("service apply: %w", allocErr)
			span.SetError(err)
			return err
		}
		service.MeshAddr = addr
	} else {
		service.MeshAddr = existing
	}

	// Register SPIFFE identity.
	if s.identity != nil {
		spiffeID := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", s.trustDomain, service.ID.Namespace, service.ID.Name)
		identity := model.ServiceIdentity{
			SPIFFEID:     spiffeID,
			ServiceID:    service.ID,
			Selectors:    service.Selectors,
			SelectorSets: service.SelectorSets,
		}
		regStart := time.Now()
		regErr := s.identity.RegisterWorkload(ctx, identity)
		s.metrics.RecordHistogram(ctx, model.MetricCPIdentityRegistrationsDurationMs, float64(time.Since(regStart).Microseconds())/1000.0, nil)
		regResult := "success"
		if regErr != nil {
			regResult = "error"
		}
		s.metrics.IncrCounter(ctx, model.MetricCPIdentityRegistrationsTotal, map[string]string{
			"namespace": service.ID.Namespace,
			"result":    regResult,
		})
		if regErr != nil {
			err = fmt.Errorf("service apply: register identity: %w", regErr)
			span.SetError(err)
			return err
		}
	}

	// Persist service resource.
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", service.ID.Namespace, service.ID.Name)
	if _, err := s.resources.Put(ctx, svcKey, mapper.ServiceToResource(service)); err != nil {
		err = fmt.Errorf("service apply: persist: %w", err)
		span.SetError(err)
		return err
	}

	// Update DNS.
	fqdn := service.ID.Name + "." + service.ID.Namespace + "." + s.dnsZone
	dnsStart := time.Now()
	dnsErr := s.dns.UpsertRecord(ctx, fqdn, service.MeshAddr)
	s.metrics.RecordHistogram(ctx, model.MetricCPDNSUpsertsDurationMs, float64(time.Since(dnsStart).Microseconds())/1000.0, nil)
	dnsResult := "success"
	if dnsErr != nil {
		dnsResult = "error"
	}
	s.metrics.IncrCounter(ctx, model.MetricCPDNSUpsertsTotal, map[string]string{
		"result": dnsResult,
	})
	if dnsErr != nil {
		err = fmt.Errorf("service apply: dns: %w", dnsErr)
		span.SetError(err)
		return err
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
	ctx, span := s.tracer.StartSpan(ctx, "ServiceAppService.Get", map[string]string{
		"omnimesh.namespace": namespace,
		"omnimesh.service":   name,
	})
	defer span.End()

	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", namespace, name)
	resource, err := s.resources.Get(ctx, svcKey)
	if err != nil {
		err = fmt.Errorf("service get: %w", domain.ErrServiceNotFound)
		span.SetError(err)
		return nil, err
	}

	svc, err := mapper.ResourceToService(resource)
	if err != nil {
		err = fmt.Errorf("service get: %w", err)
		span.SetError(err)
		return nil, err
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
	ctx, span := s.tracer.StartSpan(ctx, "ServiceAppService.List", map[string]string{
		"omnimesh.namespace": namespace,
	})
	defer span.End()

	prefix := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/", namespace)
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		err = fmt.Errorf("service list: %w", err)
		span.SetError(err)
		return nil, err
	}

	services := make([]*model.Service, 0, len(resources))
	for _, r := range resources {
		svc, err := mapper.ResourceToService(r)
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
	ctx, span := s.tracer.StartSpan(ctx, "ServiceAppService.Delete", map[string]string{
		"omnimesh.namespace": namespace,
		"omnimesh.service":   name,
	})
	defer span.End()

	svcID := model.ServiceID{Namespace: namespace, Name: name}

	// Remove DNS record.
	fqdn := name + "." + namespace + "." + s.dnsZone
	dnsErr := s.dns.DeleteRecord(ctx, fqdn)
	dnsResult := "success"
	if dnsErr != nil {
		dnsResult = "error"
		s.logger.Warn("failed to delete DNS record during delete", "error", dnsErr)
	}
	s.metrics.IncrCounter(ctx, model.MetricCPDNSDeletesTotal, map[string]string{
		"result": dnsResult,
	})

	// Deregister identity.
	if s.identity != nil {
		idErr := s.identity.DeregisterWorkload(ctx, svcID)
		idResult := "success"
		if idErr != nil {
			idResult = "error"
			s.logger.Warn("failed to deregister identity during delete", "error", idErr)
		}
		s.metrics.IncrCounter(ctx, model.MetricCPIdentityDeregistrationsTotal, map[string]string{
			"namespace": namespace,
			"result":    idResult,
		})
	}

	// Release IP.
	if err := s.state.ReleaseIP(ctx, svcID); err != nil {
		s.logger.Warn("failed to release IP during delete", "error", err)
	}

	// Delete resource.
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", namespace, name)
	if err := s.resources.Delete(ctx, svcKey); err != nil {
		err = fmt.Errorf("service delete: %w", err)
		span.SetError(err)
		return err
	}

	s.logger.Info("service deleted", "namespace", namespace, "name", name)
	return nil
}

// Compile-time interface check.
var _ inbound.ServiceManager = (*ServiceAppService)(nil)
