package etcdstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeLivenessTracker_TrackAndAlive(t *testing.T) {
	tracker := NewNodeLivenessTracker(5*time.Second, nil)

	tracker.TrackNode("node-1")
	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive after TrackNode")
	}
	if tracker.IsAlive("node-2") {
		t.Error("expected node-2 to not be alive (never tracked)")
	}
}

func TestNodeLivenessTracker_GracePeriodExpiry(t *testing.T) {
	var lostNodeID atomic.Value

	tracker := NewNodeLivenessTracker(100*time.Millisecond, nil)
	tracker.RegisterCallback(func(_ context.Context, nodeID string) {
		lostNodeID.Store(nodeID)
	})

	tracker.TrackNode("node-1")
	tracker.OnDisconnect("node-1")

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive after disconnect")
	}

	// Wait for grace period to expire.
	time.Sleep(200 * time.Millisecond)

	val := lostNodeID.Load()
	if val == nil || val.(string) != "node-1" {
		t.Errorf("expected OnNodeLost to be called with node-1, got %v", val)
	}
}

func TestNodeLivenessTracker_ReconnectCancelsGrace(t *testing.T) {
	var lostCalled atomic.Int32

	tracker := NewNodeLivenessTracker(200*time.Millisecond, nil)
	tracker.RegisterCallback(func(_ context.Context, _ string) {
		lostCalled.Add(1)
	})

	tracker.TrackNode("node-1")
	tracker.OnDisconnect("node-1")

	// Reconnect before grace period expires.
	time.Sleep(50 * time.Millisecond)
	tracker.TrackNode("node-1")

	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive after reconnect")
	}

	// Wait past the original grace period.
	time.Sleep(300 * time.Millisecond)

	if lostCalled.Load() != 0 {
		t.Error("expected OnNodeLost to NOT be called after reconnect")
	}
}

func TestNodeLivenessTracker_UntrackNode(t *testing.T) {
	var lostCalled atomic.Int32

	tracker := NewNodeLivenessTracker(100*time.Millisecond, nil)
	tracker.RegisterCallback(func(_ context.Context, _ string) {
		lostCalled.Add(1)
	})

	tracker.TrackNode("node-1")
	tracker.UntrackNode("node-1")

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive after untrack")
	}

	// Ensure no grace period fires.
	time.Sleep(200 * time.Millisecond)
	if lostCalled.Load() != 0 {
		t.Error("expected OnNodeLost to NOT be called after untrack")
	}
}

func TestNodeLivenessTracker_ConnectedNodes(t *testing.T) {
	tracker := NewNodeLivenessTracker(5*time.Second, nil)

	tracker.TrackNode("node-1")
	tracker.TrackNode("node-2")
	tracker.TrackNode("node-3")

	connected := tracker.ConnectedNodes()
	if len(connected) != 3 {
		t.Errorf("expected 3 connected nodes, got %d", len(connected))
	}

	tracker.OnDisconnect("node-2")
	connected = tracker.ConnectedNodes()
	if len(connected) != 2 {
		t.Errorf("expected 2 connected nodes after disconnect, got %d", len(connected))
	}
}
