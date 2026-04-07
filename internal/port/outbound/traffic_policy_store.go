package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// TrafficPolicyEventType indicates the kind of change in a policy watch event.
type TrafficPolicyEventType int

const (
	// TrafficPolicyEventPut indicates a policy was created or updated.
	TrafficPolicyEventPut TrafficPolicyEventType = iota
	// TrafficPolicyEventDelete indicates a policy was deleted.
	TrafficPolicyEventDelete
)

// TrafficPolicyEvent is a watch event for traffic policy changes.
type TrafficPolicyEvent struct {
	Type   TrafficPolicyEventType
	Policy *model.TrafficPolicy
}

// TrafficPolicyStore persists and queries traffic policies.
type TrafficPolicyStore interface {
	PutTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error
	GetTrafficPolicy(ctx context.Context, namespace, name string) (*model.TrafficPolicy, error)
	GetPolicyByService(ctx context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error)
	ListTrafficPolicies(ctx context.Context, namespace string) ([]*model.TrafficPolicy, error)
	DeleteTrafficPolicy(ctx context.Context, namespace, name string) error
	WatchTrafficPolicies(ctx context.Context) (<-chan TrafficPolicyEvent, error)
}

// EndpointHealthEventType indicates the kind of change in a health watch event.
type EndpointHealthEventType int

const (
	// EndpointHealthEventPut indicates a health record was created or updated.
	EndpointHealthEventPut EndpointHealthEventType = iota
	// EndpointHealthEventDelete indicates a health record was deleted.
	EndpointHealthEventDelete
)

// EndpointHealthEvent is a watch event for endpoint health changes.
type EndpointHealthEvent struct {
	Type   EndpointHealthEventType
	Health *model.EndpointHealth
}

// EndpointHealthStore persists and queries endpoint health status.
type EndpointHealthStore interface {
	// PutEndpointHealth persists an endpoint health report.
	// leaseID must be the etcd lease ID for the node that reported this health so
	// the key is automatically removed when the node's lease expires.
	PutEndpointHealth(ctx context.Context, health *model.EndpointHealth, leaseID int64) error
	GetEndpointHealth(ctx context.Context, serviceID model.ServiceID, addr string) (*model.EndpointHealth, error)
	ListEndpointHealth(ctx context.Context, serviceID model.ServiceID) ([]*model.EndpointHealth, error)
	WatchEndpointHealth(ctx context.Context) (<-chan EndpointHealthEvent, error)
}

// DependencyEventType indicates the kind of change in a dependency watch event.
type DependencyEventType int

const (
	// DependencyEventPut indicates a dependency was created or updated.
	DependencyEventPut DependencyEventType = iota
	// DependencyEventDelete indicates a dependency was deleted.
	DependencyEventDelete
)

// DependencyEvent is a watch event for dependency changes.
type DependencyEvent struct {
	Type         DependencyEventType
	Dependencies *model.ServiceDependencies
}

// DependencyStore persists and queries service dependencies.
type DependencyStore interface {
	PutDependencies(ctx context.Context, deps *model.ServiceDependencies) error
	GetDependencies(ctx context.Context, serviceID model.ServiceID) (*model.ServiceDependencies, error)
	WatchDependencies(ctx context.Context) (<-chan DependencyEvent, error)
}

// ScopedDistributor pushes scoped state to agents via the persistent gRPC stream.
type ScopedDistributor interface {
	// PushScopeSnapshot sends the full scope to an agent (on initial connection).
	PushScopeSnapshot(ctx context.Context, nodeID string, scope *model.AgentScope) error
	// PushScopeUpdate sends an incremental scope change to an agent.
	PushScopeUpdate(ctx context.Context, nodeID string, update *model.ScopeUpdate) error
}
