package model

import "net/netip"

// NamespaceIsolation determines the default authorization behavior within a namespace.
type NamespaceIsolation int

const (
	// NamespaceIsolationStrict is the default: services within the namespace
	// cannot communicate unless an explicit ALLOW policy exists.
	NamespaceIsolationStrict NamespaceIsolation = iota
	// NamespaceIsolationOpen allows free communication between services
	// within the namespace (no explicit ALLOW policy required).
	NamespaceIsolationOpen
)

// String returns the string representation of a NamespaceIsolation.
func (n NamespaceIsolation) String() string {
	switch n {
	case NamespaceIsolationOpen:
		return "open"
	default:
		return "strict"
	}
}

// Namespace is a logical grouping for mesh resources. A namespace must exist
// before any namespaced resource can be created within it.
type Namespace struct {
	Name      string
	Labels    map[string]string
	Isolation NamespaceIsolation
	// CIDR is the IP prefix used for allocating mesh IPs to services in this namespace.
	// It is immutable after namespace creation.
	CIDR netip.Prefix
	// Subnets contains all IP prefixes owned by omnimesh in this namespace.
	Subnets []netip.Prefix
}

// AllSubnets returns all subnets associated with this namespace.
// If Subnets is populated, it returns Subnets; otherwise if CIDR is valid,
// it returns a slice containing CIDR.
func (n Namespace) AllSubnets() []netip.Prefix {
	if len(n.Subnets) > 0 {
		return n.Subnets
	}
	if n.CIDR.IsValid() {
		return []netip.Prefix{n.CIDR}
	}
	return nil
}

