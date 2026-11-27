package grpcadapter

import (
	"context"
	"encoding/json"
	"testing"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestControlPlaneServer_Telemetry(t *testing.T) {
	srv, _ := newTestControlPlaneServer(t)
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()
	srv.SetTelemetry(metrics, tracer)

	ctx := context.Background()

	// Structural validation error (missing metadata)
	_, err := srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{Kind: "Service"},
	})
	if err == nil {
		t.Fatal("expected error for missing metadata")
	}

	valErrs := metrics.FindByName(model.MetricCPResourcesValidationErrors)
	if len(valErrs) != 1 || valErrs[0].Labels["error_type"] != "structural" {
		t.Errorf("expected structural validation error, got %+v", valErrs)
	}

	// Valid Service apply and delete
	specBytes, _ := json.Marshal(map[string]any{
		"protocol": "HTTP",
		"port":     8080,
	})
	_, err = srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{
			ApiVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata:   &omnimeshv1.ResourceMetadata{Name: "orders", Namespace: "production"},
			Spec:       specBytes,
		},
	})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}

	if got := metrics.CountByName(model.MetricCPResourcesApplyTotal); got != 2 {
		t.Errorf("apply.total count = %d, want 2", got)
	}
	if got := metrics.CountByName(model.MetricCPResourcesApplyDurationMs); got != 2 {
		t.Errorf("apply.duration_ms count = %d, want 2", got)
	}

	_, err = srv.DeleteResource(ctx, &omnimeshv1.DeleteResourceRequest{
		Kind:      "Service",
		Namespace: "production",
		Name:      "orders",
	})
	if err != nil {
		t.Fatalf("DeleteResource() error = %v", err)
	}
	if got := metrics.CountByName(model.MetricCPResourcesDeleteTotal); got != 1 {
		t.Errorf("delete.total count = %d, want 1", got)
	}
	if len(tracer.Spans) < 3 {
		t.Errorf("expected at least 3 spans, got %d", len(tracer.Spans))
	}
}
