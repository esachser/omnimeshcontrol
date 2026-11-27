package grpcadapter

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grpcMetadataCarrier struct {
	md metadata.MD
}

func (c grpcMetadataCarrier) Get(key string) string {
	vals := c.md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (c grpcMetadataCarrier) Set(key, value string) {
	c.md.Set(key, value)
}

func (c grpcMetadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.md))
	for k := range c.md {
		keys = append(keys, k)
	}
	return keys
}

// UnaryServerInterceptor returns a gRPC unary interceptor that extracts W3C
// trace context, starts a server span, and records RFC-0005 §4.9 gRPC metrics.
func UnaryServerInterceptor(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) grpc.UnaryServerInterceptor {
	m := telemetry.EnsureMetrics(metrics)
	t := telemetry.EnsureTracer(tracer)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = extractIncomingContext(ctx, t)
		rpcService, rpcMethod := splitGRPCMethod(info.FullMethod)
		ctx, span := t.StartSpan(ctx, info.FullMethod, map[string]string{
			"rpc.system":  "grpc",
			"rpc.service": rpcService,
			"rpc.method":  rpcMethod,
		})
		defer span.End()

		start := time.Now()
		resp, err := handler(ctx, req)
		durationMs := float64(time.Since(start).Microseconds()) / 1000.0

		code := status.Code(err)
		span.SetAttribute("rpc.grpc.status_code", code.String())
		if err != nil {
			span.SetError(err)
		}

		m.IncrCounter(ctx, model.MetricCPGRPCRequestsTotal, map[string]string{
			"method":      info.FullMethod,
			"status_code": code.String(),
		})
		m.RecordHistogram(ctx, model.MetricCPGRPCRequestsDurationMs, durationMs, map[string]string{
			"method": info.FullMethod,
		})

		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream interceptor that extracts W3C
// trace context, tracks active streams, and records RFC-0005 §4.9 gRPC metrics.
func StreamServerInterceptor(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) grpc.StreamServerInterceptor {
	m := telemetry.EnsureMetrics(metrics)
	t := telemetry.EnsureTracer(tracer)
	var activeStreams atomic.Int64

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := extractIncomingContext(ss.Context(), t)
		rpcService, rpcMethod := splitGRPCMethod(info.FullMethod)
		ctx, span := t.StartSpan(ctx, info.FullMethod, map[string]string{
			"rpc.system":  "grpc",
			"rpc.service": rpcService,
			"rpc.method":  rpcMethod,
		})
		defer span.End()

		active := activeStreams.Add(1)
		m.SetGauge(ctx, model.MetricCPGRPCStreamsActive, float64(active), nil)
		defer func() {
			remaining := activeStreams.Add(-1)
			m.SetGauge(context.Background(), model.MetricCPGRPCStreamsActive, float64(remaining), nil)
		}()

		start := time.Now()
		wrapped := &tracedServerStream{ServerStream: ss, ctx: ctx}
		err := handler(srv, wrapped)
		durationMs := float64(time.Since(start).Microseconds()) / 1000.0

		code := status.Code(err)
		span.SetAttribute("rpc.grpc.status_code", code.String())
		if err != nil {
			span.SetError(err)
		}

		m.IncrCounter(ctx, model.MetricCPGRPCRequestsTotal, map[string]string{
			"method":      info.FullMethod,
			"status_code": code.String(),
		})
		m.RecordHistogram(ctx, model.MetricCPGRPCRequestsDurationMs, durationMs, map[string]string{
			"method": info.FullMethod,
		})

		return err
	}
}

type tracedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tracedServerStream) Context() context.Context {
	return s.ctx
}

func extractIncomingContext(ctx context.Context, tracer outbound.TraceExporter) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	return tracer.ExtractContext(ctx, grpcMetadataCarrier{md: md})
}

func splitGRPCMethod(fullMethod string) (serviceName, methodName string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return trimmed, ""
	}
	return trimmed[:idx], trimmed[idx+1:]
}
