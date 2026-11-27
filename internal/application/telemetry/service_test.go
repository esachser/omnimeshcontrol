package telemetry

import (
	"context"
	"errors"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

type mockShutdown struct {
	called bool
	err    error
}

func (m *mockShutdown) Shutdown(_ context.Context) error {
	m.called = true
	return m.err
}

func TestTelemetryService_ListMetricsTargets(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()

	if _, err := tracker.TrackNode("node-linux"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	if err := state.PersistNode(context.Background(), "node-linux", &model.Node{
		ID:       "node-linux",
		Platform: model.NodePlatformLinux,
		Target:   "dc-east",
		Addr:     "10.0.0.10:15008",
	}, 0); err != nil {
		t.Fatalf("PersistNode() error = %v", err)
	}

	// Track a disconnected node that should not appear in targets.
	if _, err := tracker.TrackNode("node-gone"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	tracker.OnDisconnect("node-gone")

	cfg := model.DefaultTelemetryConfig()
	cfg.Metrics.AgentMetricsPort = 9091

	svc := NewTelemetryService(Config{
		Telemetry:   cfg,
		NodeTracker: tracker,
		StateStore:  state,
	})

	targets, err := svc.ListMetricsTargets(context.Background())
	if err != nil {
		t.Fatalf("ListMetricsTargets() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target group, got %d (%+v)", len(targets), targets)
	}
	if len(targets[0].Targets) != 1 || targets[0].Targets[0] != "10.0.0.10:9091" {
		t.Errorf("Targets = %v, want [10.0.0.10:9091]", targets[0].Targets)
	}
	if targets[0].Labels["node_id"] != "node-linux" || targets[0].Labels["platform"] != "linux" || targets[0].Labels["target"] != "dc-east" {
		t.Errorf("unexpected labels: %+v", targets[0].Labels)
	}
}

func TestTelemetryService_Shutdown(t *testing.T) {
	s1 := &mockShutdown{}
	s2 := &mockShutdown{err: errors.New("flush failed")}

	svc := NewTelemetryService(Config{
		Telemetry: model.DefaultTelemetryConfig(),
		Shutdowns: []outbound.TelemetryShutdown{s1, nil, s2},
	})

	err := svc.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected joined shutdown error")
	}
	if !s1.called || !s2.called {
		t.Error("expected all non-nil shutdowns to be called")
	}
}
