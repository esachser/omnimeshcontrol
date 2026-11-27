package route

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// RouteAppService implements the RouteManager inbound port.
type RouteAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	distributor outbound.ScopeDistributor
	targets     inbound.TargetManager
	tracker     service.NodeTracker
	logger      *slog.Logger
	metrics     outbound.MetricsRecorder
	tracer      outbound.TraceExporter
}

// NewRouteAppService creates a new RouteAppService.
func NewRouteAppService(resources outbound.ResourceStore, state outbound.StateStore, targets inbound.TargetManager, logger *slog.Logger) *RouteAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &RouteAppService{
		resources: resources,
		state:     state,
		targets:   targets,
		logger:    logger,
		metrics:   telemetry.NoopMetricsRecorder{},
		tracer:    telemetry.NoopTraceExporter{},
	}
}

// SetTelemetry injects optional metrics and tracing ports.
func (s *RouteAppService) SetTelemetry(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) {
	s.metrics = telemetry.EnsureMetrics(metrics)
	s.tracer = telemetry.EnsureTracer(tracer)
}

// SetScopeDistributor sets the scope distributor.
func (s *RouteAppService) SetScopeDistributor(d outbound.ScopeDistributor) {
	s.distributor = d
}

// SetNodeTracker sets the node tracker.
func (s *RouteAppService) SetNodeTracker(t service.NodeTracker) {
	s.tracker = t
}

// Apply creates or updates a target route.
func (s *RouteAppService) Apply(ctx context.Context, route *model.TargetRoute) error {
	ctx, span := s.tracer.StartSpan(ctx, "RouteAppService.Apply", map[string]string{
		"omnimesh.namespace":           route.Namespace,
		"omnimesh.node":                route.NodeID,
		"omnimesh.destination.service": route.TargetService,
	})
	defer span.End()

	if route.Name == "" || route.Namespace == "" || route.NodeID == "" || route.TargetService == "" {
		err := fmt.Errorf("route apply: name, namespace, nodeID and targetService are required")
		span.SetError(err)
		return err
	}

	var leaseID int64
	if s.tracker != nil {
		leaseID = s.tracker.LeaseID(route.NodeID)
	}

	if err := s.state.PersistTargetRoute(ctx, route, leaseID); err != nil {
		err = fmt.Errorf("route apply: %w", err)
		span.SetError(err)
		return err
	}

	s.metrics.IncrCounter(ctx, model.MetricCPRoutesAnnouncementsTotal, map[string]string{
		"transport": "grpc",
	})

	s.logger.Info("target route applied", "namespace", route.Namespace, "name", route.Name, "targetService", route.TargetService)

	s.broadcastUpdate(ctx, *route, model.ScopeUpdateModify)

	return nil
}

// Get retrieves a target route by namespace and name.
func (s *RouteAppService) Get(ctx context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error) {
	return s.state.GetTargetRoute(ctx, namespace, nodeId, serviceName, name)
}

// List returns all target routes, optionally filtered by namespace.
func (s *RouteAppService) List(ctx context.Context, namespace string) ([]*model.TargetRoute, error) {
	return s.state.ListTargetRoutes(ctx, namespace)
}

// Delete removes a target route.
func (s *RouteAppService) Delete(ctx context.Context, namespace, name, serviceName, nodeId string) error {
	ctx, span := s.tracer.StartSpan(ctx, "RouteAppService.Delete", map[string]string{
		"omnimesh.namespace":           namespace,
		"omnimesh.node":                nodeId,
		"omnimesh.destination.service": serviceName,
	})
	defer span.End()

	route, getErr := s.Get(ctx, namespace, name, serviceName, nodeId)

	err := s.state.DeleteTargetRoute(ctx, namespace, nodeId, serviceName, name)
	if err != nil {
		err = fmt.Errorf("route delete: %w", err)
		span.SetError(err)
		return err
	}

	s.metrics.IncrCounter(ctx, model.MetricCPRoutesWithdrawalsTotal, map[string]string{
		"transport": "grpc",
	})

	s.logger.Info("target route deleted", "namespace", namespace, "name", name)

	if getErr == nil && route != nil {
		s.broadcastUpdate(ctx, *route, model.ScopeUpdateRemove)
	}

	return nil
}

// DeleteByNodeId broadcasts removal of all routes for a node.
func (s *RouteAppService) DeleteByNodeId(ctx context.Context, nodeId string) error {
	nodeRoutes, listErr := s.state.ListTargetRoutesByNode(ctx, nodeId)
	var routesToDelete []model.TargetRoute
	if listErr == nil {
		for _, rt := range nodeRoutes {
			routesToDelete = append(routesToDelete, *rt)
		}
	}

	s.logger.Info("target routes cleared for node", "nodeId", nodeId)

	for _, route := range routesToDelete {
		s.broadcastUpdate(ctx, route, model.ScopeUpdateRemove)
	}

	return nil
}

func (s *RouteAppService) ClearRoutes(ctx context.Context) error {
	err := s.state.ClearTargetRoutes(ctx)
	if err != nil {
		return fmt.Errorf("route clear: %w", err)
	}

	s.logger.Info("target routes cleared")
	return nil
}

func (s *RouteAppService) broadcastUpdate(ctx context.Context, route model.TargetRoute, updateType model.ScopeUpdateType) {
	if s.distributor == nil {
		return
	}

	if route.Address == "" {
		destNode, err := s.state.GetNode(ctx, route.NodeID)
		if err == nil && destNode != nil {
			route.Address = destNode.Addr
			route.Target = destNode.Target
			if destNode.Target != "" {
				netTarget, tErr := s.targets.Get(ctx, destNode.Target)
				if tErr == nil && netTarget != nil && netTarget.GatewayAddress != "" {
					route.TargetAddress = netTarget.GatewayAddress
				}
			}
		}
	}

	update := model.ScopeUpdate{
		Type:   updateType,
		Routes: []model.TargetRoute{route},
	}

	distStart := time.Now()
	if err := s.distributor.BroadcastUpdate(ctx, update); err != nil {
		s.logger.Warn("failed to broadcast route scope update", "route", route.Name, "error", err)
		return
	}
	s.metrics.RecordHistogram(ctx, model.MetricCPRoutesDistributionDurationMs, float64(time.Since(distStart).Microseconds())/1000.0, nil)
}

// Compile-time interface check.
var _ inbound.RouteManager = (*RouteAppService)(nil)
