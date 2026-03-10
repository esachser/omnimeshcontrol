package outbound

import (
	"context"
	"net/netip"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// StateStore handles system-derived state and infrastructure-specific operations.
// The default implementation maps to /omnimesh/state/ keys in etcd.
//
// Node liveness is tracked in-memory (not here) — this interface only handles
// the persistent state transitions (registration, loss, deregistration).
type StateStore interface {
	// AllocateIP atomically allocates a mesh IP for the given service from the CIDR.
	// Returns domain.ErrIPExhausted if no IPs are available, or domain.ErrIPConflict
	// if the service already has an allocation.
	AllocateIP(ctx context.Context, id model.ServiceID, cidr netip.Prefix) (netip.Addr, error)

	// ReleaseIP releases the mesh IP allocated to the given service.
	ReleaseIP(ctx context.Context, id model.ServiceID) error

	// GetIPAllocation returns the mesh IP allocated to the given service.
	// Returns domain.ErrServiceNotFound if no allocation exists.
	GetIPAllocation(ctx context.Context, id model.ServiceID) (netip.Addr, error)

	// GetNode retrieves a node's state from durable storage.
	// Returns domain.ErrNodeNotFound if the node does not exist.
	GetNode(ctx context.Context, nodeID string) (*model.Node, error)

	// PersistNode persists a node's state to durable storage (e.g., etcd).
	// Called on initial registration and on state changes.
	PersistNode(ctx context.Context, nodeID string, node *model.Node) error

	// MarkNodeLost updates a node's persistent state to LOST.
	// Called after the in-memory grace period timer expires.
	MarkNodeLost(ctx context.Context, nodeID string) error

	// DeregisterNode removes a node from persistent storage.
	// Called when an agent gracefully shuts down.
	DeregisterNode(ctx context.Context, nodeID string) error

	// IsLeader reports whether this control plane instance is the current Raft leader.
	IsLeader() bool

	// WaitForLeader blocks until a leader is elected or the timeout expires.
	WaitForLeader(ctx context.Context, timeout time.Duration) error

	// LeaderAddr returns the address of the current leader.
	LeaderAddr() string
}
