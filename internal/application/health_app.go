package application

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// HealthAppService implements the ServiceHealthReporter inbound port.
// It processes health reports from agents and coordinates route recalculation.
type HealthAppService struct {
	healthStore outbound.EndpointHealthStore
	stateStore  outbound.StateStore
	routes      outbound.RouteDistributor
	logger      *slog.Logger
}

// NewHealthAppService creates a new HealthAppService.
func NewHealthAppService(
	healthStore outbound.EndpointHealthStore,
	stateStore outbound.StateStore,
	routes outbound.RouteDistributor,
	logger *slog.Logger,
) *HealthAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthAppService{
		healthStore: healthStore,
		stateStore:  stateStore,
		routes:      routes,
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

	// Trigger route recalculation.
	if err := s.recalculateRoute(ctx, report.ServiceID); err != nil {
		s.logger.Warn("failed to recalculate route after health report", "service", report.ServiceID, "error", err)
	}

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

	if err := s.recalculateRoute(ctx, serviceID); err != nil {
		s.logger.Warn("failed to recalculate route after drain request", "service", serviceID, "error", err)
	}

	return nil
}

func (s *HealthAppService) recalculateRoute(ctx context.Context, serviceID model.ServiceID) error {
	// Retrieve mesh address for this service
	addr, err := s.stateStore.GetIPAllocation(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("get ip allocation: %w", err)
	}

	// Fetch all health records.
	records, err := s.healthStore.ListEndpointHealth(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("list endpoint health: %w", err)
	}

	route := model.MeshRoute{
		ServiceID:   serviceID,
		MeshAddr:    addr,
		TargetNodes: []model.NodeEndpoint{},
	}

	for _, h := range records {
		if h.Status == model.EndpointHealthHealthy {
			// Fetch node to get Target and Labels
			nodeTarget := ""
			var nodeLabels map[string]string
			if node, nErr := s.stateStore.GetNode(ctx, h.NodeID); nErr == nil && node != nil {
				nodeTarget = node.Target
				nodeLabels = node.Labels
			}

			// Parse the endpoint address
			if epAddr, pErr := netip.ParseAddrPort(h.Address); pErr == nil {
				route.TargetNodes = append(route.TargetNodes, model.NodeEndpoint{
					NodeID: h.NodeID,
					Addr:   epAddr,
					Target: nodeTarget,
					Labels: nodeLabels,
				})
			}
		}
	}

	// Announce updated route.
	if err := s.routes.AnnounceRoute(ctx, route); err != nil {
		return fmt.Errorf("announce route: %w", err)
	}

	return nil
}

// Compile-time interface check.
var _ inbound.ServiceHealthReporter = (*HealthAppService)(nil)
