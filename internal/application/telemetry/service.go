// Package telemetry provides the application-layer orchestration for
// observability: holding telemetry ports, managing exporter shutdown, and
// exposing connected agent scrape targets for Prometheus service discovery.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// TelemetryService orchestrates control plane observability ports, lifecycle
// shutdown, and Prometheus HTTP service discovery target generation.
type TelemetryService struct {
	cfg         model.TelemetryConfig
	metrics     outbound.MetricsRecorder
	tracer      outbound.TraceExporter
	shutdowns   []outbound.TelemetryShutdown
	nodeTracker domainservice.NodeTracker
	stateStore  outbound.StateStore
	logger      *slog.Logger
}

// Config holds dependencies for TelemetryService.
type Config struct {
	Telemetry   model.TelemetryConfig
	Metrics     outbound.MetricsRecorder
	Tracer      outbound.TraceExporter
	Shutdowns   []outbound.TelemetryShutdown
	NodeTracker domainservice.NodeTracker
	StateStore  outbound.StateStore
	Logger      *slog.Logger
}

// NewTelemetryService creates a new TelemetryService with nil-safe port defaults.
func NewTelemetryService(cfg Config) *TelemetryService {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TelemetryService{
		cfg:         cfg.Telemetry,
		metrics:     EnsureMetrics(cfg.Metrics),
		tracer:      EnsureTracer(cfg.Tracer),
		shutdowns:   cfg.Shutdowns,
		nodeTracker: cfg.NodeTracker,
		stateStore:  cfg.StateStore,
		logger:      logger,
	}
}

// Metrics returns the configured MetricsRecorder port (never nil).
func (s *TelemetryService) Metrics() outbound.MetricsRecorder {
	return s.metrics
}

// Tracer returns the configured TraceExporter port (never nil).
func (s *TelemetryService) Tracer() outbound.TraceExporter {
	return s.tracer
}

// ListMetricsTargets builds Prometheus HTTP SD target groups for all connected
// agent nodes (RFC-0005 §9.1).
func (s *TelemetryService) ListMetricsTargets(ctx context.Context) ([]model.MetricTargetGroup, error) {
	if s.nodeTracker == nil || s.stateStore == nil {
		return []model.MetricTargetGroup{}, nil
	}

	port := s.cfg.Metrics.AgentMetricsPort
	if port <= 0 {
		port = 9091
	}

	nodeIDs := s.nodeTracker.ConnectedNodes()
	groups := make([]model.MetricTargetGroup, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, err := s.stateStore.GetNode(ctx, nodeID)
		if err != nil || node == nil || node.Addr == "" {
			if err != nil {
				s.logger.Warn("telemetry: failed to resolve node for metrics targets", "nodeID", nodeID, "error", err)
			}
			continue
		}

		host := extractHost(node.Addr)
		if host == "" {
			continue
		}

		target := net.JoinHostPort(host, strconv.Itoa(port))
		labels := map[string]string{
			"job":      "omnimesh-agent",
			"node_id":  node.ID,
			"platform": node.Platform.String(),
		}
		if node.Target != "" {
			labels["target"] = node.Target
		}

		groups = append(groups, model.MetricTargetGroup{
			Targets: []string{target},
			Labels:  labels,
		})
	}

	return groups, nil
}

// Shutdown flushes and shuts down all registered telemetry pipelines.
func (s *TelemetryService) Shutdown(ctx context.Context) error {
	var errs []error
	for _, sd := range s.shutdowns {
		if sd == nil {
			continue
		}
		if err := sd.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return addr
}

// Compile-time interface check.
var _ inbound.MetricsTargetProvider = (*TelemetryService)(nil)
