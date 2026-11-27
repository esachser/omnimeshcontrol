package telemetry

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// NoopMetricsRecorder is a no-op implementation of outbound.MetricsRecorder.
type NoopMetricsRecorder struct{}

// IncrCounter does nothing.
func (NoopMetricsRecorder) IncrCounter(_ context.Context, _ string, _ map[string]string) {}

// SetGauge does nothing.
func (NoopMetricsRecorder) SetGauge(_ context.Context, _ string, _ float64, _ map[string]string) {}

// RecordHistogram does nothing.
func (NoopMetricsRecorder) RecordHistogram(_ context.Context, _ string, _ float64, _ map[string]string) {
}

// NoopTraceExporter is a no-op implementation of outbound.TraceExporter.
type NoopTraceExporter struct{}

// StartSpan returns the unchanged context and a no-op SpanHandle.
func (NoopTraceExporter) StartSpan(ctx context.Context, _ string, _ map[string]string) (context.Context, outbound.SpanHandle) {
	return ctx, noopSpanHandle{}
}

// InjectContext does nothing and returns nil.
func (NoopTraceExporter) InjectContext(_ context.Context, _ outbound.PropagationCarrier) error {
	return nil
}

// ExtractContext returns ctx unchanged.
func (NoopTraceExporter) ExtractContext(ctx context.Context, _ outbound.PropagationCarrier) context.Context {
	return ctx
}

// SpanContextFromContext returns an empty SpanContext.
func (NoopTraceExporter) SpanContextFromContext(_ context.Context) model.SpanContext {
	return model.SpanContext{}
}

type noopSpanHandle struct{}

func (noopSpanHandle) SetAttribute(_, _ string) {}
func (noopSpanHandle) SetError(_ error)         {}
func (noopSpanHandle) End()                     {}

// EnsureMetrics returns m if non-nil, otherwise a NoopMetricsRecorder.
func EnsureMetrics(m outbound.MetricsRecorder) outbound.MetricsRecorder {
	if m == nil {
		return NoopMetricsRecorder{}
	}
	return m
}

// EnsureTracer returns t if non-nil, otherwise a NoopTraceExporter.
func EnsureTracer(t outbound.TraceExporter) outbound.TraceExporter {
	if t == nil {
		return NoopTraceExporter{}
	}
	return t
}

// Compile-time interface checks.
var (
	_ outbound.MetricsRecorder = NoopMetricsRecorder{}
	_ outbound.TraceExporter   = NoopTraceExporter{}
)
