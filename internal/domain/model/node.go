package model

import "net/netip"

// NodeStatus indicates the lifecycle state of an agent node.
type NodeStatus int

const (
	// NodeStatusActive indicates the node is connected and healthy.
	NodeStatusActive NodeStatus = iota
	// NodeStatusLost indicates the node has disconnected and the grace period
	// has expired without a reconnection.
	NodeStatusLost
	// NodeStatusDeregistered indicates the node was gracefully deregistered.
	NodeStatusDeregistered
)

// String returns the string representation of a NodeStatus.
func (s NodeStatus) String() string {
	switch s {
	case NodeStatusActive:
		return "ACTIVE"
	case NodeStatusLost:
		return "LOST"
	case NodeStatusDeregistered:
		return "DEREGISTERED"
	default:
		return "UNKNOWN"
	}
}

// NodePlatform describes the operating system of the agent node.
type NodePlatform int

const (
	// NodePlatformLinux represents a Linux-based node.
	NodePlatformLinux NodePlatform = iota
	// NodePlatformWindows represents a Windows-based node.
	NodePlatformWindows
	// NodePlatformDarwin represents a macOS-based node.
	NodePlatformDarwin
)

// String returns the string representation of a NodePlatform.
func (p NodePlatform) String() string {
	switch p {
	case NodePlatformWindows:
		return "windows"
	case NodePlatformDarwin:
		return "darwin"
	default:
		return "linux"
	}
}

// Node represents an agent node in the mesh. Each node runs an Omnimesh Agent
// that manages traffic interception for all services on that node.
type Node struct {
	// ID is the unique identifier for this node (typically the hostname or a UUID).
	ID string
	// Platform is the operating system of the node.
	Platform NodePlatform
	// Target is the NetworkTarget this node belongs to.
	Target string
	// Addr is the network address of the node's agent (used for HBONE tunneling).
	Addr netip.AddrPort
	// Status indicates the lifecycle state of the node.
	Status NodeStatus
	// Labels are arbitrary key-value pairs for organization and selection.
	Labels map[string]string
	// Services lists the ServiceIDs of services running on this node.
	Services []ServiceID
}
