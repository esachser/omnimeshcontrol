package application

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

func TestNodeUpdateService_DrainBroadcastsRouteRemoval(t *testing.T) {
	state := newMockStateStore()
	dist := newMockScopeDistributor()
	svc := NewNodeUpdateService(state, dist, nil)
	ctx := context.Background()

	// Seed the state store with a node and some routes.
	node := &model.Node{
		ID:       "node-drain-1",
		Platform: model.NodePlatformLinux,
		Status:   model.NodeStatusActive,
	}
	state.PersistNode(ctx, node.ID, node, 0)

	route1 := &model.TargetRoute{
		NodeID:        "node-drain-1",
		Name:          "edp1",
		Namespace:     "production",
		TargetService: "payment-service",
	}
	route2 := &model.TargetRoute{
		NodeID:        "node-drain-1",
		Name:          "edp2",
		Namespace:     "production",
		TargetService: "order-service",
	}
	state.PersistTargetRoute(ctx, route1, 0)
	state.PersistTargetRoute(ctx, route2, 0)

	// Simulate a DRAINING node event.
	drainingNode := *node
	drainingNode.Status = model.NodeStatusDraining
	event := outbound.NodeStateEvent{
		Type: outbound.StateEventPut,
		Key:  "/omnimesh/state/nodes/node-drain-1",
		Node: &drainingNode,
	}
	svc.handleEvent(ctx, event)

	// Verify a single broadcast with all routes for the node.
	dist.mu.Lock()
	defer dist.mu.Unlock()

	if len(dist.broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(dist.broadcasts))
	}

	bc := dist.broadcasts[0]
	if bc.Type != model.ScopeUpdateRemove {
		t.Errorf("expected ScopeUpdateRemove, got %v", bc.Type)
	}
	if len(bc.Routes) != 2 {
		t.Errorf("expected 2 routes in broadcast, got %d", len(bc.Routes))
	}
}

func TestNodeUpdateService_IsNodeDraining(t *testing.T) {
	state := newMockStateStore()
	dist := newMockScopeDistributor()
	svc := NewNodeUpdateService(state, dist, nil)
	ctx := context.Background()

	// Initially not draining.
	if svc.IsNodeDraining("node-x") {
		t.Error("expected node-x to not be draining initially")
	}

	// Seed a route so the drain event has something to broadcast.
	route := &model.TargetRoute{
		NodeID:        "node-x",
		Name:          "edp1",
		Namespace:     "default",
		TargetService: "svc-1",
	}
	state.PersistTargetRoute(ctx, route, 0)

	// Simulate drain event.
	drainingNode := &model.Node{ID: "node-x", Status: model.NodeStatusDraining}
	svc.handleEvent(ctx, outbound.NodeStateEvent{
		Type: outbound.StateEventPut,
		Node: drainingNode,
	})

	if !svc.IsNodeDraining("node-x") {
		t.Error("expected node-x to be draining after drain event")
	}

	// Simulate delete event (lease expired).
	svc.handleEvent(ctx, outbound.NodeStateEvent{
		Type: outbound.StateEventDelete,
		Node: drainingNode,
	})

	if svc.IsNodeDraining("node-x") {
		t.Error("expected node-x to not be draining after delete event")
	}
}

func TestNodeUpdateService_IgnoresNonDrainPuts(t *testing.T) {
	state := newMockStateStore()
	dist := newMockScopeDistributor()
	svc := NewNodeUpdateService(state, dist, nil)
	ctx := context.Background()

	// Active node PUT should not trigger a broadcast.
	activeNode := &model.Node{ID: "node-active", Status: model.NodeStatusActive}
	svc.handleEvent(ctx, outbound.NodeStateEvent{
		Type: outbound.StateEventPut,
		Node: activeNode,
	})

	dist.mu.Lock()
	bCount := len(dist.broadcasts)
	dist.mu.Unlock()

	if bCount != 0 {
		t.Errorf("expected 0 broadcasts for active node PUT, got %d", bCount)
	}
}

func TestNodeUpdateService_RunContextCancellation(t *testing.T) {
	state := newMockStateStore()
	dist := newMockScopeDistributor()
	svc := NewNodeUpdateService(state, dist, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := svc.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error on cancelled context, got %v", err)
	}
}

func TestNodeUpdateService_RunNilDeps(t *testing.T) {
	svc := NewNodeUpdateService(nil, nil, nil)

	err := svc.Run(context.Background())
	if err == nil {
		t.Error("expected error with nil state store")
	}

	svc2 := NewNodeUpdateService(newMockStateStore(), nil, nil)
	err = svc2.Run(context.Background())
	if err == nil {
		t.Error("expected error with nil distributor")
	}
}
