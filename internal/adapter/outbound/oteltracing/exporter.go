// Package oteltracing implements the outbound.TraceExporter port using the
// OpenTelemetry Tracing SDK with W3C TraceContext propagation and optional
// error-biased sampling (RFC-0005 §6).
package oteltracing

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerScope = "github.com/esachser/omnimeshcontrol"

// Exporter implements outbound.TraceExporter and outbound.TelemetryShutdown
// backed by a private OpenTelemetry TracerProvider and W3C propagator.
type Exporter struct {
	enabled    bool
	provider   *sdktrace.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	logger     *slog.Logger
}

// NewExporter initializes a trace Exporter from cfg, configuring OTLP gRPC/HTTP
// exporters, sampling, and W3C context propagation.
func NewExporter(ctx context.Context, cfg model.TelemetryConfig, logger *slog.Logger) (*Exporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Traces.Enabled {
		return newDisabledExporter(logger), nil
	}

	res, err := buildResource(cfg.Resource)
	if err != nil {
		return nil, fmt.Errorf("oteltracing: build resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(buildSampler(cfg.Traces)),
	}

	for _, expCfg := range cfg.Traces.Exporters {
		var spanExp sdktrace.SpanExporter
		switch expCfg.Type {
		case model.ExporterOTLPGRPC:
			spanExp, err = newOTLPGRPCExporter(ctx, expCfg)
		case model.ExporterOTLPHTTP:
			spanExp, err = newOTLPHTTPExporter(ctx, expCfg)
		default:
			return nil, fmt.Errorf("oteltracing: unsupported exporter type %q", expCfg.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("oteltracing: create %s exporter: %w", expCfg.Type, err)
		}
		var proc sdktrace.SpanProcessor = sdktrace.NewBatchSpanProcessor(spanExp)
		if cfg.Traces.AlwaysSampleErrors {
			proc = &errorSamplingSpanProcessor{next: proc}
		}
		opts = append(opts, sdktrace.WithSpanProcessor(proc))
	}

	provider := sdktrace.NewTracerProvider(opts...)
	return newExporterFromProvider(provider, logger), nil
}

// NewExporterWithSpanExporter builds an Exporter backed by a caller-supplied
// sdktrace.SpanExporter (such as tracetest.NewInMemoryExporter for tests).
func NewExporterWithSpanExporter(cfg model.TelemetryConfig, exp sdktrace.SpanExporter, syncExport bool, logger *slog.Logger) (*Exporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Traces.Enabled {
		return newDisabledExporter(logger), nil
	}

	res, err := buildResource(cfg.Resource)
	if err != nil {
		return nil, fmt.Errorf("oteltracing: build resource: %w", err)
	}

	var proc sdktrace.SpanProcessor
	if syncExport {
		proc = sdktrace.NewSimpleSpanProcessor(exp)
	} else {
		proc = sdktrace.NewBatchSpanProcessor(exp)
	}
	if cfg.Traces.AlwaysSampleErrors {
		proc = &errorSamplingSpanProcessor{next: proc}
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(buildSampler(cfg.Traces)),
		sdktrace.WithSpanProcessor(proc),
	)
	return newExporterFromProvider(provider, logger), nil
}

func newDisabledExporter(logger *slog.Logger) *Exporter {
	return &Exporter{
		enabled: false,
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		logger: logger,
	}
}

func newExporterFromProvider(provider *sdktrace.TracerProvider, logger *slog.Logger) *Exporter {
	return &Exporter{
		enabled:  true,
		provider: provider,
		tracer:   provider.Tracer(tracerScope),
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		logger: logger,
	}
}

// StartSpan begins a new span with the given name and optional string attributes.
func (e *Exporter) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, outbound.SpanHandle) {
	if !e.enabled {
		return ctx, noopSpanHandle{}
	}
	var opts []trace.SpanStartOption
	if len(attrs) > 0 {
		opts = append(opts, trace.WithAttributes(mapToAttributes(attrs)...))
	}
	ctx, span := e.tracer.Start(ctx, name, opts...)
	return ctx, &otelSpanHandle{span: span}
}

// InjectContext writes the active W3C trace context into carrier.
func (e *Exporter) InjectContext(ctx context.Context, carrier outbound.PropagationCarrier) error {
	if carrier == nil {
		return fmt.Errorf("oteltracing: propagation carrier is nil")
	}
	e.propagator.Inject(ctx, carrier)
	return nil
}

// ExtractContext reads W3C trace context from carrier into a derived context.
func (e *Exporter) ExtractContext(ctx context.Context, carrier outbound.PropagationCarrier) context.Context {
	if carrier == nil {
		return ctx
	}
	return e.propagator.Extract(ctx, carrier)
}

// SpanContextFromContext converts the active OpenTelemetry SpanContext into a
// domain model.SpanContext.
func (e *Exporter) SpanContextFromContext(ctx context.Context) model.SpanContext {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return model.SpanContext{}
	}
	return model.SpanContext{
		TraceID:    sc.TraceID().String(),
		SpanID:     sc.SpanID().String(),
		TraceFlags: byte(sc.TraceFlags()),
		Remote:     sc.IsRemote(),
	}
}

