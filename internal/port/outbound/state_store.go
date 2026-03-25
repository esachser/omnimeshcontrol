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
// Node liveness is tracked via etcd leases (NodeLivenessTracker). Keys for the
// node record and its target routes are attached to the same lease so that etcd
// cleans them up atomically when the lease expires or is revoked.
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

	// PersistNode persists a node's state to durable storage.
	// leaseID must be the etcd lease ID returned by NodeTracker.TrackNode so the
	// key is automatically removed when the lease expires or is revoked.
	PersistNode(ctx context.Context, nodeID string, node *model.Node, leaseID int64) error

	// IsLeader reports whether this control plane instance is the current Raft leader.
	IsLeader() bool

	// WaitForLeader blocks until a leader is elected or the timeout expires.
	WaitForLeader(ctx context.Context, timeout time.Duration) error

	// LeaderAddr returns the address of the current leader.
	LeaderAddr() string

	// PersistTargetRoute persists a target route reported by a node.
	// leaseID must be the etcd lease ID for the node that reported this route so
	// the route key is automatically removed when the node's lease expires.
	PersistTargetRoute(ctx context.Context, route *model.TargetRoute, leaseID int64) error

	// DeleteTargetRoute removes a specific target route.
	DeleteTargetRoute(ctx context.Context, namespace, nodeID, serviceName, name string) error

	// GetTargetRoute retrieves a specific target route.
	GetTargetRoute(ctx context.Context, namespace, nodeID, serviceName, name string) (*model.TargetRoute, error)

	// ListTargetRoutes returns all target routes, optionally filtered by namespace.
	ListTargetRoutes(ctx context.Context, namespace string) ([]*model.TargetRoute, error)

	// ListTargetRoutesByNode retrieves all target routes associated with a specific node.
	ListTargetRoutesByNode(ctx context.Context, nodeID string) ([]*model.TargetRoute, error)

	// ClearTargetRoutes removes all target routes.
	ClearTargetRoutes(ctx context.Context) error

	// WatchTargetRoutes returns a channel of events for target route state changes.
	WatchTargetRoutes(ctx context.Context) (<-chan TargetRouteStateEvent, error)
}

// StateEventType indicates the kind of change in a state watch event.
type StateEventType int

const (
	// StateEventPut indicates a state record was created or updated.
	StateEventPut StateEventType = iota
	// StateEventDelete indicates a state record was deleted.
	StateEventDelete
)

// TargetRouteStateEvent represents a single change observed via a watch on TargetRoutes in the StateStore.
type TargetRouteStateEvent struct {
	// Type indicates whether this is a put or delete event.
	Type StateEventType
	// Key is the storage key of the target route that changed.
	Key string
	// Route is the target route value (nil on delete events).
	Route *model.TargetRoute
}
