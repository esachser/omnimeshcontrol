package authz

import (
	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// AuthzPolicyToResource converts a domain AuthorizationPolicy to a generic Resource.
func AuthzPolicyToResource(p *model.AuthorizationPolicy) *model.Resource {
	if p == nil {
		return nil
	}
	res, err := codec.AuthzPolicyToResource(p)
	if err != nil {
		return nil
	}
	return res
}

// ResourceToAuthzPolicy converts a generic Resource back to a domain AuthorizationPolicy.
func ResourceToAuthzPolicy(r *model.Resource) (*model.AuthorizationPolicy, error) {
	return codec.ResourceToAuthzPolicy(r)
}
