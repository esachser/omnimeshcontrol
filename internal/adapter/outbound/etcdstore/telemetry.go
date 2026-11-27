package etcdstore

import (
	"context"
	"errors"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type noopMetrics struct{}

func (noopMetrics) IncrCounter(_ context.Context, _ string, _ map[string]string) {}
func (noopMetrics) SetGauge(_ context.Context, _ string, _ float64, _ map[string]string) {
}
func (noopMetrics) RecordHistogram(_ context.Context, _ string, _ float64, _ map[string]string) {
}

type noopTracer struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string, _ map[string]string) (context.Context, outbound.SpanHandle) {
	return ctx, noopSpan{}
}
func (noopTracer) InjectContext(_ context.Context, _ outbound.PropagationCarrier) error { return nil }
func (noopTracer) ExtractContext(ctx context.Context, _ outbound.PropagationCarrier) context.Context {
	return ctx
}
func (noopTracer) SpanContextFromContext(_ context.Context) model.SpanContext {
	return model.SpanContext{}
}

type noopSpan struct{}

func (noopSpan) SetAttribute(_, _ string) {}
func (noopSpan) SetError(_ error)         {}
func (noopSpan) End()                     {}

func ensureMetrics(m outbound.MetricsRecorder) outbound.MetricsRecorder {
	if m == nil {
		return noopMetrics{}
	}
	return m
}

func ensureTracer(t outbound.TraceExporter) outbound.TraceExporter {
	if t == nil {
		return noopTracer{}
	}
	return t
}

func startEtcdSpan(ctx context.Context, tracer outbound.TraceExporter, operation, key string) (context.Context, outbound.SpanHandle) {
	attrs := map[string]string{
		"db.system":    "etcd",
		"db.operation": operation,
	}
	if key != "" {
		attrs["etcd.key"] = key
	}
	return ensureTracer(tracer).StartSpan(ctx, "etcd."+operation, attrs)
}

func recordEtcdOp(ctx context.Context, metrics outbound.MetricsRecorder, span outbound.SpanHandle, operation string, start time.Time, err error) {
	m := ensureMetrics(metrics)
	durationMs := float64(time.Since(start).Microseconds()) / 1000.0
	labels := map[string]string{"operation": operation}
	m.IncrCounter(ctx, model.MetricCPEtcdOperationsTotal, labels)
	m.RecordHistogram(ctx, model.MetricCPEtcdOperationsDurationMs, durationMs, labels)
	if err != nil {
		if span != nil {
			span.SetError(err)
		}
		m.IncrCounter(ctx, model.MetricCPEtcdOperationsErrors, map[string]string{
			"operation":  operation,
			"error_type": classifyEtcdError(err),
		})
	}
}

func classifyEtcdError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded:
		return "timeout"
	case errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled:
		return "canceled"
	case errors.Is(err, domain.ErrResourceNotFound) || errors.Is(err, domain.ErrNodeNotFound) || errors.Is(err, domain.ErrServiceNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrRevisionConflict) || errors.Is(err, domain.ErrIPConflict):
		return "conflict"
	case status.Code(err) == codes.Unavailable:
		return "unavailable"
	default:
		return "other"
	}
}
