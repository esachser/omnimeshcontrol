package node

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestNodeAppService_Telemetry(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	routes := apptest.NewMockRouteManager()
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	nodeApp := NewNodeAppService(state, tracker, routes, nil)
	nodeApp.SetTelemetry(metrics, tracer)

	ctx := context.Background()
	err := nodeApp.RegisterNode(ctx, &model.Node{
		ID:       "node-win-1",
		Platform: model.NodePlatformWindows,
		Addr:     "10.0.0.2:15008",
	})
	if err != nil {
		t.Fatalf("RegisterNode() error = %v", err)
	}

	// Simulate lease expiry callback.
	for _, cb := range tracker.Callbacks {
		cb(ctx, "node-win-1")
	}

	disc := metrics.FindByName(model.MetricCPNodesDisconnections)
	if len(disc) != 1 || disc[0].Labels["node_id"] != "node-win-1" || disc[0].Labels["platform"] != "windows" {
		t.Errorf("disconnections = %+v, want node-win-1 / windows", disc)
	}
	if len(tracer.Spans) != 1 || !tracer.Spans[0].Ended {
		t.Errorf("expected 1 ended span, got %+v", tracer.Spans)
	}
}
