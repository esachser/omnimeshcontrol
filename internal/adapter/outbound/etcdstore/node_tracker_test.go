package etcdstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// testTracker creates a NodeLivenessTracker backed by a testcontainers etcd.
// leaseTTL must be >= 1 (etcd minimum). Keep it short (2–5s) for fast tests.
func testTracker(t *testing.T, client *clientv3.Client, leaseTTL int64) *NodeLivenessTracker {
	t.Helper()
	tracker, err := NewNodeLivenessTracker(client, leaseTTL, nil)
	if err != nil {
		t.Fatalf("NewNodeLivenessTracker() error = %v", err)
	}
	t.Cleanup(tracker.Close)
	return tracker
}

func TestNodeLivenessTracker_TrackAndAlive(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	tracker := testTracker(t, client, 30)

	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive after TrackNode")
	}
	if tracker.IsAlive("node-2") {
		t.Error("expected node-2 to not be alive (never tracked)")
	}
}

func TestNodeLivenessTracker_LeaseID(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	tracker := testTracker(t, client, 30)

	if tracker.LeaseID("node-1") != 0 {
		t.Error("LeaseID() for untracked node should be 0")
	}

	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	if tracker.LeaseID("node-1") == 0 {
		t.Error("LeaseID() for tracked node should be non-zero")
	}
}

func TestNodeLivenessTracker_TTLExpiry(t *testing.T) {
	// Use minimum etcd TTL of 2s.
	const leaseTTL = 2

	client, cleanup := testEtcdClient(t)
	defer cleanup()

	var lostNodeID atomic.Value
	tracker := testTracker(t, client, leaseTTL)
	tracker.RegisterCallback(func(_ context.Context, nodeID string) {
		lostNodeID.Store(nodeID)
	})

	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	tracker.OnDisconnect("node-1") // stop keepalive; TTL counts down

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive in local map after OnDisconnect")
	}

	// Wait for TTL to expire (etcd enforces >=1s; 2s TTL + 1s headroom).
	deadline := time.Now().Add(time.Duration(leaseTTL+2) * time.Second)
	for time.Now().Before(deadline) {
		if v := lostNodeID.Load(); v != nil && v.(string) == "node-1" {
			return // success
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("expected OnNodeLost callback for node-1 within %ds, got %v", leaseTTL+2, lostNodeID.Load())
}

func TestNodeLivenessTracker_ReconnectCancelsExpiry(t *testing.T) {
	const leaseTTL = 2

	client, cleanup := testEtcdClient(t)
	defer cleanup()

	var lostCalled atomic.Int32
	tracker := testTracker(t, client, leaseTTL)
	tracker.RegisterCallback(func(_ context.Context, _ string) {
		lostCalled.Add(1)
	})

	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	tracker.OnDisconnect("node-1") // stop keepalive

	// Reconnect before TTL expires.
	time.Sleep(500 * time.Millisecond)
	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode (reconnect) error = %v", err)
	}

	if !tracker.IsAlive("node-1") {
		t.Error("expected node-1 to be alive after reconnect")
	}

	// Wait well past the original TTL; callback should NOT have fired.
	time.Sleep(time.Duration(leaseTTL+2) * time.Second)

	if lostCalled.Load() != 0 {
		t.Errorf("expected OnNodeLost to NOT be called after reconnect, called %d times", lostCalled.Load())
	}
}

func TestNodeLivenessTracker_UntrackNodeImmediate(t *testing.T) {
	const leaseTTL = 30 // long TTL; untrack must be instant

	client, cleanup := testEtcdClient(t)
	defer cleanup()

	var lostCalled atomic.Int32
	tracker := testTracker(t, client, leaseTTL)
	tracker.RegisterCallback(func(_ context.Context, _ string) {
		lostCalled.Add(1)
	})

	if _, err := tracker.TrackNode("node-1"); err != nil {
		t.Fatalf("TrackNode() error = %v", err)
	}
	tracker.UntrackNode("node-1") // should revoke lease immediately

	if tracker.IsAlive("node-1") {
		t.Error("expected node-1 to not be alive after untrack")
	}

	// The watcher will fire the callback after the DELETE event. Give it a moment.
	time.Sleep(1 * time.Second)

	if lostCalled.Load() == 0 {
		t.Error("expected OnNodeLost callback after UntrackNode (lease revoked)")
	}
}

func TestNodeLivenessTracker_ConnectedNodes(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	tracker := testTracker(t, client, 30)

	for _, id := range []string{"node-1", "node-2", "node-3"} {
		if _, err := tracker.TrackNode(id); err != nil {
			t.Fatalf("TrackNode(%s) error = %v", id, err)
		}
	}

	connected := tracker.ConnectedNodes()
	if len(connected) != 3 {
		t.Errorf("expected 3 connected nodes, got %d: %v", len(connected), connected)
	}

	tracker.OnDisconnect("node-2")
	connected = tracker.ConnectedNodes()
	if len(connected) != 2 {
		t.Errorf("expected 2 connected nodes after disconnect, got %d: %v", len(connected), connected)
	}
}
