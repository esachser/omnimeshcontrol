package node

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestNodeAppService_RegisterAndDeregister(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	app := NewNodeAppService(state, tracker, apptest.NewMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-1",
		Platform: model.NodePlatformLinux,
		Addr:     "10.0.0.1:9090",
	}

	if err := app.RegisterNode(ctx, node); err != nil {
		t.Fatalf("RegisterNode() error = %v", err)
	}

	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive in tracker")
	}

	_, err := state.GetNode(ctx, "node-1")
	if err != nil {
		t.Error("expected node-1 to be persisted in state store")
	}

	if err := app.DeregisterNode(ctx, "node-1"); err != nil {
		t.Fatalf("DeregisterNode() error = %v", err)
	}

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive after deregister")
	}
}

func TestNodeAppService_StreamDisconnectAndReconnect(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	app := NewNodeAppService(state, tracker, apptest.NewMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-2",
		Platform: model.NodePlatformWindows,
	}
	app.RegisterNode(ctx, node)

	if err := app.OnStreamDisconnect(ctx, "node-2"); err != nil {
		t.Fatalf("OnStreamDisconnect() error = %v", err)
	}
	if tracker.IsAlive("node-2") {
		t.Error("expected node-2 to not be alive after disconnect")
	}

	if err := app.OnStreamReconnect(ctx, "node-2"); err != nil {
		t.Fatalf("OnStreamReconnect() error = %v", err)
	}
	if !tracker.IsAlive("node-2") {
		t.Error("expected node-2 to be alive after reconnect")
	}
}

func TestNodeAppService_UpdateNodeServices(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	app := NewNodeAppService(state, tracker, apptest.NewMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-3",
		Platform: model.NodePlatformLinux,
	}
	app.RegisterNode(ctx, node)

	svc1 := model.ServiceID{Namespace: "default", Name: "svc1"}
	svc2 := model.ServiceID{Namespace: "default", Name: "svc2"}
	svc3 := model.ServiceID{Namespace: "default", Name: "svc3"}

	err := app.UpdateNodeServices(ctx, "node-3", []model.ServiceID{svc1, svc2}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedNode, _ := state.GetNode(ctx, "node-3")
	if len(updatedNode.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(updatedNode.Services))
	}

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

func TestNodeAppService_DrainNode(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	app := NewNodeAppService(state, tracker, apptest.NewMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-drain",
		Platform: model.NodePlatformLinux,
		Addr:     "10.0.0.5:9090",
	}
	if err := app.RegisterNode(ctx, node); err != nil {
		t.Fatalf("RegisterNode() error = %v", err)
	}

	if err := app.DrainNode(ctx, "node-drain"); err != nil {
		t.Fatalf("DrainNode() error = %v", err)
	}

	persisted, err := state.GetNode(ctx, "node-drain")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if persisted.Status != model.NodeStatusDraining {
		t.Errorf("expected status DRAINING, got %v", persisted.Status)
	}

	if tracker.IsAlive("node-drain") {
		t.Error("expected tracker to mark node-drain as not alive after drain")
	}
}

func TestNodeAppService_OnStreamDisconnect_SkipsForDrainingNode(t *testing.T) {
	state := apptest.NewMockStateStore()
	tracker := apptest.NewMockNodeTracker()
	app := NewNodeAppService(state, tracker, apptest.NewMockRouteManager(), nil)
	ctx := context.Background()

	node := &model.Node{
		ID:       "node-draining",
		Platform: model.NodePlatformLinux,
		Status:   model.NodeStatusDraining,
	}
	state.PersistNode(ctx, node.ID, node, 0)
	tracker.TrackNode(node.ID)
	tracker.OnDisconnect(node.ID)

	if err := app.OnStreamDisconnect(ctx, "node-draining"); err != nil {
		t.Fatalf("OnStreamDisconnect() error = %v", err)
	}

	if tracker.IsAlive("node-draining") {
		t.Error("expected node to remain not-alive after redundant OnStreamDisconnect")
	}
}
