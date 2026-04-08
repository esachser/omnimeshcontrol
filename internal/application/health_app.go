package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// HealthAppService implements the ServiceHealthReporter inbound port.
// It processes health reports from agents, persists them (lease-attached for
// automatic cleanup on node disconnect), and relies on HealthUpdateService
// to broadcast the change to all connected agents via the etcd watch loop.
//
// Separation of concerns:
//   - HealthAppService: validate + persist (write path)
//   - HealthUpdateService: watch etcd + broadcast (fan-out path)
//
// This avoids double-broadcasting on the local CP replica: the etcd watch
// fires on every replica (including the one that wrote), so
// HealthUpdateService handles fanout uniformly across the cluster.
type HealthAppService struct {
	healthStore outbound.EndpointHealthStore
	tracker     service.NodeTracker
	logger      *slog.Logger
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
	}
}

// ReportEndpointHealth processes a health status report from an agent.
// The health key is attached to the reporting node's etcd lease so it is
// automatically removed when the node disconnects.
// Broadcasting is handled by HealthUpdateService via the etcd watch.
func (s *HealthAppService) ReportEndpointHealth(ctx context.Context, report *model.EndpointHealth) error {
	// Look up the reporting node's lease ID so the health key is attached
	// to the same lease as the node's state and route keys.
	var leaseID int64
	if s.tracker != nil && report.NodeID != "" {
		leaseID = s.tracker.LeaseID(report.NodeID)
	}

	if err := s.healthStore.PutEndpointHealth(ctx, report, leaseID); err != nil {
		return fmt.Errorf("health report: %w", err)
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
// Sets the endpoint to DRAINING. Agents are notified via HealthUpdateService
// watching the etcd write.
func (s *HealthAppService) DrainService(ctx context.Context, serviceID model.ServiceID, routeName string) error {
	drainReport := &model.EndpointHealth{
		ServiceID: serviceID,
		RouteName: routeName,
		Status:    model.EndpointHealthDraining,
	}

	// Drain reports are not lease-attached — we want the DRAINING status to
	// persist even if the reporting agent's stream is temporarily interrupted.
	if err := s.healthStore.PutEndpointHealth(ctx, drainReport, 0); err != nil {
		return fmt.Errorf("drain service: %w", err)
	}

	s.logger.Info("service endpoint draining",
		"service", serviceID,
		"route_name", routeName,
	)
	return nil
}

// Compile-time interface check.
var _ inbound.ServiceHealthReporter = (*HealthAppService)(nil)
