package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// TrafficPolicyManager defines the inbound use cases for traffic policy management.
type TrafficPolicyManager interface {
	// CreateTrafficPolicy creates a new traffic policy.
	// Returns domain.ErrPolicyConflict if a policy already exists for the target service.
	CreateTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error
	// UpdateTrafficPolicy updates an existing traffic policy.
	UpdateTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error
	// GetTrafficPolicy retrieves a traffic policy by namespace and name.
	GetTrafficPolicy(ctx context.Context, namespace, name string) (*model.TrafficPolicy, error)
	// DeleteTrafficPolicy removes a traffic policy.
	DeleteTrafficPolicy(ctx context.Context, namespace, name string) error
	// ListTrafficPolicies returns all traffic policies in the given namespace.
	ListTrafficPolicies(ctx context.Context, namespace string) ([]*model.TrafficPolicy, error)
	// GetPolicyForService returns the traffic policy attached to a specific service.
	GetPolicyForService(ctx context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error)
}

// ServiceHealthReporter defines the inbound use cases for agents reporting health.
type ServiceHealthReporter interface {
	// ReportEndpointHealth processes a health status report from an agent.
	ReportEndpointHealth(ctx context.Context, report *model.EndpointHealth) error
	// DrainService initiates graceful termination for a service endpoint.
	// The control plane withdraws the endpoint from all agents' routes,
	// preventing new connections while allowing existing ones to complete.
	DrainService(ctx context.Context, serviceID model.ServiceID, endpointAddr string) error
}

// AgentScopeManager defines the inbound use cases for managing agent scopes.
type AgentScopeManager interface {
	// GetAgentScope returns the full set of services, routes, policies, and health
	// data that the given agent needs.
	GetAgentScope(ctx context.Context, nodeID string) (*model.AgentScope, error)
}
