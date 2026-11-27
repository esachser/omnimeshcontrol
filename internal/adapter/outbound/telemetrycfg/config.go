// Package telemetrycfg loads and validates domain telemetry configuration
// from YAML files and environment variables.
package telemetrycfg

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"sigs.k8s.io/yaml"
)

type yamlRoot struct {
	Observability yamlObservability `json:"observability"`
}

type yamlObservability struct {
	Metrics  yamlMetrics       `json:"metrics"`
	Traces   yamlTraces        `json:"traces"`
	Resource map[string]string `json:"resource"`
}

type yamlMetrics struct {
	Enabled          *bool            `json:"enabled"`
	Endpoint         string           `json:"endpoint"`
	ExportInterval   string           `json:"export_interval"`
	AgentMetricsPort int              `json:"agent_metrics_port"`
	Exporters        []yamlExporter   `json:"exporters"`
	Cardinality      *yamlCardinality `json:"cardinality"`
}

type yamlCardinality struct {
	MaxLabelCardinality *int                 `json:"max_label_cardinality"`
	DropLabels          map[string][]string  `json:"drop_labels"`
	HistogramBuckets    map[string][]float64 `json:"histogram_buckets"`
}

type yamlTraces struct {
	Enabled            *bool          `json:"enabled"`
	Sampler            string         `json:"sampler"`
	SampleRate         *float64       `json:"sample_rate"`
	AlwaysSampleErrors *bool          `json:"always_sample_errors"`
	Exporters          []yamlExporter `json:"exporters"`
}

type yamlExporter struct {
	Type     string            `json:"type"`
	Target   string            `json:"target"`
	Insecure bool              `json:"insecure"`
	Headers  map[string]string `json:"headers"`
}

// LoadFromEnv constructs a validated TelemetryConfig by starting with defaults,
// applying an optional YAML file referenced by OMNIMESH_TELEMETRY_CONFIG, and
// finally applying environment variable overrides.
func LoadFromEnv() (model.TelemetryConfig, error) {
	return loadWithLookup(os.Getenv)
}

func loadWithLookup(getenv func(string) string) (model.TelemetryConfig, error) {
	cfg := model.DefaultTelemetryConfig()

	if path := strings.TrimSpace(getenv("OMNIMESH_TELEMETRY_CONFIG")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return model.TelemetryConfig{}, fmt.Errorf("telemetrycfg: read %q: %w", path, err)
		}
		if err := applyYAML(&cfg, data); err != nil {
			return model.TelemetryConfig{}, err
		}
	}

	if err := applyEnv(&cfg, getenv); err != nil {
		return model.TelemetryConfig{}, err
	}

	if err := cfg.Validate(); err != nil {
		return model.TelemetryConfig{}, err
	}
	return cfg, nil
}

func applyYAML(cfg *model.TelemetryConfig, data []byte) error {
	var root yamlRoot
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("telemetrycfg: parse yaml: %w", err)
	}

	obs := root.Observability
	if obs.Metrics.Enabled != nil {
		cfg.Metrics.Enabled = *obs.Metrics.Enabled
	}
	if obs.Metrics.Endpoint != "" {
		cfg.Metrics.Endpoint = obs.Metrics.Endpoint
	}
	if obs.Metrics.ExportInterval != "" {
		d, err := time.ParseDuration(obs.Metrics.ExportInterval)
		if err != nil {
			return fmt.Errorf("telemetrycfg: parse metrics.export_interval: %w", err)
		}
		cfg.Metrics.ExportInterval = d
	}
	if obs.Metrics.AgentMetricsPort != 0 {
		cfg.Metrics.AgentMetricsPort = obs.Metrics.AgentMetricsPort
	}
	if len(obs.Metrics.Exporters) > 0 {
		cfg.Metrics.Exporters = convertExporters(obs.Metrics.Exporters)
	}
	if obs.Metrics.Cardinality != nil {
		if obs.Metrics.Cardinality.MaxLabelCardinality != nil {
			cfg.Metrics.Cardinality.MaxLabelCardinality = *obs.Metrics.Cardinality.MaxLabelCardinality
		}
		if len(obs.Metrics.Cardinality.DropLabels) > 0 {
			cfg.Metrics.Cardinality.DropLabels = obs.Metrics.Cardinality.DropLabels
		}
		if len(obs.Metrics.Cardinality.HistogramBuckets) > 0 {
			if cfg.Metrics.Cardinality.HistogramBuckets == nil {
				cfg.Metrics.Cardinality.HistogramBuckets = make(map[string][]float64)
			}
			for k, v := range obs.Metrics.Cardinality.HistogramBuckets {
				cfg.Metrics.Cardinality.HistogramBuckets[k] = v
			}
		}
	}

	if obs.Traces.Enabled != nil {
		cfg.Traces.Enabled = *obs.Traces.Enabled
	}
	if obs.Traces.Sampler != "" {
		cfg.Traces.Sampler = model.TraceSamplerType(obs.Traces.Sampler)
	}
	if obs.Traces.SampleRate != nil {
		cfg.Traces.SampleRate = *obs.Traces.SampleRate
	}
	if obs.Traces.AlwaysSampleErrors != nil {
		cfg.Traces.AlwaysSampleErrors = *obs.Traces.AlwaysSampleErrors
	}
	if len(obs.Traces.Exporters) > 0 {
		cfg.Traces.Exporters = convertExporters(obs.Traces.Exporters)
	}

	for k, v := range obs.Resource {
		switch k {
		case "service.name":
			cfg.Resource.ServiceName = v
		case "service.namespace":
			cfg.Resource.ServiceNamespace = v
		case "service.version":
			cfg.Resource.ServiceVersion = v
		case "deployment.environment", "deployment.environment.name":
			cfg.Resource.Environment = v
		default:
			if cfg.Resource.Attributes == nil {
				cfg.Resource.Attributes = make(map[string]string)
			}
			cfg.Resource.Attributes[k] = v
		}
	}

	return nil
}

