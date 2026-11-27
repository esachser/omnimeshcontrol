package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// MetricsRecorder defines the outbound port for recording control plane metrics.
// Application services and instrumented adapters call this interface without
// depending directly on the OpenTelemetry SDK.
type MetricsRecorder interface {
	// IncrCounter increments a monotonic counter metric by 1.
	IncrCounter(ctx context.Context, name string, labels map[string]string)
	// SetGauge sets the current value of a gauge metric.
	SetGauge(ctx context.Context, name string, value float64, labels map[string]string)
	// RecordHistogram records an observation into a histogram metric.
	RecordHistogram(ctx context.Context, name string, value float64, labels map[string]string)
}

// PropagationCarrier abstracts header/metadata maps for trace context propagation.
type PropagationCarrier interface {
	// Get returns the value associated with the passed key, or empty string.
	Get(key string) string
	// Set stores a key-value pair in the carrier.
	Set(key, value string)
	// Keys lists all keys present in the carrier.
	Keys() []string
}

// SpanHandle allows callers to enrich and complete an active trace span.
type SpanHandle interface {
	// SetAttribute attaches a string attribute to the span.
	SetAttribute(key, value string)
	// SetError records an error on the span and marks its status as Error.
	SetError(err error)
	// End completes the span.
	End()
}

// TraceExporter defines the outbound port for creating and propagating trace spans.
type TraceExporter interface {
	// StartSpan begins a new child span under ctx with optional initial attributes.
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanHandle)
	// InjectContext writes the active trace context from ctx into carrier.
	InjectContext(ctx context.Context, carrier PropagationCarrier) error
	// ExtractContext extracts trace context from carrier into a derived context.
	ExtractContext(ctx context.Context, carrier PropagationCarrier) context.Context
	// SpanContextFromContext returns the domain SpanContext attached to ctx.
	SpanContextFromContext(ctx context.Context) model.SpanContext
}

// TelemetryShutdown defines the outbound port for flushing and closing telemetry pipelines.
type TelemetryShutdown interface {
	// Shutdown flushes pending telemetry data and releases resources.
	Shutdown(ctx context.Context) error
}
