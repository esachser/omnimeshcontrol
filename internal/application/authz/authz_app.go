package authz

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// AuthzAppService implements the AuthorizationPolicyManager inbound port.
// It manages authorization policies and evaluates access control using the
// domain AuthzEvaluator (DENY → ALLOW → default DENY chain).
type AuthzAppService struct {
	resources outbound.ResourceStore
	depStore  outbound.DependencyStore
	evaluator *domainservice.AuthzEvaluator
	logger    *slog.Logger
}

// NewAuthzAppService creates a new AuthzAppService.
func NewAuthzAppService(
	resources outbound.ResourceStore,
	depStore outbound.DependencyStore,
	evaluator *domainservice.AuthzEvaluator,
	logger *slog.Logger,
) *AuthzAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthzAppService{
		resources: resources,
		depStore:  depStore,
		evaluator: evaluator,
		logger:    logger,
	}
}

// Create creates a new authorization policy.
func (s *AuthzAppService) Create(ctx context.Context, policy *model.AuthorizationPolicy) error {
	if policy.Name == "" {
		return fmt.Errorf("authz create: %w: name is required", domain.ErrInvalidName)
	}

	key := s.policyKey(policy.Namespace, policy.Name)
	if _, err := s.resources.Get(ctx, key); err == nil {
		return fmt.Errorf("authz create: %w", domain.ErrDuplicatePolicy)
	}

	resource := AuthzPolicyToResource(policy)
	if _, err := s.resources.Put(ctx, key, resource); err != nil {
		return fmt.Errorf("authz create: %w", err)
	}

	if policy.Action == model.PolicyActionAllow {
		if err := s.updateDependencies(ctx, policy); err != nil {
			s.logger.Warn("failed to update dependencies for authz policy",
				"name", policy.Name, "error", err)
		}
	}

	s.logger.Info("authorization policy created",
		"name", policy.Name,
		"namespace", policy.Namespace,
		"action", policy.Action,
	)
	return nil
}

func (s *AuthzAppService) Update(ctx context.Context, policy *model.AuthorizationPolicy) error {
	if policy.Name == "" {
		return fmt.Errorf("authz update: %w: name is required", domain.ErrInvalidName)
	}

	resource := AuthzPolicyToResource(policy)
	if _, err := s.resources.Put(ctx, s.policyKey(policy.Namespace, policy.Name), resource); err != nil {
		return fmt.Errorf("authz update: %w", err)
	}

	if policy.Action == model.PolicyActionAllow {
		if err := s.updateDependencies(ctx, policy); err != nil {
			s.logger.Warn("failed to update dependencies for authz policy",
				"name", policy.Name, "error", err)
		}
	}

	s.logger.Info("authorization policy updated",
		"name", policy.Name,
		"namespace", policy.Namespace,
		"action", policy.Action,
	)
	return nil
}

// Get retrieves an authorization policy by namespace and name.
func (s *AuthzAppService) Get(ctx context.Context, namespace, name string) (*model.AuthorizationPolicy, error) {
	key := s.policyKey(namespace, name)
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("authz get: %w", domain.ErrAuthzPolicyNotFound)
	}
	return ResourceToAuthzPolicy(resource)
}

// List returns all authorization policies in a namespace.
func (s *AuthzAppService) List(ctx context.Context, namespace string) ([]*model.AuthorizationPolicy, error) {
	prefix := fmt.Sprintf("policy.omnimesh.io/v1/AuthorizationPolicy/%s/", namespace)
	if namespace == "" {
		prefix = "policy.omnimesh.io/v1/AuthorizationPolicy/"
	}
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("authz list: %w", err)
	}

	policies := make([]*model.AuthorizationPolicy, 0, len(resources))
	for _, r := range resources {
		p, pErr := ResourceToAuthzPolicy(r)
		if pErr != nil {
			s.logger.Warn("skipping invalid authz policy resource", "error", pErr)
			continue
		}
		policies = append(policies, p)
	}
	return policies, nil
}

// Delete removes an authorization policy.
func (s *AuthzAppService) Delete(ctx context.Context, namespace, name string) error {
	key := s.policyKey(namespace, name)
	if err := s.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("authz delete: %w", err)
	}

	s.logger.Info("authorization policy deleted", "name", name, "namespace", namespace)
	return nil
}

// Evaluate checks whether traffic from source to destination is authorized.
func (s *AuthzAppService) Evaluate(ctx context.Context, source, destination model.ServiceID) (bool, error) {
	srcPolicies, err := s.List(ctx, source.Namespace)
	if err != nil {
		return false, err
	}
	dstPolicies, err := s.List(ctx, destination.Namespace)
	if err != nil {
		return false, err
	}

	seen := make(map[string]bool)
	var allPolicies []model.AuthorizationPolicy
	for _, p := range append(srcPolicies, dstPolicies...) {
		key := p.Namespace + "/" + p.Name
		if !seen[key] {
			seen[key] = true
			allPolicies = append(allPolicies, *p)
		}
	}

	result := s.evaluator.Evaluate(source, destination, allPolicies)
	return result == domainservice.EvaluateResultAllow, nil
}

func (s *AuthzAppService) policyKey(namespace, name string) string {
	return fmt.Sprintf("policy.omnimesh.io/v1/AuthorizationPolicy/%s/%s", namespace, name)
}

func (s *AuthzAppService) updateDependencies(ctx context.Context, policy *model.AuthorizationPolicy) error {
	dep := model.ServiceDependency{
		Source: model.ServiceID{
			Namespace: policy.Source.Namespace,
			Name:      policy.Source.Name,
		},
		Target: model.ServiceID{
			Namespace: policy.Destination.Namespace,
			Name:      policy.Destination.Name,
		},
		Origin:     model.DependencyAuthzPolicy,
		PolicyName: policy.Name,
	}

	deps := &model.ServiceDependencies{
		ServiceID:    dep.Source,
		Dependencies: []model.ServiceDependency{dep},
	}

	return s.depStore.PutDependencies(ctx, deps)
}

// Compile-time interface check.
var _ inbound.AuthorizationPolicyManager = (*AuthzAppService)(nil)
