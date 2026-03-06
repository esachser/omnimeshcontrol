package service

import "github.com/esachser/omnimeshcontrol/internal/domain/model"

// AuthzEvaluator evaluates authorization policies for a given source→destination
// service pair. The evaluation order follows the default-deny model:
//
//  1. If any DENY policy matches → DENY.
//  2. If any ALLOW policy matches → ALLOW.
//  3. If no policy matches → DENY (default deny).
type AuthzEvaluator struct{}

// NewAuthzEvaluator creates a new AuthzEvaluator.
func NewAuthzEvaluator() *AuthzEvaluator {
	return &AuthzEvaluator{}
}

// EvaluateResult is the outcome of an authorization evaluation.
type EvaluateResult int

const (
	// EvaluateResultDeny indicates the request is denied.
	EvaluateResultDeny EvaluateResult = iota
	// EvaluateResultAllow indicates the request is allowed.
	EvaluateResultAllow
)

// String returns the string representation of an EvaluateResult.
func (r EvaluateResult) String() string {
	if r == EvaluateResultAllow {
		return "ALLOW"
	}
	return "DENY"
}

// Evaluate checks whether traffic from source to destination is authorized
// given the set of applicable policies.
func (e *AuthzEvaluator) Evaluate(
	source model.ServiceID,
	destination model.ServiceID,
	policies []model.AuthorizationPolicy,
) EvaluateResult {
	// Step 1: Check DENY policies first.
	for i := range policies {
		p := &policies[i]
		if p.Action == model.PolicyActionDeny && e.matches(p, source, destination) {
			return EvaluateResultDeny
		}
	}

	// Step 2: Check ALLOW policies.
	for i := range policies {
		p := &policies[i]
		if p.Action == model.PolicyActionAllow && e.matches(p, source, destination) {
			return EvaluateResultAllow
		}
	}

	// Step 3: Default deny.
	return EvaluateResultDeny
}

// matches checks whether a policy matches the given source and destination.
func (e *AuthzEvaluator) matches(
	policy *model.AuthorizationPolicy,
	source model.ServiceID,
	destination model.ServiceID,
) bool {
	return e.selectorMatches(policy.Source, source) &&
		e.selectorMatches(policy.Destination, destination)
}

// selectorMatches checks whether a PolicySelector matches a ServiceID.
func (e *AuthzEvaluator) selectorMatches(selector model.PolicySelector, id model.ServiceID) bool {
	// Namespace must match.
	if selector.Namespace != "" && selector.Namespace != id.Namespace {
		return false
	}
	// Name must match (wildcard "*" matches any name).
	if selector.Name != "*" && selector.Name != "" && selector.Name != id.Name {
		return false
	}
	// Label matching is not performed here — labels are resolved at the application layer
	// by filtering services before calling Evaluate.
	return true
}
