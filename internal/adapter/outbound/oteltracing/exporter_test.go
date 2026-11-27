package oteltracing

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type mapCarrier map[string]string

func (m mapCarrier) Get(key string) string { return m[key] }
func (m mapCarrier) Set(key, val string)   { m[key] = val }
func (m mapCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestExporter_SpansAndPropagation(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	cfg.Traces.Enabled = true
	cfg.Traces.Sampler = model.SamplerAlwaysOn
	cfg.Traces.AlwaysSampleErrors = false

	memExp := tracetest.NewInMemoryExporter()
	exp, err := NewExporterWithSpanExporter(cfg, memExp, true, slog.Default())
	if err != nil {
		t.Fatalf("NewExporterWithSpanExporter() error = %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	ctx, span := exp.StartSpan(context.Background(), "parent.op", map[string]string{
		"omnimesh.node": "node-1",
	})
	span.SetAttribute("custom.key", "val")

	sc := exp.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsSampled() {
		t.Fatalf("expected valid sampled SpanContext, got %+v", sc)
	}

	carrier := make(mapCarrier)
	if err := exp.InjectContext(ctx, carrier); err != nil {
		t.Fatalf("InjectContext() error = %v", err)
	}
	if carrier["traceparent"] == "" {
		t.Fatal("expected W3C traceparent header to be injected")
	}
	span.End()

	extractedCtx := exp.ExtractContext(context.Background(), carrier)
	_, child := exp.StartSpan(extractedCtx, "child.op", nil)
	child.End()

	spans := memExp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 exported spans, got %d", len(spans))
	}
	if spans[1].Parent.SpanID() != spans[0].SpanContext.SpanID() {
		t.Errorf("child parent SpanID = %s, want %s", spans[1].Parent.SpanID(), spans[0].SpanContext.SpanID())
	}
}

func TestExporter_AlwaysSampleErrors(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	cfg.Traces.Enabled = true
	cfg.Traces.Sampler = model.SamplerAlwaysOff
	cfg.Traces.AlwaysSampleErrors = true

	memExp := tracetest.NewInMemoryExporter()
	exp, err := NewExporterWithSpanExporter(cfg, memExp, true, slog.Default())
	if err != nil {
		t.Fatalf("NewExporterWithSpanExporter() error = %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	// Healthy span should be dropped under AlwaysOff.
	_, okSpan := exp.StartSpan(context.Background(), "healthy.op", nil)
	okSpan.End()

	// Errored span should be promoted to Sampled and exported.
	_, errSpan := exp.StartSpan(context.Background(), "failed.op", nil)
	errSpan.SetError(errors.New("boom"))
	errSpan.End()

	spans := memExp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 exported span (error only), got %d", len(spans))
	}
	if spans[0].Name != "failed.op" {
		t.Errorf("exported span name = %q, want %q", spans[0].Name, "failed.op")
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("exported span status = %v, want Error", spans[0].Status.Code)
	}
	if !spans[0].SpanContext.IsSampled() {
		t.Error("expected promoted error span to carry Sampled flag")
	}
}

func TestExporter_DisabledIsNoop(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	cfg.Traces.Enabled = false

	exp, err := NewExporter(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	ctx, span := exp.StartSpan(context.Background(), "noop.op", map[string]string{"k": "v"})
	span.SetAttribute("a", "b")
	span.SetError(errors.New("ignored"))
	span.End()

	if sc := exp.SpanContextFromContext(ctx); sc.IsValid() {
		t.Errorf("expected invalid SpanContext when disabled, got %+v", sc)
	}
}
