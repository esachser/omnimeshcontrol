package model

import (
	"fmt"
	"strings"
)

// ServiceIdentity represents the SPIFFE identity of a service in the mesh.
// Each service receives a SPIFFE Verifiable Identity Document (SVID) that
// enables mTLS authentication.
type ServiceIdentity struct {
	// SPIFFEID is the SPIFFE identity URI (e.g., "spiffe://omnimesh.local/ns/production/sa/order-service").
	SPIFFEID string
	// ServiceID is the mesh service this identity belongs to.
	ServiceID ServiceID
	// Selectors defines a single set of workload selectors (deprecated: use SelectorSets).
	Selectors []string
	// SelectorSets defines one or more independent sets of workload selectors used
	// to register this identity in SPIRE. Selectors within each set are AND-ed;
	// multiple sets are registered as separate SPIRE entries (OR-ed).
	SelectorSets [][]string
}

// EffectiveSelectorSets returns the normalized list of selector sets for this
// identity. If SelectorSets is populated, it is returned; otherwise Selectors is
// returned as a single selector set when non-empty.
func (id ServiceIdentity) EffectiveSelectorSets() [][]string {
	return effectiveSelectorSets(id.SelectorSets, id.Selectors)
}

func effectiveSelectorSets(sets [][]string, legacy []string) [][]string {
	if len(sets) > 0 {
		out := make([][]string, len(sets))
		for i, set := range sets {
			cp := make([]string, len(set))
			copy(cp, set)
			out[i] = cp
		}
		return out
	}
	if len(legacy) > 0 {
		cp := make([]string, len(legacy))
		copy(cp, legacy)
		return [][]string{cp}
	}
	return nil
}

func validateSelectorSets(sets [][]string, legacy []string) error {
	if len(sets) > 0 && len(legacy) > 0 {
		return fmt.Errorf("cannot specify both selectors and selectorSets")
	}
	for i, set := range sets {
		if len(set) == 0 {
			return fmt.Errorf("selector set %d must contain at least one selector", i)
		}
		for j, sel := range set {
			if strings.TrimSpace(sel) == "" {
				return fmt.Errorf("selector set %d contains an empty selector at index %d", i, j)
			}
		}
	}
	for j, sel := range legacy {
		if strings.TrimSpace(sel) == "" {
			return fmt.Errorf("selectors contains an empty selector at index %d", j)
		}
	}
	return nil
}
