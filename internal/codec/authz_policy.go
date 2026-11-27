package codec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

type authzSelectorSpec struct {
	Namespace      string            `json:"namespace,omitempty"`
	Name           string            `json:"name,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	SpiffeID       string            `json:"spiffeID,omitempty"`
	SpiffeIDSnake  string            `json:"spiffe_id,omitempty"`
	TrustDomain    string            `json:"trustDomain,omitempty"`
	TrustDomainSnk string            `json:"trust_domain,omitempty"`
}

type httpAuthzRuleSpec struct {
	Methods           []string          `json:"methods,omitempty"`
	PathPrefixes      []string          `json:"pathPrefixes,omitempty"`
	PathPrefixesSnake []string          `json:"path_prefixes,omitempty"`
	PathExact         []string          `json:"pathExact,omitempty"`
	PathExactSnake    []string          `json:"path_exact,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
}

type grpcAuthzRuleSpec struct {
	Services []string `json:"services,omitempty"`
	Methods  []string `json:"methods,omitempty"`
}

type authzRuleSpec struct {
	HTTP *httpAuthzRuleSpec `json:"http,omitempty"`
	GRPC *grpcAuthzRuleSpec `json:"grpc,omitempty"`
}

type authzPolicySpec struct {
	Action                string            `json:"action,omitempty"`
	Source                authzSelectorSpec `json:"source,omitempty"`
	Destination           authzSelectorSpec `json:"destination,omitempty"`
	DestinationPorts      []int             `json:"destinationPorts,omitempty"`
	DestinationPortsSnake []int             `json:"destination_ports,omitempty"`
	Rules                 []authzRuleSpec   `json:"rules,omitempty"`
	ValidFrom             string            `json:"validFrom,omitempty"`
	ValidFromSnake        string            `json:"valid_from,omitempty"`
	ValidUntil            string            `json:"validUntil,omitempty"`
	ValidUntilSnake       string            `json:"valid_until,omitempty"`
}

// AuthzPolicyToResource converts an AuthorizationPolicy into a Resource envelope.
func AuthzPolicyToResource(policy *model.AuthorizationPolicy) (*model.Resource, error) {
	specJSON, err := EncodeAuthzPolicySpec(policy)
	if err != nil {
		return nil, err
	}

	return &model.Resource{
		APIVersion: "policy.omnimesh.io/v1",
		Kind:       "AuthorizationPolicy",
		Metadata: model.ResourceMetadata{
			Name:      policy.Name,
			Namespace: policy.Namespace,
		},
		Spec: specJSON,
	}, nil
}

// ResourceToAuthzPolicy converts a Resource envelope into an AuthorizationPolicy.
func ResourceToAuthzPolicy(resource *model.Resource) (*model.AuthorizationPolicy, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource is nil")
	}
	if resource.Kind != "AuthorizationPolicy" {
		return nil, fmt.Errorf("expected kind AuthorizationPolicy, got %s", resource.Kind)
	}

	return DecodeAuthzPolicySpec(resource.Spec, resource.Metadata.Name, resource.Metadata.Namespace)
}

// EncodeAuthzPolicySpec encodes an AuthorizationPolicy into JSON spec bytes.
func EncodeAuthzPolicySpec(policy *model.AuthorizationPolicy) ([]byte, error) {
	if policy == nil {
		return nil, fmt.Errorf("authorization policy is nil")
	}

	spec := authzPolicySpec{
		Action: policy.Action.String(),
		Source: authzSelectorSpec{
			Namespace:   policy.Source.Namespace,
			Name:        policy.Source.Name,
			Labels:      cloneStringMap(policy.Source.Labels),
			SpiffeID:    policy.Source.SpiffeID,
			TrustDomain: policy.Source.TrustDomain,
		},
		Destination: authzSelectorSpec{
			Namespace:   policy.Destination.Namespace,
			Name:        policy.Destination.Name,
			Labels:      cloneStringMap(policy.Destination.Labels),
			SpiffeID:    policy.Destination.SpiffeID,
			TrustDomain: policy.Destination.TrustDomain,
		},
		DestinationPorts: append([]int(nil), policy.DestinationPorts...),
	}

	for _, r := range policy.Rules {
		var rs authzRuleSpec
		if r.HTTP != nil {
			rs.HTTP = &httpAuthzRuleSpec{
				Methods:      append([]string(nil), r.HTTP.Methods...),
				PathPrefixes: append([]string(nil), r.HTTP.PathPrefixes...),
				PathExact:    append([]string(nil), r.HTTP.PathExact...),
				Headers:      cloneStringMap(r.HTTP.Headers),
			}
		}
		if r.GRPC != nil {
			rs.GRPC = &grpcAuthzRuleSpec{
				Services: append([]string(nil), r.GRPC.Services...),
				Methods:  append([]string(nil), r.GRPC.Methods...),
			}
		}
		spec.Rules = append(spec.Rules, rs)
	}

	if policy.ValidFrom != nil {
		spec.ValidFrom = policy.ValidFrom.UTC().Format(time.RFC3339Nano)
	}
	if policy.ValidUntil != nil {
		spec.ValidUntil = policy.ValidUntil.UTC().Format(time.RFC3339Nano)
	}

	return json.Marshal(spec)
}

