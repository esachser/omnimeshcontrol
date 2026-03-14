package service

import (
	"context"
)

// NodeLivenessCallback is called when a node's liveness state changes.
type NodeLivenessCallback func(ctx context.Context, nodeID string)

// NodeTracker is the interface for the in-memory liveness tracker.
// This decouples the application service from the concrete adapter implementation.
type NodeTracker interface {
	TrackNode(nodeID string)
	OnDisconnect(nodeID string)
	UntrackNode(nodeID string)
	IsAlive(nodeID string) bool
	RegisterCallback(callback NodeLivenessCallback)
}
