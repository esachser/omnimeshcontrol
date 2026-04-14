package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// NodeRegistrar defines the inbound use cases for agent node registration.
// Node liveness is implicit from the persistent gRPC stream — there is no
// explicit Heartbeat RPC.
type NodeRegistrar interface {
	// RegisterNode registers a new agent node with the control plane.
	// Called when the agent opens the bidirectional gRPC stream.
	RegisterNode(ctx context.Context, node *model.Node) error
	// DeregisterNode gracefully deregisters a node.
	// Called when the agent sends a deregistration message before closing the stream.
	DeregisterNode(ctx context.Context, nodeID string) error
	// OnStreamDisconnect is called when an agent's gRPC stream disconnects unexpectedly.
	// Starts the grace period timer. If the agent does not reconnect within the grace
	// period, the node is marked as lost and its services are made unhealthy.
	OnStreamDisconnect(ctx context.Context, nodeID string) error
	// OnStreamReconnect is called when an agent reconnects within the grace period.
	// Cancels the grace period timer and resumes the node.
	OnStreamReconnect(ctx context.Context, nodeID string) error
	// UpdateNodeServices updates the services running on the node.
	UpdateNodeServices(ctx context.Context, nodeID string, added []model.ServiceID, removed []model.ServiceID) error
	// DrainNode initiates graceful node draining. Sets the node status to
	// DRAINING and stops the keepalive so the lease TTL counts down (grace
	// period). The NodeUpdateService watcher broadcasts a single route-removal
	// event to all agents. When the lease expires, all attached keys (routes,
	// health, liveness) are atomically deleted.
	DrainNode(ctx context.Context, nodeID string) error
}
