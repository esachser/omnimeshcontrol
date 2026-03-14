package application

import (
	"context"
	"net/netip"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestNodeAppService_RegisterAndDeregister(t *testing.T) {
	state := newMockStateStore()
	tracker := newMockNodeTracker()
	app := NewNodeAppService(state, tracker, newMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-1",
		Platform: model.NodePlatformLinux,
		Addr:     netip.MustParseAddrPort("10.0.0.1:9090"),
	}

	// Register.
	if err := app.RegisterNode(ctx, node); err != nil {
		t.Fatalf("RegisterNode() error = %v", err)
	}

	// Verify node is tracked.
	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive in tracker")
	}

	// Verify node is persisted.
	state.mu.Lock()
	_, persisted := state.nodes["node-1"]
	state.mu.Unlock()
	if !persisted {
		t.Error("expected node-1 to be persisted in state store")
	}

	// Deregister.
	if err := app.DeregisterNode(ctx, "node-1"); err != nil {
		t.Fatalf("DeregisterNode() error = %v", err)
	}

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive after deregister")
	}
}

func TestNodeAppService_StreamDisconnectAndReconnect(t *testing.T) {
	state := newMockStateStore()
	tracker := newMockNodeTracker()
	app := NewNodeAppService(state, tracker, newMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-2",
		Platform: model.NodePlatformWindows,
	}
	app.RegisterNode(ctx, node)

	// Disconnect.
	if err := app.OnStreamDisconnect(ctx, "node-2"); err != nil {
		t.Fatalf("OnStreamDisconnect() error = %v", err)
	}
	if tracker.IsAlive("node-2") {
		t.Error("expected node-2 to not be alive after disconnect")
	}

	// Reconnect.
	if err := app.OnStreamReconnect(ctx, "node-2"); err != nil {
		t.Fatalf("OnStreamReconnect() error = %v", err)
	}
	if !tracker.IsAlive("node-2") {
		t.Error("expected node-2 to be alive after reconnect")
	}
}

func TestNodeAppService_UpdateNodeServices(t *testing.T) {
	state := newMockStateStore()
	tracker := newMockNodeTracker()
	app := NewNodeAppService(state, tracker, newMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-3",
		Platform: model.NodePlatformLinux,
	}
	app.RegisterNode(ctx, node)

	svc1 := model.ServiceID{Namespace: "default", Name: "svc1"}
	svc2 := model.ServiceID{Namespace: "default", Name: "svc2"}
	svc3 := model.ServiceID{Namespace: "default", Name: "svc3"}

	// Initial add
	err := app.UpdateNodeServices(ctx, "node-3", []model.ServiceID{svc1, svc2}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedNode, _ := state.GetNode(ctx, "node-3")
	if len(updatedNode.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(updatedNode.Services))
	}

	// Add svc3, remove svc1
	err = app.UpdateNodeServices(ctx, "node-3", []model.ServiceID{svc3}, []model.ServiceID{svc1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedNode, _ = state.GetNode(ctx, "node-3")
	if len(updatedNode.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(updatedNode.Services))
	}

	foundSvc2, foundSvc3 := false, false
	for _, s := range updatedNode.Services {
		if s == svc2 {
			foundSvc2 = true
		}
		if s == svc3 {
			foundSvc3 = true
		}
	}
	if !foundSvc2 || !foundSvc3 {
		t.Errorf("expected services to contain svc2 and svc3, got %v", updatedNode.Services)
	}
}
