package model

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
)

// ExporterType identifies a telemetry export backend.
type ExporterType string

const (
	// ExporterPrometheus exposes a Prometheus/OpenMetrics /metrics HTTP endpoint.
	ExporterPrometheus ExporterType = "prometheus"
	// ExporterOTLPGRPC pushes telemetry via OTLP over gRPC.
	ExporterOTLPGRPC ExporterType = "otlp-grpc"
	// ExporterOTLPHTTP pushes telemetry via OTLP over HTTP.
	ExporterOTLPHTTP ExporterType = "otlp-http"
)

// TraceSamplerType identifies a trace sampling strategy.
type TraceSamplerType string

const (
	// SamplerParentBasedTraceIDRatio respects parent sampling decisions and falls back to trace ID ratio.
	SamplerParentBasedTraceIDRatio TraceSamplerType = "parentbased_traceidratio"
	// SamplerTraceIDRatio samples traces deterministically according to a trace ID ratio.
	SamplerTraceIDRatio TraceSamplerType = "traceidratio"
	// SamplerAlwaysOn samples every trace.
	SamplerAlwaysOn TraceSamplerType = "always_on"
	// SamplerAlwaysOff samples no traces (unless error sampling elevates failed spans).
	SamplerAlwaysOff TraceSamplerType = "always_off"
)

// RFC-0005 §4 Control Plane metric names.
const (
	// Resource Management (§4.1)
	MetricCPResourcesTotal            = "omnimesh.cp.resources.total"
	MetricCPResourcesApplyTotal       = "omnimesh.cp.resources.apply.total"
	MetricCPResourcesApplyDurationMs  = "omnimesh.cp.resources.apply.duration_ms"
	MetricCPResourcesDeleteTotal      = "omnimesh.cp.resources.delete.total"
	MetricCPResourcesValidationErrors = "omnimesh.cp.resources.validation.errors"

	// IPAM (§4.2)
	MetricCPIpamAllocated        = "omnimesh.cp.ipam.allocated"
	MetricCPIpamCapacity         = "omnimesh.cp.ipam.capacity"
	MetricCPIpamUtilizationRatio = "omnimesh.cp.ipam.utilization_ratio"
	MetricCPIpamAllocationErrors = "omnimesh.cp.ipam.allocation.errors"

	// Node Management (§4.3)
	MetricCPNodesConnected       = "omnimesh.cp.nodes.connected"
	MetricCPNodesHeartbeatTotal  = "omnimesh.cp.nodes.heartbeat.total"
	MetricCPNodesHeartbeatMissed = "omnimesh.cp.nodes.heartbeat.missed"
	MetricCPNodesDisconnections  = "omnimesh.cp.nodes.disconnections"

	// etcd (§4.4)
	MetricCPEtcdOperationsTotal      = "omnimesh.cp.etcd.operations.total"
	MetricCPEtcdOperationsDurationMs = "omnimesh.cp.etcd.operations.duration_ms"
	MetricCPEtcdOperationsErrors     = "omnimesh.cp.etcd.operations.errors"
	MetricCPEtcdLeader               = "omnimesh.cp.etcd.leader"
	MetricCPEtcdDBSizeBytes          = "omnimesh.cp.etcd.db_size_bytes"
	MetricCPEtcdPeersConnected       = "omnimesh.cp.etcd.peers.connected"

	// Identity / SPIRE (§4.5)
	MetricCPIdentityRegistrationsTotal      = "omnimesh.cp.identity.registrations.total"
	MetricCPIdentityRegistrationsDurationMs = "omnimesh.cp.identity.registrations.duration_ms"
	MetricCPIdentityDeregistrationsTotal    = "omnimesh.cp.identity.deregistrations.total"

	// DNS (§4.6)
	MetricCPDNSUpsertsTotal      = "omnimesh.cp.dns.upserts.total"
	MetricCPDNSDeletesTotal      = "omnimesh.cp.dns.deletes.total"
	MetricCPDNSUpsertsDurationMs = "omnimesh.cp.dns.upserts.duration_ms"

	// Route Distribution (§4.7)
	MetricCPRoutesAnnouncementsTotal     = "omnimesh.cp.routes.announcements.total"
	MetricCPRoutesWithdrawalsTotal       = "omnimesh.cp.routes.withdrawals.total"
	MetricCPRoutesDistributionDurationMs = "omnimesh.cp.routes.distribution.duration_ms"

	// Scoped Distribution (§4.8)
	MetricCPScopeSnapshotsTotal   = "omnimesh.cp.scope.snapshots.total"
	MetricCPScopeUpdatesTotal     = "omnimesh.cp.scope.updates.total"
	MetricCPScopeOnDemandTotal    = "omnimesh.cp.scope.ondemand.total"
	MetricCPScopeServicesPerAgent = "omnimesh.cp.scope.services_per_agent"

	// gRPC Server (§4.9)
	MetricCPGRPCRequestsTotal      = "omnimesh.cp.grpc.requests.total"
	MetricCPGRPCRequestsDurationMs = "omnimesh.cp.grpc.requests.duration_ms"
	MetricCPGRPCStreamsActive      = "omnimesh.cp.grpc.streams.active"
)

