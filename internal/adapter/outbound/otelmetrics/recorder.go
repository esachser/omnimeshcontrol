// Package otelmetrics implements the outbound.MetricsRecorder port using the
// OpenTelemetry Metrics SDK with support for Prometheus /metrics scraping and
// OTLP (gRPC and HTTP) export, plus RFC-0005 §10 cardinality controls.
package otelmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

const instrumentationScope = "github.com/esachser/omnimeshcontrol"

// Recorder implements outbound.MetricsRecorder and outbound.TelemetryShutdown
// backed by a private OpenTelemetry MeterProvider.
type Recorder struct {
	enabled    bool
	provider   *sdkmetric.MeterProvider
	meter      metric.Meter
	handler    http.Handler
	logger     *slog.Logger
	maxCard    int
	dropLabels map[string]map[string]struct{}

	instMu     sync.RWMutex
	counters   map[string]metric.Int64Counter
	gauges     map[string]metric.Float64Gauge
	histograms map[string]metric.Float64Histogram

	cardMu     sync.Mutex
	cardSeen   map[string]map[string]struct{}
	cardWarned map[string]bool
}

// NewRecorder initializes a Recorder from cfg, configuring all requested
// exporters (Prometheus pull and/or OTLP gRPC/HTTP push) and cardinality views.
func NewRecorder(ctx context.Context, cfg model.TelemetryConfig, logger *slog.Logger) (*Recorder, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Metrics.Enabled {
		return newDisabledRecorder(logger), nil
	}

	exporters := cfg.Metrics.Exporters
	if len(exporters) == 0 {
		exporters = []model.ExporterConfig{{Type: model.ExporterPrometheus}}
	}

	var (
		opts    []sdkmetric.Option
		handler http.Handler
	)

	res, err := buildResource(cfg.Resource)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: build resource: %w", err)
	}
	opts = append(opts, sdkmetric.WithResource(res))

	for _, view := range buildCardinalityViews(cfg.Metrics.Cardinality) {
		opts = append(opts, sdkmetric.WithView(view))
	}

	interval := cfg.Metrics.ExportInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}

	for _, expCfg := range exporters {
		switch expCfg.Type {
		case model.ExporterPrometheus:
			reg := prometheus.NewRegistry()
			promExp, err := otelprom.New(otelprom.WithRegisterer(reg))
			if err != nil {
				return nil, fmt.Errorf("otelmetrics: create prometheus exporter: %w", err)
			}
			opts = append(opts, sdkmetric.WithReader(promExp))
			handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{
				EnableOpenMetrics: true,
			})
		case model.ExporterOTLPGRPC:
			exp, err := newOTLPGRPCExporter(ctx, expCfg)
			if err != nil {
				return nil, fmt.Errorf("otelmetrics: create otlp-grpc exporter: %w", err)
			}
			opts = append(opts, sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval)),
			))
		case model.ExporterOTLPHTTP:
			exp, err := newOTLPHTTPExporter(ctx, expCfg)
			if err != nil {
				return nil, fmt.Errorf("otelmetrics: create otlp-http exporter: %w", err)
			}
			opts = append(opts, sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval)),
			))
		default:
			return nil, fmt.Errorf("otelmetrics: unsupported exporter type %q", expCfg.Type)
		}
	}

	provider := sdkmetric.NewMeterProvider(opts...)
	return newRecorderFromProvider(cfg, provider, handler, logger), nil
}

// NewRecorderWithReader constructs a Recorder wired to a caller-supplied
// sdkmetric.Reader (for example, sdkmetric.NewManualReader in tests).
func NewRecorderWithReader(cfg model.TelemetryConfig, reader sdkmetric.Reader, logger *slog.Logger) (*Recorder, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Metrics.Enabled {
		return newDisabledRecorder(logger), nil
	}

	res, err := buildResource(cfg.Resource)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: build resource: %w", err)
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	}
	for _, view := range buildCardinalityViews(cfg.Metrics.Cardinality) {
		opts = append(opts, sdkmetric.WithView(view))
	}

	provider := sdkmetric.NewMeterProvider(opts...)
	return newRecorderFromProvider(cfg, provider, nil, logger), nil
}

func newDisabledRecorder(logger *slog.Logger) *Recorder {
	return &Recorder{
		enabled:    false,
		logger:     logger,
		counters:   make(map[string]metric.Int64Counter),
		gauges:     make(map[string]metric.Float64Gauge),
		histograms: make(map[string]metric.Float64Histogram),
	}
}

