package etcdstore

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const livenessPrefix = "/omnimesh/liveness/"

// NodeLivenessTracker tracks agent node liveness via etcd leases.
//
// Each connected node holds one etcd lease. The liveness key, the persisted
// node state, and all target-route keys are attached to that lease so that
// when the lease expires (TTL elapsed after disconnect) or is revoked
// (graceful deregistration), etcd atomically removes every key — across all
// control-plane replicas simultaneously.
//
// The background watcher on livenessPrefix fires OnNodeLost callbacks when
// the liveness key is deleted, regardless of which CP replica held the stream.
type NodeLivenessTracker struct {
	client   *clientv3.Client
	leaseTTL int64 // seconds
	mu       sync.Mutex
	nodes    map[string]*liveNode
	onLost   []service.NodeLivenessCallback
	logger   *slog.Logger

	watchCancel context.CancelFunc
}

type liveNode struct {
	leaseID      clientv3.LeaseID
	kaCancel     context.CancelFunc // cancels the KeepAlive goroutine
	disconnected bool               // true after OnDisconnect (keepalive stopped, TTL counting)
}

// NewNodeLivenessTracker creates a tracker backed by etcd leases and starts
// a background watcher for liveness-key deletions.
func NewNodeLivenessTracker(client *clientv3.Client, leaseTTL int64, logger *slog.Logger) (*NodeLivenessTracker, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if leaseTTL <= 0 {
		leaseTTL = 30
	}

	watchCtx, watchCancel := context.WithCancel(context.Background())

	t := &NodeLivenessTracker{
		client:      client,
		leaseTTL:    leaseTTL,
		nodes:       make(map[string]*liveNode),
		logger:      logger,
		watchCancel: watchCancel,
	}

	go t.watchLiveness(watchCtx)
	return t, nil
}

// watchLiveness runs a background etcd watcher that fires OnNodeLost callbacks
// whenever a liveness key is deleted (lease expired or revoked).
func (t *NodeLivenessTracker) watchLiveness(ctx context.Context) {
	watchCh := t.client.Watch(ctx, livenessPrefix, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			for _, ev := range resp.Events {
				if ev.Type != clientv3.EventTypeDelete {
					continue
				}
				nodeID := strings.TrimPrefix(string(ev.Kv.Key), livenessPrefix)
				t.logger.Warn("node liveness key deleted — node lost", "nodeID", nodeID)
				t.mu.Lock()
				// Only remove and fire callbacks if the node is still in the
				// disconnected-pending state. If it has already reconnected
				// (disconnected == false), the DELETE event is from the old
				// lease expiring harmlessly — do NOT fire OnNodeLost.
				existing, stillTracked := t.nodes[nodeID]
				if stillTracked && existing.disconnected {
					delete(t.nodes, nodeID)
				}
				callbacks := make([]service.NodeLivenessCallback, len(t.onLost))
				copy(callbacks, t.onLost)
				shouldFire := stillTracked && existing.disconnected
				t.mu.Unlock()

				if shouldFire {
					for _, cb := range callbacks {
						go cb(context.Background(), nodeID)
					}
				}
			}
		}
	}
}

// RegisterCallback registers a callback to be called when a node is lost.
func (t *NodeLivenessTracker) RegisterCallback(callback service.NodeLivenessCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onLost = append(t.onLost, callback)
}

