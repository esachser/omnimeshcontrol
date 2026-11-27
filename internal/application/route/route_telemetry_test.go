package route

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestRouteAppService_Telemetry(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	dist := apptest.NewMockScopeDistributor()
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	routeApp := NewRouteAppService(resources, state, nil, nil)
	routeApp.SetScopeDistributor(dist)
	routeApp.SetTelemetry(metrics, tracer)

	ctx := context.Background()
	rt := &model.TargetRoute{
		Name:          "r1",
		Namespace:     "prod",
		NodeID:        "node-1",
		TargetService: "api",
	}
	if err := routeApp.Apply(ctx, rt); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := routeApp.Delete(ctx, "prod", "r1", "api", "node-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if got := metrics.CountByName(model.MetricCPRoutesAnnouncementsTotal); got != 1 {
		t.Errorf("announcements = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPRoutesWithdrawalsTotal); got != 1 {
		t.Errorf("withdrawals = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPRoutesDistributionDurationMs); got != 2 {
		t.Errorf("distribution.duration_ms = %d, want 2", got)
	}
	if len(tracer.Spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(tracer.Spans))
	}
}
