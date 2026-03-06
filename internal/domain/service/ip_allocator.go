// Package service contains domain services — pure business logic that operates
// on domain models. Domain services have no external dependencies.
package service

import (
	"fmt"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/domain"
)

// IPAllocator provides pure validation logic for mesh IP allocation.
// The actual persistence is handled by the StateStore outbound port;
// this service validates CIDR constraints and conflict detection.
type IPAllocator struct {
	cidr netip.Prefix
}

// NewIPAllocator creates a new IPAllocator for the given mesh CIDR.
func NewIPAllocator(cidr netip.Prefix) (*IPAllocator, error) {
	if !cidr.IsValid() {
		return nil, fmt.Errorf("ip allocator: %w: invalid CIDR %q", domain.ErrInvalidNamespace, cidr)
	}
	return &IPAllocator{cidr: cidr}, nil
}

// CIDR returns the mesh CIDR used by this allocator.
func (a *IPAllocator) CIDR() netip.Prefix {
	return a.cidr
}

// ValidateAddr checks whether the given address falls within the mesh CIDR.
func (a *IPAllocator) ValidateAddr(addr netip.Addr) error {
	if !a.cidr.Contains(addr) {
		return fmt.Errorf("ip allocator: address %s is not within mesh CIDR %s", addr, a.cidr)
	}
	return nil
}

// NetworkAddr returns the network address for the mesh CIDR (first address, typically unusable).
func (a *IPAllocator) NetworkAddr() netip.Addr {
	return a.cidr.Addr()
}
