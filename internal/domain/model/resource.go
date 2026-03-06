// Package model defines the core domain entities and value objects for the
// Omnimesh control plane. These types are pure Go with no external dependencies.
package model

import (
	"encoding/json"
	"time"
)

// Resource is the generic envelope for all declarative resources in the mesh.
// It mirrors the Kubernetes-style apiVersion/kind/metadata/spec structure,
// allowing the ResourceStore to persist any resource type uniformly.
type Resource struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ResourceMetadata `json:"metadata"`
	Spec       json.RawMessage  `json:"spec"`
}

// ResourceMetadata contains the common metadata fields shared by all resources.
type ResourceMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Revision  int64             `json:"revision,omitempty"`
	CreatedAt time.Time         `json:"createdAt,omitempty"`
	UpdatedAt time.Time         `json:"updatedAt,omitempty"`
}

// ResourceKey returns the storage key for this resource in the format
// "{namespace}/{name}" or just "{name}" for cluster-scoped resources.
func (m ResourceMetadata) ResourceKey() string {
	if m.Namespace != "" {
		return m.Namespace + "/" + m.Name
	}
	return m.Name
}
