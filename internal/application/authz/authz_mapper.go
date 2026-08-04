package authz

import (
	"encoding/json"
	"fmt"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// authzSpec is the JSON spec representation for AuthorizationPolicy resources.
type authzSpec struct {
	Action      string       `json:"action"`
	Source      selectorSpec `json:"source"`
	Destination selectorSpec `json:"destination"`
}

type selectorSpec struct {
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// AuthzPolicyToResource converts a domain AuthorizationPolicy to a generic Resource.
func AuthzPolicyToResource(p *model.AuthorizationPolicy) *model.Resource {
	spec := authzSpec{
		Action: p.Action.String(),
		Source: selectorSpec{
			Namespace: p.Source.Namespace,
			Name:      p.Source.Name,
			Labels:    p.Source.Labels,
		},
		Destination: selectorSpec{
			Namespace: p.Destination.Namespace,
			Name:      p.Destination.Name,
			Labels:    p.Destination.Labels,
		},
	}

	specJSON, _ := json.Marshal(spec)

	return &model.Resource{
		APIVersion: "policy.omnimesh.io/v1",
		Kind:       "AuthorizationPolicy",
		Metadata: model.ResourceMetadata{
			Name:      p.Name,
			Namespace: p.Namespace,
		},
		Spec: specJSON,
	}
}

// ResourceToAuthzPolicy converts a generic Resource back to a domain AuthorizationPolicy.
func ResourceToAuthzPolicy(r *model.Resource) (*model.AuthorizationPolicy, error) {
	if r.Kind != "AuthorizationPolicy" {
		return nil, fmt.Errorf("expected kind AuthorizationPolicy, got %s", r.Kind)
	}

	var spec authzSpec
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal authz spec: %w", err)
		}
	}

	p := &model.AuthorizationPolicy{
		Name:      r.Metadata.Name,
		Namespace: r.Metadata.Namespace,
		Source: model.PolicySelector{
			Namespace: spec.Source.Namespace,
			Name:      spec.Source.Name,
			Labels:    spec.Source.Labels,
		},
		Destination: model.PolicySelector{
			Namespace: spec.Destination.Namespace,
			Name:      spec.Destination.Name,
			Labels:    spec.Destination.Labels,
		},
	}

	switch spec.Action {
	case "DENY":
		p.Action = model.PolicyActionDeny
	default:
		p.Action = model.PolicyActionAllow
	}

	return p, nil
}