// TrackNode grants a new etcd lease for the node, puts the liveness key
// attached to that lease, and starts a keepalive loop.
// If a previous lease exists for this node, it is revoked first.
// Returns the new LeaseID so callers can attach additional keys to the lease.
func (t *NodeLivenessTracker) TrackNode(nodeID string) (service.LeaseID, error) {
	t.mu.Lock()
	// Revoke any existing lease for this node (reconnect scenario).
	if old, ok := t.nodes[nodeID]; ok {
		old.kaCancel()
		// Best-effort revoke; ignore error if already expired.
		go t.client.Revoke(context.Background(), old.leaseID) //nolint:errcheck
		delete(t.nodes, nodeID)
	}
	t.mu.Unlock()

	// Grant a new lease.
	grantResp, err := t.client.Grant(context.Background(), t.leaseTTL)
	if err != nil {
		return 0, fmt.Errorf("node tracker: grant lease for %q: %w", nodeID, err)
	}
	leaseID := grantResp.ID

	// Put the liveness key attached to the new lease.
	livenessKey := livenessPrefix + nodeID
	if _, err := t.client.Put(context.Background(), livenessKey, "1",
		clientv3.WithLease(leaseID)); err != nil {
		t.client.Revoke(context.Background(), leaseID) //nolint:errcheck
		return 0, fmt.Errorf("node tracker: put liveness key for %q: %w", nodeID, err)
	}

	// Start keepalive.
	kaCtx, kaCancel := context.WithCancel(context.Background())
	kaCh, err := t.client.KeepAlive(kaCtx, leaseID)
	if err != nil {
		kaCancel()
		t.client.Revoke(context.Background(), leaseID) //nolint:errcheck
		return 0, fmt.Errorf("node tracker: start keepalive for %q: %w", nodeID, err)
	}

	// Drain keepalive responses in the background.
	go func() {
		for range kaCh {
			// responses just confirm the lease is renewed; nothing to do.
		}
	}()

	t.mu.Lock()
	t.nodes[nodeID] = &liveNode{leaseID: leaseID, kaCancel: kaCancel}
	t.mu.Unlock()

	t.logger.Info("node tracked with etcd lease",
		"nodeID", nodeID,
		"leaseID", leaseID,
		"ttl", t.leaseTTL,
	)
	return int64(leaseID), nil
}

// OnDisconnect stops the keepalive for a node without revoking the lease.
// The lease TTL counts down and etcd deletes all attached keys automatically,
// which triggers the OnNodeLost callback via the watcher.
func (t *NodeLivenessTracker) OnDisconnect(nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.nodes[nodeID]
	if !ok {
		return
	}
	node.kaCancel()         // stop keepalive; lease will expire after TTL
	node.disconnected = true // mark as grace-period-pending
	t.logger.Info("node keepalive stopped — lease TTL counting down",
		"nodeID", nodeID,
		"leaseID", node.leaseID,
		"ttlSeconds", t.leaseTTL,
	)
}

// UntrackNode immediately revokes the lease, causing etcd to instantly delete
// every key associated with the node (liveness, state, routes). Used for
// graceful deregistration. Callbacks are fired inline.
func (t *NodeLivenessTracker) UntrackNode(nodeID string) {
	t.mu.Lock()
	node, ok := t.nodes[nodeID]
	if !ok {
		t.mu.Unlock()
		return
	}
	node.kaCancel()
	leaseID := node.leaseID
	delete(t.nodes, nodeID)
	callbacks := make([]service.NodeLivenessCallback, len(t.onLost))
	copy(callbacks, t.onLost)
	t.mu.Unlock()

	if _, err := t.client.Revoke(context.Background(), leaseID); err != nil {
		t.logger.Warn("node tracker: revoke lease", "nodeID", nodeID, "error", err)
	}
	t.logger.Info("node untracked — lease revoked", "nodeID", nodeID, "leaseID", leaseID)

	// Fire callbacks inline (don't wait for the watcher DELETE event).
	for _, cb := range callbacks {
		go cb(context.Background(), nodeID)
	}
}

// IsAlive reports whether a node has an active keepalive lease (not just pending-expiry).
func (t *NodeLivenessTracker) IsAlive(nodeID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, ok := t.nodes[nodeID]
	return ok && !n.disconnected
}

// LeaseID returns the current lease ID for a live node, or 0 if not tracked.
func (t *NodeLivenessTracker) LeaseID(nodeID string) service.LeaseID {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n, ok := t.nodes[nodeID]; ok {
		return int64(n.leaseID)
	}
	return 0
}

// ConnectedNodes returns the IDs of all nodes with an active keepalive lease.
func (t *NodeLivenessTracker) ConnectedNodes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]string, 0, len(t.nodes))
	for id, n := range t.nodes {
		if !n.disconnected {
			result = append(result, id)
		}
	}
	return result
}

// Close cancels the background watcher and revokes all active leases.
// Must be called when the control-plane shuts down.
func (t *NodeLivenessTracker) Close() {
	t.watchCancel()

	t.mu.Lock()
	leases := make([]clientv3.LeaseID, 0, len(t.nodes))
	for _, n := range t.nodes {
		n.kaCancel()
		leases = append(leases, n.leaseID)
	}
	t.nodes = make(map[string]*liveNode)
	t.mu.Unlock()

	for _, id := range leases {
		t.client.Revoke(context.Background(), id) //nolint:errcheck
	}
}

// Compile-time interface check.
var _ service.NodeTracker = (*NodeLivenessTracker)(nil)
