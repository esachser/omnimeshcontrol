package model

import (
	"fmt"
	"net/netip"
	"strings"
)

// ServiceID uniquely identifies a service within the mesh.
type ServiceID struct {
	Namespace string
	Name      string
}

// String returns the canonical string representation of a service ID.
func (id ServiceID) String() string {
	return id.Namespace + "/" + id.Name
}

// ParseServiceID parses a string in "namespace/name" format into a ServiceID.
func ParseServiceID(s string) (ServiceID, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return ServiceID{}, fmt.Errorf("invalid service ID format %q: expected namespace/name", s)
	}
	return ServiceID{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}

// ServiceProtocol indicates the application protocol of a service.
// This determines which traffic policy features are available.
type ServiceProtocol int

const (
	// ServiceProtocolTCP is the default, lowest-common-denominator protocol.
	ServiceProtocolTCP ServiceProtocol = iota
	// ServiceProtocolHTTP indicates an HTTP service (L7 features available).
	ServiceProtocolHTTP
	// ServiceProtocolGRPC indicates a gRPC service (L7 features available).
	ServiceProtocolGRPC
)

// String returns the string representation of a ServiceProtocol.
func (p ServiceProtocol) String() string {
	switch p {
	case ServiceProtocolHTTP:
		return "HTTP"
	case ServiceProtocolGRPC:
		return "gRPC"
	default:
		return "TCP"
	}
}

// IsL7 reports whether the protocol supports L7 traffic policy features
// such as HTTP-level retries and header-based routing.
func (p ServiceProtocol) IsL7() bool {
	return p == ServiceProtocolHTTP || p == ServiceProtocolGRPC
}

// Service represents a logical service in the mesh.
type Service struct {
	ID           ServiceID
	Protocol     ServiceProtocol
	Endpoints    []ServiceEndpoint
	MeshAddr     netip.Addr
	Labels       map[string]string
	Dependencies []ServiceID
	Port         int
	// Selectors defines a single set of workload selectors (deprecated: use SelectorSets).
	Selectors []string
	// SelectorSets defines one or more independent sets of workload selectors.
	// Selectors within a set must all match simultaneously (logical AND), while
	// multiple sets act as alternatives (logical OR), e.g. one set for Windows
	// and another for Linux.
	SelectorSets [][]string
}

// EffectiveSelectorSets returns the normalized list of selector sets for this
// service. If SelectorSets is populated, it is returned; otherwise Selectors is
// returned as a single selector set when non-empty.
func (s Service) EffectiveSelectorSets() [][]string {
	return effectiveSelectorSets(s.SelectorSets, s.Selectors)
}

// ValidateSelectors validates that any configured workload selectors and
// selector sets are non-empty and well-formed.
func (s Service) ValidateSelectors() error {
	return validateSelectorSets(s.SelectorSets, s.Selectors)
}

// EndpointHealthStatus indicates the health of a service endpoint.
type EndpointHealthStatus int

const (
	// EndpointHealthUnknown is the default state before any health check completes.
	EndpointHealthUnknown EndpointHealthStatus = iota
	// EndpointHealthHealthy indicates the endpoint is healthy and serving traffic.
	EndpointHealthHealthy
	// EndpointHealthUnhealthy indicates the endpoint has failed health checks.
	EndpointHealthUnhealthy
	// EndpointHealthDraining indicates the endpoint is shutting down gracefully.
	// No new connections should be routed to it; existing connections are allowed
	// to complete until the drain timeout.
	EndpointHealthDraining
)

// String returns the string representation of an EndpointHealthStatus.
func (s EndpointHealthStatus) String() string {
	switch s {
	case EndpointHealthHealthy:
		return "HEALTHY"
	case EndpointHealthUnhealthy:
		return "UNHEALTHY"
	case EndpointHealthDraining:
		return "DRAINING"
	default:
		return "UNKNOWN"
	}
}

// ServiceEndpoint represents a single network endpoint of a service.
type ServiceEndpoint struct {
	// Address is the host:port or IP:port of the endpoint.
	Address string
	// Weight is the relative routing weight (default: 100, range: 1-1000).
	Weight int
	// Health indicates the current health state of this endpoint.
	Health EndpointHealthStatus
}