// DecodeAuthzPolicySpec decodes JSON spec bytes into an AuthorizationPolicy.
func DecodeAuthzPolicySpec(specJSON []byte, name, namespace string) (*model.AuthorizationPolicy, error) {
	var spec authzPolicySpec
	if len(specJSON) > 0 {
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal authz spec: %w", err)
		}
	}

	action, err := model.ParsePolicyAction(spec.Action)
	if err != nil {
		return nil, err
	}

	src, err := decodeSelectorSpec("source", spec.Source)
	if err != nil {
		return nil, err
	}
	dst, err := decodeSelectorSpec("destination", spec.Destination)
	if err != nil {
		return nil, err
	}

	destPorts, err := mergeIntSliceField("destinationPorts", spec.DestinationPorts, spec.DestinationPortsSnake)
	if err != nil {
		return nil, err
	}

	validFromStr, err := mergeStringField("validFrom", spec.ValidFrom, spec.ValidFromSnake)
	if err != nil {
		return nil, err
	}
	validUntilStr, err := mergeStringField("validUntil", spec.ValidUntil, spec.ValidUntilSnake)
	if err != nil {
		return nil, err
	}

	var validFrom, validUntil *time.Time
	if validFromStr != "" {
		t, err := time.Parse(time.RFC3339Nano, validFromStr)
		if err != nil {
			return nil, fmt.Errorf("parse validFrom: %w", err)
		}
		utc := t.UTC()
		validFrom = &utc
	}
	if validUntilStr != "" {
		t, err := time.Parse(time.RFC3339Nano, validUntilStr)
		if err != nil {
			return nil, fmt.Errorf("parse validUntil: %w", err)
		}
		utc := t.UTC()
		validUntil = &utc
	}

	var rules []model.AuthzRule
	if len(spec.Rules) > 0 {
		rules = make([]model.AuthzRule, 0, len(spec.Rules))
		for i, rs := range spec.Rules {
			var rule model.AuthzRule
			if rs.HTTP != nil {
				prefixes, err := mergeStringSliceField(fmt.Sprintf("rules[%d].http.pathPrefixes", i), rs.HTTP.PathPrefixes, rs.HTTP.PathPrefixesSnake)
				if err != nil {
					return nil, err
				}
				exact, err := mergeStringSliceField(fmt.Sprintf("rules[%d].http.pathExact", i), rs.HTTP.PathExact, rs.HTTP.PathExactSnake)
				if err != nil {
					return nil, err
				}
				rule.HTTP = &model.HTTPAuthzRule{
					Methods:      append([]string(nil), rs.HTTP.Methods...),
					PathPrefixes: prefixes,
					PathExact:    exact,
					Headers:      cloneStringMap(rs.HTTP.Headers),
				}
			}
			if rs.GRPC != nil {
				rule.GRPC = &model.GRPCAuthzRule{
					Services: append([]string(nil), rs.GRPC.Services...),
					Methods:  append([]string(nil), rs.GRPC.Methods...),
				}
			}
			rules = append(rules, rule)
		}
	}

	return &model.AuthorizationPolicy{
		Name:             name,
		Namespace:        namespace,
		Action:           action,
		Source:           src,
		Destination:      dst,
		DestinationPorts: destPorts,
		Rules:            rules,
		ValidFrom:        validFrom,
		ValidUntil:       validUntil,
	}, nil
}

func decodeSelectorSpec(role string, s authzSelectorSpec) (model.PolicySelector, error) {
	spiffeID, err := mergeStringField(role+".spiffeID", s.SpiffeID, s.SpiffeIDSnake)
	if err != nil {
		return model.PolicySelector{}, err
	}
	trustDomain, err := mergeStringField(role+".trustDomain", s.TrustDomain, s.TrustDomainSnk)
	if err != nil {
		return model.PolicySelector{}, err
	}
	return model.PolicySelector{
		Namespace:   s.Namespace,
		Name:        s.Name,
		Labels:      cloneStringMap(s.Labels),
		SpiffeID:    spiffeID,
		TrustDomain: trustDomain,
	}, nil
}

func mergeStringField(field, primary, alias string) (string, error) {
	if primary != "" && alias != "" && primary != alias {
		return "", fmt.Errorf("conflicting values for %s (%q vs %q)", field, primary, alias)
	}
	if primary != "" {
		return primary, nil
	}
	return alias, nil
}

func mergeStringSliceField(field string, primary, alias []string) ([]string, error) {
	if len(primary) > 0 && len(alias) > 0 && !reflect.DeepEqual(primary, alias) {
		return nil, fmt.Errorf("conflicting values for %s", field)
	}
	if len(primary) > 0 {
		return append([]string(nil), primary...), nil
	}
	if len(alias) > 0 {
		return append([]string(nil), alias...), nil
	}
	return nil, nil
}

func mergeIntSliceField(field string, primary, alias []int) ([]int, error) {
	if len(primary) > 0 && len(alias) > 0 && !reflect.DeepEqual(primary, alias) {
		return nil, fmt.Errorf("conflicting values for %s", field)
	}
	if len(primary) > 0 {
		return append([]int(nil), primary...), nil
	}
	if len(alias) > 0 {
		return append([]int(nil), alias...), nil
	}
	return nil, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
