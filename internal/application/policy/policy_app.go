package policy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// TrafficPolicyAppService implements the TrafficPolicyManager inbound port.
type TrafficPolicyAppService struct {
	policyStore outbound.TrafficPolicyStore
	resources   outbound.ResourceStore
	validator   *domainservice.PolicyValidator
	logger      *slog.Logger
	metrics     outbound.MetricsRecorder
	tracer      outbound.TraceExporter
}

// NewTrafficPolicyAppService creates a new TrafficPolicyAppService.
func NewTrafficPolicyAppService(
	policyStore outbound.TrafficPolicyStore,
	resources outbound.ResourceStore,
	validator *domainservice.PolicyValidator,
	logger *slog.Logger,
) *TrafficPolicyAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TrafficPolicyAppService{
		policyStore: policyStore,
		resources:   resources,
		validator:   validator,
		logger:      logger,
		metrics:     telemetry.NoopMetricsRecorder{},
		tracer:      telemetry.NoopTraceExporter{},
	}
}

// SetTelemetry injects optional metrics and tracing ports.
func (s *TrafficPolicyAppService) SetTelemetry(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) {
	s.metrics = telemetry.EnsureMetrics(metrics)
	s.tracer = telemetry.EnsureTracer(tracer)
}

// CreateTrafficPolicy creates a new traffic policy.
func (s *TrafficPolicyAppService) CreateTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error {
	ctx, span := s.tracer.StartSpan(ctx, "TrafficPolicyAppService.Create", map[string]string{
		"omnimesh.namespace":   policy.Namespace,
		"omnimesh.policy.name": policy.Name,
	})
	defer span.End()

	if policy.Name == "" {
		err := fmt.Errorf("policy create: %w: name is required", domain.ErrInvalidName)
		span.SetError(err)
		return err
	}

	// Check target service exists and get its protocol for validation.
	svc, err := s.getTargetService(ctx, policy.TargetService)
	if err != nil {
		err = fmt.Errorf("policy create: %w", err)
		span.SetError(err)
		return err
	}

	// Validate policy against service protocol.
	if err := s.validator.Validate(policy, svc.Protocol); err != nil {
		err = fmt.Errorf("policy create: %w", err)
		span.SetError(err)
		return err
	}

	// Check for existing policy on same service.
	existing, _ := s.policyStore.GetPolicyByService(ctx, policy.TargetService)
	if existing != nil {
		err := fmt.Errorf("policy create: %w", domain.ErrPolicyConflict)
		span.SetError(err)
		return err
	}

	if err := s.policyStore.PutTrafficPolicy(ctx, policy); err != nil {
		err = fmt.Errorf("policy create: %w", err)
		span.SetError(err)
		return err
	}

	s.logger.Info("traffic policy created",
		"name", policy.Name,
		"namespace", policy.Namespace,
		"target", policy.TargetService,
	)
	return nil
}

// UpdateTrafficPolicy updates an existing traffic policy.
func (s *TrafficPolicyAppService) UpdateTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error {
	ctx, span := s.tracer.StartSpan(ctx, "TrafficPolicyAppService.Update", map[string]string{
		"omnimesh.namespace":   policy.Namespace,
		"omnimesh.policy.name": policy.Name,
	})
	defer span.End()

	svc, err := s.getTargetService(ctx, policy.TargetService)
	if err != nil {
		err = fmt.Errorf("policy update: %w", err)
		span.SetError(err)
		return err
	}

	if err := s.validator.Validate(policy, svc.Protocol); err != nil {
		err = fmt.Errorf("policy update: %w", err)
		span.SetError(err)
		return err
	}

	if err := s.policyStore.PutTrafficPolicy(ctx, policy); err != nil {
		err = fmt.Errorf("policy update: %w", err)
		span.SetError(err)
		return err
	}

	s.logger.Info("traffic policy updated", "name", policy.Name, "namespace", policy.Namespace)
	return nil
}

// GetTrafficPolicy retrieves a traffic policy by namespace and name.
func (s *TrafficPolicyAppService) GetTrafficPolicy(ctx context.Context, namespace, name string) (*model.TrafficPolicy, error) {
	policy, err := s.policyStore.GetTrafficPolicy(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("policy get: %w", domain.ErrPolicyNotFound)
	}
	return policy, nil
}

// DeleteTrafficPolicy removes a traffic policy.
func (s *TrafficPolicyAppService) DeleteTrafficPolicy(ctx context.Context, namespace, name string) error {
	ctx, span := s.tracer.StartSpan(ctx, "TrafficPolicyAppService.Delete", map[string]string{
		"omnimesh.namespace":   namespace,
		"omnimesh.policy.name": name,
	})
	defer span.End()

	if err := s.policyStore.DeleteTrafficPolicy(ctx, namespace, name); err != nil {
		err = fmt.Errorf("policy delete: %w", err)
		span.SetError(err)
		return err
	}
	s.logger.Info("traffic policy deleted", "name", name, "namespace", namespace)
	return nil
}

// ListTrafficPolicies returns all traffic policies in a namespace.
func (s *TrafficPolicyAppService) ListTrafficPolicies(ctx context.Context, namespace string) ([]*model.TrafficPolicy, error) {
	policies, err := s.policyStore.ListTrafficPolicies(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("policy list: %w", err)
	}
	return policies, nil
}

// GetPolicyForService returns the traffic policy attached to a specific service.
func (s *TrafficPolicyAppService) GetPolicyForService(ctx context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error) {
	policy, err := s.policyStore.GetPolicyByService(ctx, serviceID)
	if err != nil {
		return nil, fmt.Errorf("policy for service: %w", domain.ErrPolicyNotFound)
	}
	return policy, nil
}

func (s *TrafficPolicyAppService) getTargetService(ctx context.Context, id model.ServiceID) (*model.Service, error) {
	svcKey := fmt.Sprintf("core.omnimesh.io/v1/Service/%s/%s", id.Namespace, id.Name)
	resource, err := s.resources.Get(ctx, svcKey)
	if err != nil {
		return nil, fmt.Errorf("target service %q: %w", id, domain.ErrServiceNotFound)
	}
	return mapper.ResourceToService(resource)
}

// Compile-time interface check.
var _ inbound.TrafficPolicyManager = (*TrafficPolicyAppService)(nil)
