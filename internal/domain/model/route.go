package model

import "net/netip"

// MeshRoute describes how to reach a mesh service. Routes are distributed
// to agent nodes so they can forward traffic to the correct endpoints.
type MeshRoute struct {
	// ServiceID identifies the service this route is for.
	ServiceID ServiceID
	// MeshAddr is the virtual mesh IP assigned to the service.
	MeshAddr netip.Addr
	// TargetNodes lists the nodes where this service has healthy endpoints.
	TargetNodes []NodeEndpoint
}

// NodeEndpoint identifies a specific agent node that can serve traffic
// for a given service.
type NodeEndpoint struct {
	// NodeID is the unique identifier of the agent node.
	NodeID string
	// Addr is the network address of the agent (for HBONE tunnel establishment).
	Addr string
	// Target is the locality (NetworkTarget) of this node.
	Target string
	// Labels are the metadata tags for this node, used for subset routing.
	Labels map[string]string
}
