package mapper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// serviceSpec is the JSON spec representation for Service resources.
type serviceSpec struct {
	Protocol     string     `json:"protocol,omitempty"`
	Port         int        `json:"port,omitempty"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Selectors    []string   `json:"selectors,omitempty"`
	SelectorSets [][]string `json:"selectorSets,omitempty"`
}

type rawServiceSpec struct {
	Protocol     string          `json:"protocol,omitempty"`
	Port         int             `json:"port,omitempty"`
	Dependencies []string        `json:"dependencies,omitempty"`
	Selectors    json.RawMessage `json:"selectors,omitempty"`
	SelectorSets json.RawMessage `json:"selectorSets,omitempty"`
}

// ServiceToResource converts a domain Service to a generic Resource for storage.
func ServiceToResource(svc *model.Service) *model.Resource {
	spec := serviceSpec{
		Protocol:     svc.Protocol.String(),
		Port:         svc.Port,
		Selectors:    svc.Selectors,
		SelectorSets: svc.SelectorSets,
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

// ResourceToService converts a generic Resource back to a domain Service.
func ResourceToService(r *model.Resource) (*model.Service, error) {
	if r.Kind != "Service" {
		return nil, fmt.Errorf("expected kind Service, got %s", r.Kind)
	}

	var spec rawServiceSpec
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

	if err := decodeServiceSelectors(&spec, svc); err != nil {
		return nil, err
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

func decodeServiceSelectors(spec *rawServiceSpec, svc *model.Service) error {
	if len(spec.SelectorSets) > 0 && !bytes.Equal(bytes.TrimSpace(spec.SelectorSets), []byte("null")) {
		sets, err := decodeSelectorSets(spec.SelectorSets)
		if err != nil {
			return fmt.Errorf("unmarshal service selectorSets: %w", err)
		}
		svc.SelectorSets = sets
	}

	if len(spec.Selectors) > 0 && !bytes.Equal(bytes.TrimSpace(spec.Selectors), []byte("null")) {
		var flat []string
		if err := json.Unmarshal(spec.Selectors, &flat); err == nil {
			svc.Selectors = flat
			return nil
		}

		// Also accept nested selector sets under "selectors" if selectorSets was not already provided.
		if len(svc.SelectorSets) == 0 {
			if sets, err := decodeSelectorSets(spec.Selectors); err == nil {
				svc.SelectorSets = sets
				return nil
			}
		}
		return fmt.Errorf("unmarshal service selectors: expected string list or list of selector sets")
	}

	return nil
}

func decodeSelectorSets(raw json.RawMessage) ([][]string, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, err
	}

	sets := make([][]string, 0, len(elems))
	for i, elem := range elems {
		trimmed := bytes.TrimSpace(elem)
		switch {
		case bytes.HasPrefix(trimmed, []byte("[")):
			var set []string
			if err := json.Unmarshal(trimmed, &set); err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			sets = append(sets, set)
		case bytes.HasPrefix(trimmed, []byte("{")):
			var obj struct {
				Selectors []string `json:"selectors"`
			}
			if err := json.Unmarshal(trimmed, &obj); err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			sets = append(sets, obj.Selectors)
		default:
			return nil, fmt.Errorf("index %d: expected array of selectors or {selectors: [...]}", i)
		}
	}

	return sets, nil
}

// namespaceSpec is the JSON spec representation for Namespace resources.
type namespaceSpec struct {
	Isolation string   `json:"isolation,omitempty"`
	CIDR      string   `json:"cidr,omitempty"`
	Subnets   []string `json:"subnets,omitempty"`
}

// NamespaceToResource converts a domain Namespace to a generic Resource.
func NamespaceToResource(ns *model.Namespace) *model.Resource {
	spec := namespaceSpec{Isolation: ns.Isolation.String()}
	if ns.CIDR.IsValid() {
		spec.CIDR = ns.CIDR.String()
	}
	for _, sub := range ns.Subnets {
		if sub.IsValid() {
			spec.Subnets = append(spec.Subnets, sub.String())
		}
	}
	if len(spec.Subnets) == 0 && spec.CIDR != "" {
		spec.Subnets = []string{spec.CIDR}
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

// ResourceToNamespace converts a generic Resource back to a domain Namespace.
func ResourceToNamespace(r *model.Resource) (*model.Namespace, error) {
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

	for _, subStr := range spec.Subnets {
		sub, err := netip.ParsePrefix(subStr)
		if err != nil {
			return nil, fmt.Errorf("parse namespace subnet %q: %w", subStr, err)
		}
		ns.Subnets = append(ns.Subnets, sub)
	}

	if len(ns.Subnets) == 0 && ns.CIDR.IsValid() {
		ns.Subnets = []netip.Prefix{ns.CIDR}
	} else if !ns.CIDR.IsValid() && len(ns.Subnets) > 0 {
		ns.CIDR = ns.Subnets[0]
	}

	return ns, nil
}

// targetSpec is the JSON spec representation for NetworkTarget resources.
type targetSpec struct {
	GatewayAddress string `json:"gatewayAddress,omitempty"`
}

// TargetToResource converts a domain NetworkTarget to a generic Resource.
func TargetToResource(t *model.NetworkTarget) *model.Resource {
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

// ResourceToTarget converts a generic Resource back to a domain NetworkTarget.
func ResourceToTarget(r *model.Resource) (*model.NetworkTarget, error) {
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

// TrafficPolicyToResource converts a domain TrafficPolicy to a generic Resource.
func TrafficPolicyToResource(tp *model.TrafficPolicy) (*model.Resource, error) {
	return codec.TrafficPolicyToResource(tp)
}

// ResourceToTrafficPolicy converts a generic Resource back to a domain TrafficPolicy.
func ResourceToTrafficPolicy(r *model.Resource) (*model.TrafficPolicy, error) {
	return codec.ResourceToTrafficPolicy(r)
}
