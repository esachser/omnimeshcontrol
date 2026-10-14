package model

import "time"

// HealthProbeType indicates the type of health check used to probe an endpoint.
type HealthProbeType int

const (
	// HealthProbeTCP checks if a TCP connection can be established.
	HealthProbeTCP HealthProbeType = iota
	// HealthProbeHTTP sends an HTTP GET and checks the response status code.
	HealthProbeHTTP
	// HealthProbeGRPC uses the gRPC health checking protocol.
	HealthProbeGRPC
)

// String returns the string representation of a HealthProbeType.
func (t HealthProbeType) String() string {
	switch t {
	case HealthProbeHTTP:
		return "HTTP"
	case HealthProbeGRPC:
		return "gRPC"
	default:
		return "TCP"
	}
}

// EndpointHealth represents the health status of a single service endpoint
// as reported by an agent.
type EndpointHealth struct {
	// ServiceID identifies the service this health report is for.
	ServiceID ServiceID
	// RouteName is the route name of the endpoint.
	RouteName string
	// Status is the current health status.
	Status EndpointHealthStatus
	// LastCheck is when the last health check was performed.
	LastCheck time.Time
	// ProbeType is the type of health check used.
	ProbeType HealthProbeType
	// NodeID is the agent node that reported this health.
	NodeID string
}

// AgentScope represents the full set of services, routes, policies, and health
// data that a given agent needs.
type AgentScope struct {
	// NodeID identifies the agent this scope is for.
	NodeID string
	// Services are the services relevant to this agent.
	Services []Service
	// Routes are the target routes (node-level service registrations) relevant to this agent.
	Routes []TargetRoute
	// TrafficPolicies are the traffic policies relevant to this agent's services.
	TrafficPolicies []TrafficPolicy
	// AuthzPolicies are the authorization policies relevant to this agent's services.
	AuthzPolicies []AuthorizationPolicy
	// EndpointHealth is the health status of endpoints in this agent's scope.
	EndpointHealth []EndpointHealth
	// Namespaces are the namespaces and their subnets owned by omnimesh.
	Namespaces []Namespace
}

// ScopeUpdateType indicates what kind of scope change occurred.
type ScopeUpdateType int

const (
	// ScopeUpdateAdd indicates a resource was added to the agent's scope.
	ScopeUpdateAdd ScopeUpdateType = iota
	// ScopeUpdateModify indicates a resource in the agent's scope was modified.
	ScopeUpdateModify
	// ScopeUpdateRemove indicates a resource was removed from the agent's scope.
	ScopeUpdateRemove
)

// ScopeUpdate represents an incremental change to an agent's scope.
type ScopeUpdate struct {
	// Type indicates what kind of change occurred.
	Type ScopeUpdateType
	// Services are the services that changed (if applicable).
	Services []Service
	// Routes are the target routes that changed (if applicable).
	Routes []TargetRoute
	// TrafficPolicies are the policies that changed (if applicable).
	TrafficPolicies []TrafficPolicy
	// AuthzPolicies are the authz policies that changed (if applicable).
	AuthzPolicies []AuthorizationPolicy
	// EndpointHealth is the health data that changed (if applicable).
	EndpointHealth []EndpointHealth
	// Namespaces are the namespaces that changed (if applicable).
	Namespaces []Namespace
}
