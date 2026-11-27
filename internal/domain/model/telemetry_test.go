package model

import (
	"errors"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain"
)

func TestTelemetryConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TelemetryConfig)
		wantErr bool
	}{
		{
			name:    "default config is valid",
			mutate:  func(_ *TelemetryConfig) {},
			wantErr: false,
		},
		{
			name: "metrics disabled skips metrics validation",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Enabled = false
				c.Metrics.Endpoint = ""
			},
			wantErr: false,
		},
		{
			name: "invalid metrics endpoint",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Endpoint = "not-a-host-port"
			},
			wantErr: true,
		},
		{
			name: "invalid agent metrics port zero",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.AgentMetricsPort = 0
			},
			wantErr: true,
		},
		{
			name: "invalid agent metrics port too large",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.AgentMetricsPort = 70000
			},
			wantErr: true,
		},
		{
			name: "negative max label cardinality",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Cardinality.MaxLabelCardinality = -1
			},
			wantErr: true,
		},
		{
			name: "non-monotonic histogram buckets",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Cardinality.HistogramBuckets["duration_ms"] = []float64{10, 10, 50}
			},
			wantErr: true,
		},
		{
			name: "empty histogram buckets",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Cardinality.HistogramBuckets["duration_ms"] = []float64{}
			},
			wantErr: true,
		},
		{
			name: "otlp-grpc metrics exporter without target",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Exporters = []ExporterConfig{{Type: ExporterOTLPGRPC}}
			},
			wantErr: true,
		},
		{
			name: "unsupported metrics exporter type",
			mutate: func(c *TelemetryConfig) {
				c.Metrics.Exporters = []ExporterConfig{{Type: "stdout"}}
			},
			wantErr: true,
		},
		{
			name: "valid traces config",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.Sampler = SamplerParentBasedTraceIDRatio
				c.Traces.SampleRate = 0.25
				c.Traces.Exporters = []ExporterConfig{{Type: ExporterOTLPGRPC, Target: "localhost:4317"}}
			},
			wantErr: false,
		},
		{
			name: "invalid trace sample rate above 1",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.SampleRate = 1.5
			},
			wantErr: true,
		},
		{
			name: "invalid trace sample rate below 0",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.SampleRate = -0.1
			},
			wantErr: true,
		},
		{
			name: "unsupported trace sampler",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.Sampler = "invalid_sampler"
			},
			wantErr: true,
		},
		{
			name: "trace exporter prometheus unsupported",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.Exporters = []ExporterConfig{{Type: ExporterPrometheus}}
			},
			wantErr: true,
		},
		{
			name: "trace otlp exporter missing target",
			mutate: func(c *TelemetryConfig) {
				c.Traces.Enabled = true
				c.Traces.Exporters = []ExporterConfig{{Type: ExporterOTLPHTTP}}
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultTelemetryConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, domain.ErrInvalidTelemetryConfig) {
				t.Errorf("Validate() error = %v, want errors.Is(ErrInvalidTelemetryConfig)", err)
			}
		})
	}
}

func TestSpanContext(t *testing.T) {
	tests := []struct {
		name        string
		sc          SpanContext
		wantValid   bool
		wantSampled bool
	}{
		{
			name:        "empty is invalid and unsampled",
			sc:          SpanContext{},
			wantValid:   false,
			wantSampled: false,
		},
		{
			name:        "missing span id is invalid",
			sc:          SpanContext{TraceID: "0102030405060708090a0b0c0d0e0f10"},
			wantValid:   false,
			wantSampled: false,
		},
		{
			name: "valid unsampled",
			sc: SpanContext{
				TraceID:    "0102030405060708090a0b0c0d0e0f10",
				SpanID:     "0102030405060708",
				TraceFlags: 0x00,
			},
			wantValid:   true,
			wantSampled: false,
		},
		{
			name: "valid sampled",
			sc: SpanContext{
				TraceID:    "0102030405060708090a0b0c0d0e0f10",
				SpanID:     "0102030405060708",
				TraceFlags: 0x01,
				Remote:     true,
			},
			wantValid:   true,
			wantSampled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.IsValid(); got != tc.wantValid {
				t.Errorf("IsValid() = %v, want %v", got, tc.wantValid)
			}
			if got := tc.sc.IsSampled(); got != tc.wantSampled {
				t.Errorf("IsSampled() = %v, want %v", got, tc.wantSampled)
			}
		})
	}
}
