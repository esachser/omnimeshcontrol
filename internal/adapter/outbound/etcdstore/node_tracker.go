package etcdstore

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// NodeLivenessCallback is called when a node's liveness state changes.
type NodeLivenessCallback func(ctx context.Context, nodeID string)

// NodeLivenessTracker tracks agent node liveness in-memory via gRPC stream
// presence. When a stream disconnects, a grace period timer starts. If the
// agent does not reconnect within the grace period, the OnNodeLost callback fires.
type NodeLivenessTracker struct {
	mu          sync.Mutex
	gracePeriod time.Duration
	nodes       map[string]*trackedNode
	onNodeLost  NodeLivenessCallback
	logger      *slog.Logger
}

type trackedNode struct {
	cancel context.CancelFunc // cancels the grace period timer
	alive  bool
}

// NewNodeLivenessTracker creates a new tracker with the given grace period.
func NewNodeLivenessTracker(gracePeriod time.Duration, onNodeLost NodeLivenessCallback, logger *slog.Logger) *NodeLivenessTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeLivenessTracker{
		gracePeriod: gracePeriod,
		nodes:       make(map[string]*trackedNode),
		onNodeLost:  onNodeLost,
		logger:      logger,
	}
}

// TrackNode registers a node as alive. If the node was previously disconnected
// with a pending grace period timer, the timer is cancelled.
func (t *NodeLivenessTracker) TrackNode(nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.nodes[nodeID]; ok {
		// Cancel any pending grace period timer.
		if existing.cancel != nil {
			existing.cancel()
		}
		existing.alive = true
		existing.cancel = nil
		t.logger.Info("node reconnected within grace period", "nodeID", nodeID)
		return
	}

	t.nodes[nodeID] = &trackedNode{alive: true}
	t.logger.Info("node tracked", "nodeID", nodeID)
}

// OnDisconnect starts the grace period timer for a node. If the node does not
// reconnect (via TrackNode) within the grace period, the OnNodeLost callback fires.
func (t *NodeLivenessTracker) OnDisconnect(nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.nodes[nodeID]
	if !ok {
		return
	}

	node.alive = false
	ctx, cancel := context.WithCancel(context.Background())
	node.cancel = cancel

	t.logger.Info("node disconnected, starting grace period",
		"nodeID", nodeID,
		"gracePeriod", t.gracePeriod,
	)

	go t.graceTimer(ctx, nodeID)
}

// graceTimer waits for the grace period to expire, then fires the callback.
func (t *NodeLivenessTracker) graceTimer(ctx context.Context, nodeID string) {
	select {
	case <-time.After(t.gracePeriod):
		t.mu.Lock()
		node, ok := t.nodes[nodeID]
		if !ok || node.alive {
			t.mu.Unlock()
			return
		}
		delete(t.nodes, nodeID)
		t.mu.Unlock()

		t.logger.Warn("node grace period expired, marking as lost", "nodeID", nodeID)
		if t.onNodeLost != nil {
			t.onNodeLost(context.Background(), nodeID)
		}
	case <-ctx.Done():
		// Grace period was cancelled (node reconnected).
	}
}

// UntrackNode immediately removes a node without waiting for a grace period.
// Used for graceful deregistration.
func (t *NodeLivenessTracker) UntrackNode(nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if node, ok := t.nodes[nodeID]; ok {
		if node.cancel != nil {
			node.cancel()
		}
		delete(t.nodes, nodeID)
		t.logger.Info("node untracked (graceful deregistration)", "nodeID", nodeID)
	}
}

// IsAlive reports whether a node is currently connected (alive).
func (t *NodeLivenessTracker) IsAlive(nodeID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.nodes[nodeID]
	return ok && node.alive
}

// ConnectedNodes returns the IDs of all currently connected nodes.
func (t *NodeLivenessTracker) ConnectedNodes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make([]string, 0, len(t.nodes))
	for id, node := range t.nodes {
		if node.alive {
			result = append(result, id)
		}
	}
	return result
}
