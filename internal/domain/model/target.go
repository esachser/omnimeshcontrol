package model

import "net/netip"

// NetworkTarget represents a distinct routing domain (an isolated network)
// where internal IPs are routable between nodes within the target, but not from the outside.
// Example: a single Kubernetes cluster or a single VNet.
type NetworkTarget struct {
	// Name is the unique identifier of the NetworkTarget.
	Name string
	// GatewayAddress is the address external agents must use to establish
	// the HBONE tunnel to reach agents inside this target.
	GatewayAddress string
}

// TargetRoute defines how traffic for a specific service is distributed
// across different targets. It is defined per-target to prevent lock points.
type TargetRoute struct {
	// Name uniquely identifies this TargetRoute.
	Name string
	// Namespace is the namespace of the service this route applies to.
	Namespace string
	// TargetService is the name of the service this route applies to.
	TargetService string
	// Labels is a map of labels to match against service instances.
	Labels map[string]string
	// NodeID is the ID of the node this route is associated with.
	NodeID string
	// Address is the address of the node this route is associated with.
	Address netip.AddrPort
}
