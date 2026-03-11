package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// ScopeDistributor announces scope updates to agent nodes over their persistent stream.
type ScopeDistributor interface {
	// PushUpdate pushes a differential scope update to a specific agent node.
	// Returns an error if the agent is not connected or the send fails.
	PushUpdate(ctx context.Context, nodeID string, update model.ScopeUpdate) error
	// BroadcastUpdate pushes a differential scope update to all connected agent nodes.
	BroadcastUpdate(ctx context.Context, update model.ScopeUpdate) error
}
