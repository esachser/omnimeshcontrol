package service

import (
	"context"
)

// LeaseID is an opaque identifier for a distributed lease (maps to etcd LeaseID).
// Defined here to keep the domain layer free of etcd imports.
type LeaseID = int64

// NodeLivenessCallback is called when a node's liveness state changes.
type NodeLivenessCallback func(ctx context.Context, nodeID string)

// NodeTracker is the interface for the distributed liveness tracker.
// The concrete implementation uses etcd leases so that every control-plane
// replica observes the same liveness state.
type NodeTracker interface {
	// TrackNode grants a new etcd lease for the node and starts keepalive.
	// Returns the LeaseID so callers can attach other etcd keys to the same lease.
	TrackNode(nodeID string) (LeaseID, error)
	// OnDisconnect stops the keepalive for a node, letting the lease TTL expire
	// naturally. All keys attached to the lease are removed when it expires.
	OnDisconnect(nodeID string)
	// UntrackNode immediately revokes the lease, causing instant key removal.
	// Used for graceful deregistration.
	UntrackNode(nodeID string)
	// IsAlive reports whether a node currently has an active lease.
	IsAlive(nodeID string) bool
	// LeaseID returns the current lease ID for a live node, or 0 if not tracked.
	LeaseID(nodeID string) LeaseID
	// ConnectedNodes returns the IDs of all currently connected (leased) nodes.
	ConnectedNodes() []string
	// RegisterCallback registers a callback fired when a node's liveness key is
	// deleted (i.e., lease expired or revoked). All CP replicas fire this.
	RegisterCallback(callback NodeLivenessCallback)
}
