package telemetrycfg

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestLoadWithLookup_YAMLAndEnvOverrides(t *testing.T) {
	yamlContent := `
observability:
  metrics:
    enabled: true
    endpoint: ":9100"
    export_interval: "30s"
    agent_metrics_port: 9191
    exporters:
      - type: prometheus
      - type: otlp-grpc
        target: "collector:4317"
        insecure: true
    cardinality:
      max_label_cardinality: 500
      drop_labels:
        omnimesh.cp.resources.apply.total:
          - namespace
      histogram_buckets:
        duration_ms: [1, 10, 100]
  traces:
    enabled: true
    sampler: "parentbased_traceidratio"
    sample_rate: 0.1
    always_sample_errors: true
    exporters:
      - type: otlp-grpc
        target: "collector:4317"
        insecure: true
  resource:
    service.name: "omnimesh-cp-yaml"
    service.namespace: "mesh-prod"
    deployment.environment: "production"
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "telemetry.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}

	env := map[string]string{
		"OMNIMESH_TELEMETRY_CONFIG": cfgPath,
		"OMNIMESH_METRICS_ADDR":     ":9292",
		"OMNIMESH_SERVICE_NAME":     "omnimesh-cp-env",
	}

	cfg, err := loadWithLookup(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("loadWithLookup() error = %v", err)
	}

	if cfg.Metrics.Endpoint != ":9292" {
		t.Errorf("Metrics.Endpoint = %q, want :9292", cfg.Metrics.Endpoint)
	}
	if cfg.Metrics.ExportInterval != 30*time.Second {
		t.Errorf("Metrics.ExportInterval = %v, want 30s", cfg.Metrics.ExportInterval)
	}
	if cfg.Metrics.AgentMetricsPort != 9191 {
		t.Errorf("Metrics.AgentMetricsPort = %d, want 9191", cfg.Metrics.AgentMetricsPort)
	}
	if cfg.Metrics.Cardinality.MaxLabelCardinality != 500 {
		t.Errorf("MaxLabelCardinality = %d, want 500", cfg.Metrics.Cardinality.MaxLabelCardinality)
	}
	if !cfg.Traces.Enabled || cfg.Traces.SampleRate != 0.1 {
		t.Errorf("Traces = %+v, want enabled with sample_rate 0.1", cfg.Traces)
	}
	if cfg.Resource.ServiceName != "omnimesh-cp-env" {
		t.Errorf("Resource.ServiceName = %q, want omnimesh-cp-env", cfg.Resource.ServiceName)
	}
	if cfg.Resource.Environment != "production" {
		t.Errorf("Resource.Environment = %q, want production", cfg.Resource.Environment)
	}
}

func TestLoadWithLookup_EnvOnlyDefaults(t *testing.T) {
	env := map[string]string{
		"OMNIMESH_TRACES_ENABLED":     "true",
		"OMNIMESH_TRACES_OTLP_TARGET": "127.0.0.1:4317",
		"OMNIMESH_TRACES_SAMPLER":     string(model.SamplerAlwaysOn),
	}

	cfg, err := loadWithLookup(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("loadWithLookup() error = %v", err)
	}

	if cfg.Metrics.Endpoint != ":9092" {
		t.Errorf("Metrics.Endpoint = %q, want :9092", cfg.Metrics.Endpoint)
	}
	if !cfg.Traces.Enabled {
		t.Error("expected Traces.Enabled = true")
	}
	if len(cfg.Traces.Exporters) != 1 || cfg.Traces.Exporters[0].Target != "127.0.0.1:4317" {
		t.Errorf("Traces.Exporters = %+v, want otlp-grpc 127.0.0.1:4317", cfg.Traces.Exporters)
	}
}
