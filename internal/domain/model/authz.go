package model

// PolicyAction determines whether an authorization policy allows or denies traffic.
type PolicyAction int

const (
	// PolicyActionAllow explicitly allows traffic matching the policy.
	PolicyActionAllow PolicyAction = iota
	// PolicyActionDeny explicitly denies traffic matching the policy.
	PolicyActionDeny
)

// String returns the string representation of a PolicyAction.
func (a PolicyAction) String() string {
	switch a {
	case PolicyActionDeny:
		return "DENY"
	default:
		return "ALLOW"
	}
}

// AuthorizationPolicy defines a service-to-service access control rule.
// Policies are evaluated in order: DENY first → ALLOW list → default DENY.
type AuthorizationPolicy struct {
	// Name is the unique name of this authorization policy.
	Name string
	// Namespace is the namespace of this authorization policy.
	Namespace string
	// Action is whether this policy ALLOWs or DENYs the matching traffic.
	Action PolicyAction
	// Source selects which services this policy applies to as the caller.
	Source PolicySelector
	// Destination selects which services this policy applies to as the target.
	Destination PolicySelector
}

// PolicySelector identifies a set of services by namespace, name, or labels.
// A wildcard "*" for Name matches all services in the namespace.
type PolicySelector struct {
	// Namespace of the selected service(s).
	Namespace string
	// Name of the selected service, or "*" for all services in the namespace.
	Name string
	// Labels further filters services within the namespace (AND logic).
	Labels map[string]string
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
