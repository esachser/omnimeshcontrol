package otelmetrics

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRecorder_PrometheusExportAndCardinality(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	cfg.Metrics.Cardinality.DropLabels = map[string][]string{
		model.MetricCPResourcesApplyTotal: {"dropped_label"},
	}
	cfg.Metrics.Cardinality.HistogramBuckets["duration_ms"] = []float64{5, 25, 100}

	rec, err := NewRecorder(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer func() {
		if err := rec.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}()

	ctx := context.Background()
	rec.IncrCounter(ctx, model.MetricCPResourcesApplyTotal, map[string]string{
		"kind":          "Service",
		"namespace":     "default",
		"result":        "success",
		"dropped_label": "secret",
	})
	rec.SetGauge(ctx, model.MetricCPNodesConnected, 3, map[string]string{
		"platform": "linux",
	})
	rec.RecordHistogram(ctx, model.MetricCPResourcesApplyDurationMs, 12.5, map[string]string{
		"kind": "Service",
	})

	handler := rec.Handler()
	if handler == nil {
		t.Fatal("expected non-nil Prometheus HTTP handler")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.Code)
	}
	body := resp.Body.String()

	if !strings.Contains(body, "omnimesh_cp_resources_apply_total") {
		t.Errorf("expected counter metric in /metrics output, got:\n%s", body)
	}
	if strings.Contains(body, "dropped_label") {
		t.Errorf("expected dropped_label to be stripped from output, got:\n%s", body)
	}
	if !strings.Contains(body, "omnimesh_cp_nodes_connected") {
		t.Errorf("expected gauge metric in /metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, "omnimesh_cp_resources_apply_duration_ms_bucket") {
		t.Errorf("expected histogram buckets in /metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, `le="25"`) {
		t.Errorf("expected custom bucket le=\"25\" in /metrics output, got:\n%s", body)
	}
}

func TestRecorder_ManualReaderBucketsAndCardinalityWarning(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg := model.DefaultTelemetryConfig()
	cfg.Metrics.Cardinality.MaxLabelCardinality = 2
	cfg.Metrics.Cardinality.HistogramBuckets["duration_ms"] = []float64{10, 50}

	reader := sdkmetric.NewManualReader()
	rec, err := NewRecorderWithReader(cfg, reader, logger)
	if err != nil {
		t.Fatalf("NewRecorderWithReader() error = %v", err)
	}
	defer func() { _ = rec.Shutdown(context.Background()) }()

	ctx := context.Background()
	rec.IncrCounter(ctx, "omnimesh.cp.test.counter", map[string]string{"id": "1"})
	rec.IncrCounter(ctx, "omnimesh.cp.test.counter", map[string]string{"id": "2"})
	rec.IncrCounter(ctx, "omnimesh.cp.test.counter", map[string]string{"id": "3"})
	rec.RecordHistogram(ctx, "omnimesh.cp.test.duration_ms", 20, map[string]string{"op": "put"})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !strings.Contains(logBuf.String(), "cardinality exceeded") {
		t.Errorf("expected cardinality warning in logs, got %q", logBuf.String())
	}

	foundHist := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "omnimesh.cp.test.duration_ms" {
				hist, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("expected Histogram[float64], got %T", m.Data)
				}
				if len(hist.DataPoints) != 1 || len(hist.DataPoints[0].Bounds) != 2 {
					t.Fatalf("unexpected histogram bounds: %+v", hist.DataPoints)
				}
				foundHist = true
			}
		}
	}
	if !foundHist {
		t.Fatal("expected histogram metric in collected ResourceMetrics")
	}
}

func TestRecorder_DisabledIsNoop(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	cfg.Metrics.Enabled = false

	rec, err := NewRecorder(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if rec.Handler() != nil {
		t.Fatal("expected nil handler when metrics disabled")
	}

	ctx := context.Background()
	rec.IncrCounter(ctx, "omnimesh.cp.test.counter", map[string]string{"a": "b"})
	rec.SetGauge(ctx, "omnimesh.cp.test.gauge", 1, nil)
	rec.RecordHistogram(ctx, "omnimesh.cp.test.duration_ms", 5, nil)

	if err := rec.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
