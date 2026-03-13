package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// HealthAppService implements the ServiceHealthReporter inbound port.
// It processes health reports from agents and persists them to the health store.
// Route aggregation is now performed lazily by scope_app when building agent scope.
type HealthAppService struct {
	healthStore outbound.EndpointHealthStore
	logger      *slog.Logger
}

// NewHealthAppService creates a new HealthAppService.
// stateStore and routes are accepted for API compatibility but are no longer used.
func NewHealthAppService(
	healthStore outbound.EndpointHealthStore,
	_ outbound.StateStore,
	_ outbound.RouteDistributor,
	logger *slog.Logger,
) *HealthAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthAppService{
		healthStore: healthStore,
		logger:      logger,
	}
}

// ReportEndpointHealth processes a health status report from an agent.
func (s *HealthAppService) ReportEndpointHealth(ctx context.Context, report *model.EndpointHealth) error {
	if err := s.healthStore.PutEndpointHealth(ctx, report); err != nil {
		return fmt.Errorf("health report: %w", err)
	}

	s.logger.Debug("endpoint health reported",
		"service", report.ServiceID,
		"address", report.Address,
		"status", report.Status,
		"nodeID", report.NodeID,
	)

	return nil
}

// DrainService initiates graceful termination for a service endpoint.
func (s *HealthAppService) DrainService(ctx context.Context, serviceID model.ServiceID, endpointAddr string) error {
	drainReport := &model.EndpointHealth{
		ServiceID: serviceID,
		Address:   endpointAddr,
		Status:    model.EndpointHealthDraining,
	}

	if err := s.healthStore.PutEndpointHealth(ctx, drainReport); err != nil {
		return fmt.Errorf("drain service: %w", err)
	}

	s.logger.Info("service endpoint draining",
		"service", serviceID,
		"address", endpointAddr,
	)

	return nil
}

// Compile-time interface check.
var _ inbound.ServiceHealthReporter = (*HealthAppService)(nil)