// Shutdown flushes and shuts down the underlying TracerProvider.
func (e *Exporter) Shutdown(ctx context.Context) error {
	if e.provider == nil {
		return nil
	}
	return e.provider.Shutdown(ctx)
}

type otelSpanHandle struct {
	span trace.Span
}

func (h *otelSpanHandle) SetAttribute(key, value string) {
	h.span.SetAttributes(attribute.String(key, value))
}

func (h *otelSpanHandle) SetError(err error) {
	if err == nil {
		return
	}
	h.span.RecordError(err)
	h.span.SetStatus(codes.Error, err.Error())
}

func (h *otelSpanHandle) End() {
	h.span.End()
}

type noopSpanHandle struct{}

func (noopSpanHandle) SetAttribute(_, _ string) {}
func (noopSpanHandle) SetError(_ error)         {}
func (noopSpanHandle) End()                     {}

// errorSamplingSpanProcessor wraps a downstream SpanProcessor so that spans
// recorded with AlwaysRecord (RecordOnly) are promoted to Sampled if they end
// with codes.Error, while healthy unsampled spans are dropped before export.
type errorSamplingSpanProcessor struct {
	next sdktrace.SpanProcessor
}

func (p *errorSamplingSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

func (p *errorSamplingSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	if s.SpanContext().IsSampled() {
		p.next.OnEnd(s)
		return
	}
	if s.Status().Code == codes.Error {
		p.next.OnEnd(sampledSpan{ReadOnlySpan: s})
	}
}

func (p *errorSamplingSpanProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

func (p *errorSamplingSpanProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}

type sampledSpan struct {
	sdktrace.ReadOnlySpan
}

func (s sampledSpan) SpanContext() trace.SpanContext {
	return s.ReadOnlySpan.SpanContext().WithTraceFlags(
		s.ReadOnlySpan.SpanContext().TraceFlags() | trace.FlagsSampled,
	)
}

func buildSampler(cfg model.TracesConfig) sdktrace.Sampler {
	var base sdktrace.Sampler
	switch cfg.Sampler {
	case model.SamplerAlwaysOn:
		base = sdktrace.AlwaysSample()
	case model.SamplerAlwaysOff:
		base = sdktrace.NeverSample()
	case model.SamplerTraceIDRatio:
		base = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	default:
		base = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))
	}
	if cfg.AlwaysSampleErrors {
		return sdktrace.AlwaysRecord(base)
	}
	return base
}

func buildResource(cfg model.ResourceConfig) (*resource.Resource, error) {
	attrs := make([]attribute.KeyValue, 0, 4+len(cfg.Attributes))
	if cfg.ServiceName != "" {
		attrs = append(attrs, semconv.ServiceName(cfg.ServiceName))
	}
	if cfg.ServiceNamespace != "" {
		attrs = append(attrs, semconv.ServiceNamespace(cfg.ServiceNamespace))
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentName(cfg.Environment))
	}
	if len(cfg.Attributes) > 0 {
		keys := make([]string, 0, len(cfg.Attributes))
		for k := range cfg.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			attrs = append(attrs, attribute.String(k, cfg.Attributes[k]))
		}
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...), nil
}

func mapToAttributes(attrs map[string]string) []attribute.KeyValue {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]attribute.KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, attribute.String(k, attrs[k]))
	}
	return out
}

func newOTLPGRPCExporter(ctx context.Context, expCfg model.ExporterConfig) (sdktrace.SpanExporter, error) {
	opts := make([]otlptracegrpc.Option, 0, 3)
	if strings.Contains(expCfg.Target, "://") {
		opts = append(opts, otlptracegrpc.WithEndpointURL(expCfg.Target))
	} else {
		opts = append(opts, otlptracegrpc.WithEndpoint(expCfg.Target))
	}
	if expCfg.Insecure || strings.HasPrefix(expCfg.Target, "http://") {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	if len(expCfg.Headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(expCfg.Headers))
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newOTLPHTTPExporter(ctx context.Context, expCfg model.ExporterConfig) (sdktrace.SpanExporter, error) {
	opts := make([]otlptracehttp.Option, 0, 3)
	if strings.Contains(expCfg.Target, "://") {
		opts = append(opts, otlptracehttp.WithEndpointURL(expCfg.Target))
	} else {
		opts = append(opts, otlptracehttp.WithEndpoint(expCfg.Target))
	}
	if expCfg.Insecure || strings.HasPrefix(expCfg.Target, "http://") {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(expCfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(expCfg.Headers))
	}
	return otlptracehttp.New(ctx, opts...)
}

// Compile-time interface checks.
var (
	_ outbound.TraceExporter     = (*Exporter)(nil)
	_ outbound.TelemetryShutdown = (*Exporter)(nil)
)