func newRecorderFromProvider(cfg model.TelemetryConfig, provider *sdkmetric.MeterProvider, handler http.Handler, logger *slog.Logger) *Recorder {
	dropLabels := make(map[string]map[string]struct{}, len(cfg.Metrics.Cardinality.DropLabels))
	for metricName, labels := range cfg.Metrics.Cardinality.DropLabels {
		set := make(map[string]struct{}, len(labels))
		for _, l := range labels {
			set[l] = struct{}{}
		}
		dropLabels[metricName] = set
	}

	return &Recorder{
		enabled:    true,
		provider:   provider,
		meter:      provider.Meter(instrumentationScope),
		handler:    handler,
		logger:     logger,
		maxCard:    cfg.Metrics.Cardinality.MaxLabelCardinality,
		dropLabels: dropLabels,
		counters:   make(map[string]metric.Int64Counter),
		gauges:     make(map[string]metric.Float64Gauge),
		histograms: make(map[string]metric.Float64Histogram),
		cardSeen:   make(map[string]map[string]struct{}),
		cardWarned: make(map[string]bool),
	}
}

// Handler returns the HTTP handler serving Prometheus / OpenMetrics format,
// or nil if the Prometheus exporter is not configured or metrics are disabled.
func (r *Recorder) Handler() http.Handler {
	if !r.enabled {
		return nil
	}
	return r.handler
}

// IncrCounter increments a monotonic counter metric by 1.
func (r *Recorder) IncrCounter(ctx context.Context, name string, labels map[string]string) {
	if !r.enabled {
		return
	}
	counter, err := r.getOrCreateCounter(name)
	if err != nil {
		r.logger.Warn("otelmetrics: failed to get counter", "metric", name, "error", err)
		return
	}
	attrs := r.buildAttributes(name, labels)
	counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// SetGauge sets the current value of a gauge metric.
func (r *Recorder) SetGauge(ctx context.Context, name string, value float64, labels map[string]string) {
	if !r.enabled {
		return
	}
	gauge, err := r.getOrCreateGauge(name)
	if err != nil {
		r.logger.Warn("otelmetrics: failed to get gauge", "metric", name, "error", err)
		return
	}
	attrs := r.buildAttributes(name, labels)
	gauge.Record(ctx, value, metric.WithAttributes(attrs...))
}

// RecordHistogram records an observation into a histogram metric.
func (r *Recorder) RecordHistogram(ctx context.Context, name string, value float64, labels map[string]string) {
	if !r.enabled {
		return
	}
	hist, err := r.getOrCreateHistogram(name)
	if err != nil {
		r.logger.Warn("otelmetrics: failed to get histogram", "metric", name, "error", err)
		return
	}
	attrs := r.buildAttributes(name, labels)
	hist.Record(ctx, value, metric.WithAttributes(attrs...))
}

// Shutdown flushes and closes the underlying MeterProvider.
func (r *Recorder) Shutdown(ctx context.Context) error {
	if r.provider == nil {
		return nil
	}
	return r.provider.Shutdown(ctx)
}

func (r *Recorder) getOrCreateCounter(name string) (metric.Int64Counter, error) {
	r.instMu.RLock()
	c, ok := r.counters[name]
	r.instMu.RUnlock()
	if ok {
		return c, nil
	}

	r.instMu.Lock()
	defer r.instMu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c, nil
	}
	c, err := r.meter.Int64Counter(name)
	if err != nil {
		return nil, err
	}
	r.counters[name] = c
	return c, nil
}

func (r *Recorder) getOrCreateGauge(name string) (metric.Float64Gauge, error) {
	r.instMu.RLock()
	g, ok := r.gauges[name]
	r.instMu.RUnlock()
	if ok {
		return g, nil
	}

	r.instMu.Lock()
	defer r.instMu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g, nil
	}
	g, err := r.meter.Float64Gauge(name)
	if err != nil {
		return nil, err
	}
	r.gauges[name] = g
	return g, nil
}

func (r *Recorder) getOrCreateHistogram(name string) (metric.Float64Histogram, error) {
	r.instMu.RLock()
	h, ok := r.histograms[name]
	r.instMu.RUnlock()
	if ok {
		return h, nil
	}

	r.instMu.Lock()
	defer r.instMu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h, nil
	}
	h, err := r.meter.Float64Histogram(name)
	if err != nil {
		return nil, err
	}
	r.histograms[name] = h
	return h, nil
}

