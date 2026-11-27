package grpcadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type stubServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *stubServerStream) Context() context.Context { return s.ctx }

func TestUnaryServerInterceptor_RecordsMetricsAndSpan(t *testing.T) {
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	interceptor := UnaryServerInterceptor(metrics, tracer)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"traceparent", "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01",
	))

	info := &grpc.UnaryServerInfo{FullMethod: "/omnimesh.v1.ControlPlaneService/ApplyResource"}
	_, err := interceptor(ctx, "req", info, func(_ context.Context, _ any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor() error = %v", err)
	}

	if got := metrics.CountByName(model.MetricCPGRPCRequestsTotal); got != 1 {
		t.Errorf("grpc.requests.total count = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPGRPCRequestsDurationMs); got != 1 {
		t.Errorf("grpc.requests.duration_ms count = %d, want 1", got)
	}
	if len(tracer.Spans) != 1 || !tracer.Spans[0].Ended {
		t.Fatalf("expected 1 ended span, got %+v", tracer.Spans)
	}
	if tracer.Spans[0].Attributes["rpc.service"] != "omnimesh.v1.ControlPlaneService" {
		t.Errorf("rpc.service = %q", tracer.Spans[0].Attributes["rpc.service"])
	}
	if tracer.Spans[0].Attributes["rpc.method"] != "ApplyResource" {
		t.Errorf("rpc.method = %q", tracer.Spans[0].Attributes["rpc.method"])
	}

	// Error case
	_, err = interceptor(ctx, "req", info, func(_ context.Context, _ any) (any, error) {
		return nil, status.Error(codes.InvalidArgument, "bad input")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	reqs := metrics.FindByName(model.MetricCPGRPCRequestsTotal)
	if len(reqs) != 2 || reqs[1].Labels["status_code"] != codes.InvalidArgument.String() {
		t.Errorf("unexpected second request labels: %+v", reqs)
	}
	if tracer.Spans[1].Err == nil {
		t.Error("expected span error to be recorded")
	}
}

func TestStreamServerInterceptor_TracksActiveStreams(t *testing.T) {
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	interceptor := StreamServerInterceptor(metrics, tracer)
	ss := &stubServerStream{ctx: context.Background()}
	info := &grpc.StreamServerInfo{FullMethod: "/omnimesh.v1.AgentService/AgentStream"}

	err := interceptor(nil, ss, info, func(_ any, stream grpc.ServerStream) error {
		gauges := metrics.FindByName(model.MetricCPGRPCStreamsActive)
		if len(gauges) != 1 || gauges[0].Value != 1 {
			t.Errorf("expected active stream gauge = 1 while running, got %+v", gauges)
		}
		if stream.Context() == nil {
			return errors.New("nil stream context")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream interceptor error = %v", err)
	}

	gauges := metrics.FindByName(model.MetricCPGRPCStreamsActive)
	if len(gauges) != 2 || gauges[1].Value != 0 {
		t.Errorf("expected active stream gauge to return to 0, got %+v", gauges)
	}
	if got := metrics.CountByName(model.MetricCPGRPCRequestsTotal); got != 1 {
		t.Errorf("grpc.requests.total count = %d, want 1", got)
	}
}
