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
	app := NewNodeAppService(state, tracker, nil)
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
	app := NewNodeAppService(state, tracker, nil)
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
