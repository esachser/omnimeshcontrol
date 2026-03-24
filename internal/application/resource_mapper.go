package application

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/codec"
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
	CIDR      string `json:"cidr,omitempty"`
}

// namespaceToResource converts a domain Namespace to a generic Resource.
func namespaceToResource(ns *model.Namespace) *model.Resource {
	spec := namespaceSpec{Isolation: ns.Isolation.String()}
	if ns.CIDR.IsValid() {
		spec.CIDR = ns.CIDR.String()
	}
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

	if spec.CIDR != "" {
		cidr, err := netip.ParsePrefix(spec.CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse namespace CIDR %q: %w", spec.CIDR, err)
		}
		ns.CIDR = cidr
	}

	return ns, nil
}

// targetSpec is the JSON spec representation for NetworkTarget resources.
type targetSpec struct {
	GatewayAddress string `json:"gatewayAddress,omitempty"`
}

// targetToResource converts a domain NetworkTarget to a generic Resource.
func targetToResource(t *model.NetworkTarget) *model.Resource {
	spec := targetSpec{GatewayAddress: t.GatewayAddress}
	specJSON, _ := json.Marshal(spec)

	return &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "NetworkTarget",
		Metadata: model.ResourceMetadata{
			Name: t.Name,
		},
		Spec: specJSON,
	}
}

// resourceToTarget converts a generic Resource back to a domain NetworkTarget.
func resourceToTarget(r *model.Resource) (*model.NetworkTarget, error) {
	if r.Kind != "NetworkTarget" {
		return nil, fmt.Errorf("expected kind NetworkTarget, got %s", r.Kind)
	}

	var spec targetSpec
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal target spec: %w", err)
		}
	}

	t := &model.NetworkTarget{
		Name:           r.Metadata.Name,
		GatewayAddress: spec.GatewayAddress,
	}

	return t, nil
}

// trafficRouteDestinationSpec is the JSON representation for TrafficRouteDestination.
type trafficRouteDestinationSpec struct {
	Target string            `json:"target,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// trafficRouteSpec is the JSON representation for a TrafficRoute within a policy.
type trafficRouteSpec struct {
	Destination trafficRouteDestinationSpec `json:"destination,omitempty"`
	Weight      int                         `json:"weight,omitempty"`
}

// trafficPolicyToResource converts a domain TrafficPolicy to a generic Resource.
func trafficPolicyToResource(tp *model.TrafficPolicy) (*model.Resource, error) {
	return codec.TrafficPolicyToResource(tp)
}

// resourceToTrafficPolicy converts a generic Resource back to a domain TrafficPolicy.
func resourceToTrafficPolicy(r *model.Resource) (*model.TrafficPolicy, error) {
	return codec.ResourceToTrafficPolicy(r)
}
