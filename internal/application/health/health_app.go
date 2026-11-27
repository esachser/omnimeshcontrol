package health

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// HealthAppService implements the ServiceHealthReporter inbound port.
// It processes health reports from agents, persists them (lease-attached for
// automatic cleanup on node disconnect), and relies on HealthUpdateService
// to broadcast the change to all connected agents via the etcd watch loop.
type HealthAppService struct {
	healthStore outbound.EndpointHealthStore
	tracker     service.NodeTracker
	logger      *slog.Logger
	metrics     outbound.MetricsRecorder
	tracer      outbound.TraceExporter
}

// NewHealthAppService creates a new HealthAppService.
func NewHealthAppService(
	healthStore outbound.EndpointHealthStore,
	tracker service.NodeTracker,
	logger *slog.Logger,
) *HealthAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthAppService{
		healthStore: healthStore,
		tracker:     tracker,
		logger:      logger,
		metrics:     telemetry.NoopMetricsRecorder{},
		tracer:      telemetry.NoopTraceExporter{},
	}
}

// SetTelemetry injects optional metrics and tracing ports.
func (s *HealthAppService) SetTelemetry(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) {
	s.metrics = telemetry.EnsureMetrics(metrics)
	s.tracer = telemetry.EnsureTracer(tracer)
}

// ReportEndpointHealth processes a health status report from an agent.
func (s *HealthAppService) ReportEndpointHealth(ctx context.Context, report *model.EndpointHealth) error {
	ctx, span := s.tracer.StartSpan(ctx, "HealthAppService.ReportEndpointHealth", map[string]string{
		"omnimesh.node":                report.NodeID,
		"omnimesh.destination.service": report.ServiceID.Name,
	})
	defer span.End()

	var leaseID int64
	if s.tracker != nil && report.NodeID != "" {
		leaseID = s.tracker.LeaseID(report.NodeID)
	}

	if err := s.healthStore.PutEndpointHealth(ctx, report, leaseID); err != nil {
		err = fmt.Errorf("health report: %w", err)
		span.SetError(err)
		return err
	}

	s.logger.Debug("endpoint health reported",
		"service", report.ServiceID,
		"route_name", report.RouteName,
		"status", report.Status,
		"nodeID", report.NodeID,
	)
	return nil
}

// DrainService initiates graceful termination for a service endpoint.
func (s *HealthAppService) DrainService(ctx context.Context, serviceID model.ServiceID, routeName string) error {
	ctx, span := s.tracer.StartSpan(ctx, "HealthAppService.DrainService", map[string]string{
		"omnimesh.namespace":           serviceID.Namespace,
		"omnimesh.destination.service": serviceID.Name,
	})
	defer span.End()

	drainReport := &model.EndpointHealth{
		ServiceID: serviceID,
		RouteName: routeName,
		Status:    model.EndpointHealthDraining,
	}

	if err := s.healthStore.PutEndpointHealth(ctx, drainReport, 0); err != nil {
		err = fmt.Errorf("drain service: %w", err)
		span.SetError(err)
		return err
	}

	s.logger.Info("service endpoint draining",
		"service", serviceID,
		"route_name", routeName,
	)
	return nil
}

// Compile-time interface check.
var _ inbound.ServiceHealthReporter = (*HealthAppService)(nil)