func convertExporters(in []yamlExporter) []model.ExporterConfig {
	out := make([]model.ExporterConfig, 0, len(in))
	for _, e := range in {
		out = append(out, model.ExporterConfig{
			Type:     model.ExporterType(strings.TrimSpace(e.Type)),
			Target:   strings.TrimSpace(e.Target),
			Insecure: e.Insecure,
			Headers:  e.Headers,
		})
	}
	return out
}

func applyEnv(cfg *model.TelemetryConfig, getenv func(string) string) error {
	if v := getenv("OMNIMESH_METRICS_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_METRICS_ENABLED: %w", err)
		}
		cfg.Metrics.Enabled = b
	}
	if v := getenv("OMNIMESH_METRICS_ADDR"); v != "" {
		cfg.Metrics.Endpoint = v
	}
	if v := getenv("OMNIMESH_METRICS_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_METRICS_INTERVAL: %w", err)
		}
		cfg.Metrics.ExportInterval = d
	}
	if v := getenv("OMNIMESH_AGENT_METRICS_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_AGENT_METRICS_PORT: %w", err)
		}
		cfg.Metrics.AgentMetricsPort = p
	}
	if v := getenv("OMNIMESH_METRICS_EXPORTERS"); v != "" {
		otlpTarget := firstNonEmpty(getenv("OMNIMESH_METRICS_OTLP_TARGET"), getenv("OMNIMESH_OTLP_ENDPOINT"), getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
		insecure := parseBoolDefault(getenv("OMNIMESH_OTLP_INSECURE"), true)
		cfg.Metrics.Exporters = parseExporterList(v, otlpTarget, insecure)
	}

	if v := getenv("OMNIMESH_TRACES_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_TRACES_ENABLED: %w", err)
		}
		cfg.Traces.Enabled = b
	}
	if v := getenv("OMNIMESH_TRACES_SAMPLER"); v != "" {
		cfg.Traces.Sampler = model.TraceSamplerType(v)
	}
	if v := getenv("OMNIMESH_TRACES_SAMPLE_RATE"); v != "" {
		r, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_TRACES_SAMPLE_RATE: %w", err)
		}
		cfg.Traces.SampleRate = r
	}
	if v := getenv("OMNIMESH_TRACES_ALWAYS_SAMPLE_ERRORS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("telemetrycfg: OMNIMESH_TRACES_ALWAYS_SAMPLE_ERRORS: %w", err)
		}
		cfg.Traces.AlwaysSampleErrors = b
	}
	if v := getenv("OMNIMESH_TRACES_EXPORTERS"); v != "" {
		otlpTarget := firstNonEmpty(getenv("OMNIMESH_TRACES_OTLP_TARGET"), getenv("OMNIMESH_OTLP_ENDPOINT"), getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
		insecure := parseBoolDefault(getenv("OMNIMESH_OTLP_INSECURE"), true)
		cfg.Traces.Exporters = parseExporterList(v, otlpTarget, insecure)
	} else if len(cfg.Traces.Exporters) == 0 {
		if target := firstNonEmpty(getenv("OMNIMESH_TRACES_OTLP_TARGET"), getenv("OMNIMESH_OTLP_ENDPOINT"), getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); target != "" {
			cfg.Traces.Exporters = []model.ExporterConfig{{
				Type:     model.ExporterOTLPGRPC,
				Target:   target,
				Insecure: parseBoolDefault(getenv("OMNIMESH_OTLP_INSECURE"), true),
			}}
		}
	}

	if v := getenv("OMNIMESH_SERVICE_NAME"); v != "" {
		cfg.Resource.ServiceName = v
	}
	if v := getenv("OMNIMESH_SERVICE_NAMESPACE"); v != "" {
		cfg.Resource.ServiceNamespace = v
	}
	if v := getenv("OMNIMESH_ENVIRONMENT"); v != "" {
		cfg.Resource.Environment = v
	}
	return nil
}

func parseExporterList(raw, defaultTarget string, insecure bool) []model.ExporterConfig {
	parts := strings.Split(raw, ",")
	out := make([]model.ExporterConfig, 0, len(parts))
	for _, p := range parts {
		typ := model.ExporterType(strings.TrimSpace(p))
		if typ == "" {
			continue
		}
		exp := model.ExporterConfig{
			Type:     typ,
			Insecure: insecure,
		}
		if typ == model.ExporterOTLPGRPC || typ == model.ExporterOTLPHTTP {
			exp.Target = defaultTarget
		}
		out = append(out, exp)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseBoolDefault(raw string, def bool) bool {
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return b
}