// ExporterConfig configures a single metrics or trace exporter.
type ExporterConfig struct {
	Type     ExporterType
	Target   string
	Insecure bool
	Headers  map[string]string
}

// CardinalityConfig configures label cardinality safeguards and histogram buckets.
type CardinalityConfig struct {
	MaxLabelCardinality int
	DropLabels          map[string][]string
	HistogramBuckets    map[string][]float64
}

// MetricsConfig configures control plane metric collection and export.
type MetricsConfig struct {
	Enabled          bool
	Endpoint         string
	Exporters        []ExporterConfig
	ExportInterval   time.Duration
	Cardinality      CardinalityConfig
	AgentMetricsPort int
}

// TracesConfig configures distributed trace sampling and export.
type TracesConfig struct {
	Enabled            bool
	Sampler            TraceSamplerType
	SampleRate         float64
	AlwaysSampleErrors bool
	Exporters          []ExporterConfig
}

// ResourceConfig holds OpenTelemetry resource attributes shared across signals.
type ResourceConfig struct {
	ServiceName      string
	ServiceNamespace string
	ServiceVersion   string
	Environment      string
	Attributes       map[string]string
}

// TelemetryConfig holds the complete observability configuration for metrics and traces.
type TelemetryConfig struct {
	Metrics  MetricsConfig
	Traces   TracesConfig
	Resource ResourceConfig
}

// DefaultTelemetryConfig returns the default control plane telemetry configuration.
// Metrics default to Prometheus on :9092 (avoiding :9090/:9091 gRPC ports) and
// traces default to disabled with 1% parent-based sampling and error elevation.
func DefaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{
		Metrics: MetricsConfig{
			Enabled:  true,
			Endpoint: ":9092",
			Exporters: []ExporterConfig{
				{Type: ExporterPrometheus},
			},
			ExportInterval:   15 * time.Second,
			AgentMetricsPort: 9091,
			Cardinality: CardinalityConfig{
				MaxLabelCardinality: 1000,
				HistogramBuckets: map[string][]float64{
					"duration_ms": {1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
				},
			},
		},
		Traces: TracesConfig{
			Enabled:            false,
			Sampler:            SamplerParentBasedTraceIDRatio,
			SampleRate:         0.01,
			AlwaysSampleErrors: true,
		},
		Resource: ResourceConfig{
			ServiceName:      "omnimesh-control-plane",
			ServiceNamespace: "omnimesh",
			Environment:      "development",
		},
	}
}

