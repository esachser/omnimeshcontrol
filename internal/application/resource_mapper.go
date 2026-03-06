package application

import (
	"encoding/json"
	"fmt"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// serviceSpec is the JSON spec representation for Service resources.
type serviceSpec struct {
	Protocol     string   `json:"protocol,omitempty"`
	Port         int      `json:"port,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// serviceToResource converts a domain Service to a generic Resource for storage.
func serviceToResource(svc *model.Service) *model.Resource {
	spec := serviceSpec{
		Protocol: svc.Protocol.String(),
		Port:     svc.Port,
	}
	for _, dep := range svc.Dependencies {
		spec.Dependencies = append(spec.Dependencies, dep.String())
	}

	specJSON, _ := json.Marshal(spec)

	return &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata: model.ResourceMetadata{
			Name:      svc.ID.Name,
			Namespace: svc.ID.Namespace,
			Labels:    svc.Labels,
		},
		Spec: specJSON,
	}
}

// resourceToService converts a generic Resource back to a domain Service.
func resourceToService(r *model.Resource) (*model.Service, error) {
	if r.Kind != "Service" {
		return nil, fmt.Errorf("expected kind Service, got %s", r.Kind)
	}

	var spec serviceSpec
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal service spec: %w", err)
		}
	}

	svc := &model.Service{
		ID: model.ServiceID{
			Namespace: r.Metadata.Namespace,
			Name:      r.Metadata.Name,
		},
		Labels: r.Metadata.Labels,
		Port:   spec.Port,
	}

	switch spec.Protocol {
	case "HTTP":
		svc.Protocol = model.ServiceProtocolHTTP
	case "gRPC":
		svc.Protocol = model.ServiceProtocolGRPC
	default:
		svc.Protocol = model.ServiceProtocolTCP
	}

	return svc, nil
}

// namespaceSpec is the JSON spec representation for Namespace resources.
type namespaceSpec struct {
	Isolation string `json:"isolation,omitempty"`
}

// namespaceToResource converts a domain Namespace to a generic Resource.
func namespaceToResource(ns *model.Namespace) *model.Resource {
	spec := namespaceSpec{Isolation: ns.Isolation.String()}
	specJSON, _ := json.Marshal(spec)

	return &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Namespace",
		Metadata: model.ResourceMetadata{
			Name:   ns.Name,
			Labels: ns.Labels,
		},
		Spec: specJSON,
	}
}

// resourceToNamespace converts a generic Resource back to a domain Namespace.
func resourceToNamespace(r *model.Resource) (*model.Namespace, error) {
	if r.Kind != "Namespace" {
		return nil, fmt.Errorf("expected kind Namespace, got %s", r.Kind)
	}

	var spec namespaceSpec
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal namespace spec: %w", err)
		}
	}

	ns := &model.Namespace{
		Name:   r.Metadata.Name,
		Labels: r.Metadata.Labels,
	}

	switch spec.Isolation {
	case "open":
		ns.Isolation = model.NamespaceIsolationOpen
	default:
		ns.Isolation = model.NamespaceIsolationStrict
	}

	return ns, nil
}
