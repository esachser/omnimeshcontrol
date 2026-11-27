package model

import (
	"fmt"
	"strings"
	"time"
)

// PolicyAction determines whether an authorization policy allows, denies, or dry-runs traffic.
type PolicyAction int

const (
	// PolicyActionAllow explicitly allows traffic matching the policy.
	PolicyActionAllow PolicyAction = iota
	// PolicyActionDeny explicitly denies traffic matching the policy.
	PolicyActionDeny
	// PolicyActionDryRun logs matching traffic decisions without enforcing them.
	PolicyActionDryRun
)

// String returns the string representation of a PolicyAction.
func (a PolicyAction) String() string {
	switch a {
	case PolicyActionDeny:
		return "DENY"
	case PolicyActionDryRun:
		return "DRY_RUN"
	default:
		return "ALLOW"
	}
}

// ParsePolicyAction parses a raw action string into a PolicyAction.
// Empty string defaults to PolicyActionAllow.
func ParsePolicyAction(raw string) (PolicyAction, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "ALLOW":
		return PolicyActionAllow, nil
	case "DENY":
		return PolicyActionDeny, nil
	case "DRY_RUN", "DRY-RUN", "DRYRUN":
		return PolicyActionDryRun, nil
	default:
		return PolicyActionAllow, fmt.Errorf("unknown authorization policy action %q", raw)
	}
}

// AuthorizationPolicy defines a service-to-service access control rule.
// Policies are evaluated in order: DENY first → ALLOW list → default DENY.
type AuthorizationPolicy struct {
	// Name is the unique name of this authorization policy.
	Name string
	// Namespace is the namespace of this authorization policy.
	Namespace string
	// Action is whether this policy ALLOWs, DENYs, or DRY_RUNs the matching traffic.
	Action PolicyAction
	// Source selects which services or identities this policy applies to as the caller.
	Source PolicySelector
	// Destination selects which services or identities this policy applies to as the target.
	Destination PolicySelector
	// DestinationPorts restricts matching to specific destination ports. Empty matches any port.
	DestinationPorts []int
	// Rules defines optional L7 authorization rules (HTTP or gRPC).
	Rules []AuthzRule
	// ValidFrom is the optional inclusive timestamp when the policy becomes active.
	ValidFrom *time.Time
	// ValidUntil is the optional exclusive timestamp when the policy expires.
	ValidUntil *time.Time
}

// AuthzRule defines an L7 authorization rule (either HTTP or gRPC).
type AuthzRule struct {
	// HTTP specifies HTTP-level authorization match criteria.
	HTTP *HTTPAuthzRule
	// GRPC specifies gRPC-level authorization match criteria.
	GRPC *GRPCAuthzRule
}

// HTTPAuthzRule defines HTTP-specific L7 authorization criteria.
type HTTPAuthzRule struct {
	// Methods lists HTTP methods. Empty matches all methods.
	Methods []string
	// PathPrefixes lists URL path prefixes.
	PathPrefixes []string
	// PathExact lists exact URL paths.
	PathExact []string
	// Headers lists required HTTP headers.
	Headers map[string]string
}

// GRPCAuthzRule defines gRPC-specific L7 authorization criteria.
type GRPCAuthzRule struct {
	// Services lists gRPC service names. Empty matches all services.
	Services []string
	// Methods lists gRPC method names. Empty matches all methods.
	Methods []string
}

// PolicySelector identifies a set of services or identities by namespace, name, labels,
// exact SPIFFE ID, or trust domain.
// A wildcard "*" for Name matches all services in the namespace.
type PolicySelector struct {
	// Namespace of the selected service(s).
	Namespace string
	// Name of the selected service, or "*" for all services in the namespace.
	Name string
	// Labels further filters services within the namespace (AND logic).
	Labels map[string]string
	// SpiffeID selects an exact SPIFFE identity URI.
	SpiffeID string
	// TrustDomain selects all identities belonging to a SPIFFE trust domain.
	TrustDomain string
}

// IsWildcard reports whether this selector matches all services in the namespace.
func (s PolicySelector) IsWildcard() bool {
	return s.Name == "*"
}

// DependencyOrigin tracks where a dependency came from.
type DependencyOrigin int

const (
	// DependencyManual was declared via the --depends-on CLI flag or resource spec.
	DependencyManual DependencyOrigin = iota
	// DependencyAuthzPolicy was auto-derived from an ALLOW authorization policy.
	DependencyAuthzPolicy
)

// ServiceDependency records a dependency between a source service and a target.
type ServiceDependency struct {
	// Source is the service that depends on Target.
	Source ServiceID
	// Target is the depended-upon service. Target.Name may be "*" for namespace-wide wildcards.
	Target ServiceID
	// Origin indicates how this dependency was derived.
	Origin DependencyOrigin
	// PolicyName is the name of the authorization policy that created this dependency.
	// Empty for manually declared dependencies.
	PolicyName string
}

// ServiceDependencies is the set of dependencies for a single source service.
type ServiceDependencies struct {
	// ServiceID is the source service these dependencies belong to.
	ServiceID ServiceID
	// Dependencies is the list of services this service depends on.
	Dependencies []ServiceDependency
}