// Validate checks that the telemetry configuration is internally consistent and valid.
func (c TelemetryConfig) Validate() error {
	if c.Metrics.Enabled {
		if err := validateEndpoint(c.Metrics.Endpoint); err != nil {
			return fmt.Errorf("%w: metrics endpoint %q: %v", domain.ErrInvalidTelemetryConfig, c.Metrics.Endpoint, err)
		}
		if c.Metrics.AgentMetricsPort < 1 || c.Metrics.AgentMetricsPort > 65535 {
			return fmt.Errorf("%w: agent metrics port %d must be in [1, 65535]", domain.ErrInvalidTelemetryConfig, c.Metrics.AgentMetricsPort)
		}
		if c.Metrics.ExportInterval < 0 {
			return fmt.Errorf("%w: export interval must be non-negative", domain.ErrInvalidTelemetryConfig)
		}
		if c.Metrics.Cardinality.MaxLabelCardinality < 0 {
			return fmt.Errorf("%w: max label cardinality must be non-negative", domain.ErrInvalidTelemetryConfig)
		}
		for bucketKey, bounds := range c.Metrics.Cardinality.HistogramBuckets {
			if len(bounds) == 0 {
				return fmt.Errorf("%w: histogram buckets for %q must not be empty", domain.ErrInvalidTelemetryConfig, bucketKey)
			}
			for i := 1; i < len(bounds); i++ {
				if bounds[i] <= bounds[i-1] {
					return fmt.Errorf("%w: histogram buckets for %q must be strictly increasing", domain.ErrInvalidTelemetryConfig, bucketKey)
				}
			}
		}
		for _, exp := range c.Metrics.Exporters {
			if err := validateMetricsExporter(exp); err != nil {
				return err
			}
		}
	}

	if c.Traces.Enabled {
		switch c.Traces.Sampler {
		case SamplerParentBasedTraceIDRatio, SamplerTraceIDRatio, SamplerAlwaysOn, SamplerAlwaysOff:
		default:
			return fmt.Errorf("%w: unsupported trace sampler %q", domain.ErrInvalidTelemetryConfig, c.Traces.Sampler)
		}
		if c.Traces.SampleRate < 0 || c.Traces.SampleRate > 1 {
			return fmt.Errorf("%w: trace sample rate %v must be in [0, 1]", domain.ErrInvalidTelemetryConfig, c.Traces.SampleRate)
		}
		for _, exp := range c.Traces.Exporters {
			if err := validateTracesExporter(exp); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateEndpoint(addr string) error {
	if addr == "" {
		return fmt.Errorf("endpoint is required")
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid port %q", portStr)
	}
	return nil
}

func validateMetricsExporter(exp ExporterConfig) error {
	switch exp.Type {
	case ExporterPrometheus:
		return nil
	case ExporterOTLPGRPC, ExporterOTLPHTTP:
		if exp.Target == "" {
			return fmt.Errorf("%w: target is required for metrics exporter %q", domain.ErrInvalidTelemetryConfig, exp.Type)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported metrics exporter type %q", domain.ErrInvalidTelemetryConfig, exp.Type)
	}
}

func validateTracesExporter(exp ExporterConfig) error {
	switch exp.Type {
	case ExporterOTLPGRPC, ExporterOTLPHTTP:
		if exp.Target == "" {
			return fmt.Errorf("%w: target is required for trace exporter %q", domain.ErrInvalidTelemetryConfig, exp.Type)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported trace exporter type %q", domain.ErrInvalidTelemetryConfig, exp.Type)
	}
}

// SpanContext represents W3C trace context identifiers within the domain layer
// without importing OpenTelemetry packages.
type SpanContext struct {
	TraceID    string
	SpanID     string
	TraceFlags byte
	Remote     bool
}

// IsValid reports whether both TraceID and SpanID are populated.
func (sc SpanContext) IsValid() bool {
	return sc.TraceID != "" && sc.SpanID != ""
}

// IsSampled reports whether the sampled trace flag (0x01) is set.
func (sc SpanContext) IsSampled() bool {
	return sc.TraceFlags&0x01 == 0x01
}

// MetricTargetGroup represents a Prometheus HTTP service-discovery target group
// returned by /api/v1/targets (RFC-0005 §9.1).
type MetricTargetGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels,omitempty"`
}