func (r *Recorder) buildAttributes(metricName string, labels map[string]string) []attribute.KeyValue {
	if len(labels) == 0 {
		return nil
	}
	dropped := r.dropLabels[metricName]
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if _, skip := dropped[k]; skip {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)

	attrs := make([]attribute.KeyValue, 0, len(keys))
	var sig strings.Builder
	for i, k := range keys {
		v := labels[k]
		attrs = append(attrs, attribute.String(k, v))
		if r.maxCard > 0 {
			if i > 0 {
				sig.WriteByte('|')
			}
			sig.WriteString(k)
			sig.WriteByte('=')
			sig.WriteString(v)
		}
	}

	if r.maxCard > 0 {
		r.trackCardinality(metricName, sig.String())
	}

	return attrs
}

func (r *Recorder) trackCardinality(metricName, signature string) {
	r.cardMu.Lock()
	defer r.cardMu.Unlock()

	if r.cardWarned[metricName] {
		return
	}
	seen, ok := r.cardSeen[metricName]
	if !ok {
		seen = make(map[string]struct{})
		r.cardSeen[metricName] = seen
	}
	seen[signature] = struct{}{}
	if len(seen) > r.maxCard {
		r.cardWarned[metricName] = true
		r.logger.Warn("otelmetrics: metric label cardinality exceeded limit",
			"metric", metricName,
			"limit", r.maxCard,
			"observed", len(seen),
		)
	}
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

func buildCardinalityViews(card model.CardinalityConfig) []sdkmetric.View {
	var views []sdkmetric.View

	if len(card.HistogramBuckets) > 0 {
		keys := make([]string, 0, len(card.HistogramBuckets))
		for k := range card.HistogramBuckets {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			bounds := append([]float64(nil), card.HistogramBuckets[key]...)
			pattern := key
			if key == "duration_ms" {
				pattern = "*.duration_ms"
			}
			views = append(views, sdkmetric.NewView(
				sdkmetric.Instrument{
					Name: pattern,
					Kind: sdkmetric.InstrumentKindHistogram,
				},
				sdkmetric.Stream{
					Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
						Boundaries: bounds,
					},
				},
			))
		}
	}

	if len(card.DropLabels) > 0 {
		names := make([]string, 0, len(card.DropLabels))
		for name := range card.DropLabels {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			dropSet := make(map[string]struct{}, len(card.DropLabels[name]))
			for _, label := range card.DropLabels[name] {
				dropSet[label] = struct{}{}
			}
			views = append(views, sdkmetric.NewView(
				sdkmetric.Instrument{Name: name},
				sdkmetric.Stream{
					AttributeFilter: func(kv attribute.KeyValue) bool {
						_, drop := dropSet[string(kv.Key)]
						return !drop
					},
				},
			))
		}
	}

	return views
}

func newOTLPGRPCExporter(ctx context.Context, expCfg model.ExporterConfig) (sdkmetric.Exporter, error) {
	opts := make([]otlpmetricgrpc.Option, 0, 3)
	if strings.Contains(expCfg.Target, "://") {
		opts = append(opts, otlpmetricgrpc.WithEndpointURL(expCfg.Target))
	} else {
		opts = append(opts, otlpmetricgrpc.WithEndpoint(expCfg.Target))
	}
	if expCfg.Insecure || strings.HasPrefix(expCfg.Target, "http://") {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	if len(expCfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(expCfg.Headers))
	}
	return otlpmetricgrpc.New(ctx, opts...)
}

func newOTLPHTTPExporter(ctx context.Context, expCfg model.ExporterConfig) (sdkmetric.Exporter, error) {
	opts := make([]otlpmetrichttp.Option, 0, 3)
	if strings.Contains(expCfg.Target, "://") {
		opts = append(opts, otlpmetrichttp.WithEndpointURL(expCfg.Target))
	} else {
		opts = append(opts, otlpmetrichttp.WithEndpoint(expCfg.Target))
	}
	if expCfg.Insecure || strings.HasPrefix(expCfg.Target, "http://") {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(expCfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(expCfg.Headers))
	}
	return otlpmetrichttp.New(ctx, opts...)
}

// Compile-time interface checks.
var (
	_ outbound.MetricsRecorder   = (*Recorder)(nil)
	_ outbound.TelemetryShutdown = (*Recorder)(nil)
)
