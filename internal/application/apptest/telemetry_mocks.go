package apptest

import (
	"context"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// RecordedMetric captures a single counter, gauge, or histogram call.
type RecordedMetric struct {
	Kind   string // "counter", "gauge", "histogram"
	Name   string
	Value  float64
	Labels map[string]string
}

// RecordingMetricsRecorder is a thread-safe test double for outbound.MetricsRecorder.
type RecordingMetricsRecorder struct {
	mu      sync.Mutex
	Records []RecordedMetric
}

func NewRecordingMetricsRecorder() *RecordingMetricsRecorder {
	return &RecordingMetricsRecorder{}
}

func (r *RecordingMetricsRecorder) IncrCounter(_ context.Context, name string, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Records = append(r.Records, RecordedMetric{
		Kind:   "counter",
		Name:   name,
		Value:  1,
		Labels: cloneStringMap(labels),
	})
}

func (r *RecordingMetricsRecorder) SetGauge(_ context.Context, name string, value float64, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Records = append(r.Records, RecordedMetric{
		Kind:   "gauge",
		Name:   name,
		Value:  value,
		Labels: cloneStringMap(labels),
	})
}

func (r *RecordingMetricsRecorder) RecordHistogram(_ context.Context, name string, value float64, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Records = append(r.Records, RecordedMetric{
		Kind:   "histogram",
		Name:   name,
		Value:  value,
		Labels: cloneStringMap(labels),
	})
}

func (r *RecordingMetricsRecorder) CountByName(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, rec := range r.Records {
		if rec.Name == name {
			count++
		}
	}
	return count
}

func (r *RecordingMetricsRecorder) FindByName(name string) []RecordedMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []RecordedMetric
	for _, rec := range r.Records {
		if rec.Name == name {
			out = append(out, rec)
		}
	}
	return out
}

// RecordedSpan captures a span lifecycle for unit tests.
type RecordedSpan struct {
	Name       string
	Attributes map[string]string
	Err        error
	Ended      bool
}

// RecordingTraceExporter is a thread-safe test double for outbound.TraceExporter.
type RecordingTraceExporter struct {
	mu    sync.Mutex
	Spans []*RecordedSpan
}

func NewRecordingTraceExporter() *RecordingTraceExporter {
	return &RecordingTraceExporter{}
}

func (t *RecordingTraceExporter) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, outbound.SpanHandle) {
	span := &RecordedSpan{
		Name:       name,
		Attributes: cloneStringMap(attrs),
	}
	if span.Attributes == nil {
		span.Attributes = make(map[string]string)
	}
	t.mu.Lock()
	t.Spans = append(t.Spans, span)
	t.mu.Unlock()
	return ctx, &recordingSpanHandle{mu: &t.mu, span: span}
}

func (t *RecordingTraceExporter) InjectContext(_ context.Context, carrier outbound.PropagationCarrier) error {
	if carrier != nil {
		carrier.Set("traceparent", "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01")
	}
	return nil
}

func (t *RecordingTraceExporter) ExtractContext(ctx context.Context, _ outbound.PropagationCarrier) context.Context {
	return ctx
}

func (t *RecordingTraceExporter) SpanContextFromContext(_ context.Context) model.SpanContext {
	return model.SpanContext{
		TraceID:    "0102030405060708090a0b0c0d0e0f10",
		SpanID:     "0102030405060708",
		TraceFlags: 0x01,
	}
}

func (t *RecordingTraceExporter) SpanNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, 0, len(t.Spans))
	for _, s := range t.Spans {
		names = append(names, s.Name)
	}
	return names
}

type recordingSpanHandle struct {
	mu   *sync.Mutex
	span *RecordedSpan
}

func (h *recordingSpanHandle) SetAttribute(key, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.span.Attributes[key] = value
}

func (h *recordingSpanHandle) SetError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.span.Err = err
}

func (h *recordingSpanHandle) End() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.span.Ended = true
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var (
	_ outbound.MetricsRecorder = (*RecordingMetricsRecorder)(nil)
	_ outbound.TraceExporter   = (*RecordingTraceExporter)(nil)
)
