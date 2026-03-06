package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// AgentScopeAppService implements the AgentScopeManager inbound port.
// It calculates the full scope of services, routes, policies, and health
// data that a given agent needs based on its registered services and the
// dependency graph.
type AgentScopeAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	policyStore outbound.TrafficPolicyStore
	healthStore outbound.EndpointHealthStore
	depStore    outbound.DependencyStore
	logger      *slog.Logger
}

// NewAgentScopeAppService creates a new AgentScopeAppService.
func NewAgentScopeAppService(
	resources outbound.ResourceStore,
	state outbound.StateStore,
	policyStore outbound.TrafficPolicyStore,
	healthStore outbound.EndpointHealthStore,
	depStore outbound.DependencyStore,
	logger *slog.Logger,
) *AgentScopeAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentScopeAppService{
		resources:   resources,
		state:       state,
		policyStore: policyStore,
		healthStore: healthStore,
		depStore:    depStore,
		logger:      logger,
	}
}

// GetAgentScope builds the full scope for an agent.
func (s *AgentScopeAppService) GetAgentScope(ctx context.Context, nodeID string) (*model.AgentScope, error) {
	scope := &model.AgentScope{NodeID: nodeID}

	// Get all services across all namespaces to find which ones this agent needs.
	// In a production system, this would be filtered by the node's registered services
	// and their dependency graph. For now, we include all services.
	allResources, err := s.resources.List(ctx, "core.omnimesh.io/v1/Service/")
	if err != nil {
		return nil, fmt.Errorf("scope: list services: %w", err)
	}

	for _, r := range allResources {
		svc, mapErr := resourceToService(r)
		if mapErr != nil {
			continue
		}
		addr, addrErr := s.state.GetIPAllocation(ctx, svc.ID)
		if addrErr == nil {
			svc.MeshAddr = addr
		}
		scope.Services = append(scope.Services, *svc)
	}

	// Get traffic policies for scoped services.
	for _, svc := range scope.Services {
		policy, pErr := s.policyStore.GetPolicyByService(ctx, svc.ID)
		if pErr == nil && policy != nil {
			scope.TrafficPolicies = append(scope.TrafficPolicies, *policy)
		}
	}

	// Get health data for scoped services.
	for _, svc := range scope.Services {
		healthRecords, hErr := s.healthStore.ListEndpointHealth(ctx, svc.ID)
		if hErr == nil {
			for _, h := range healthRecords {
				scope.EndpointHealth = append(scope.EndpointHealth, *h)
			}
		}
	}

	s.logger.Info("agent scope computed",
		"nodeID", nodeID,
		"services", len(scope.Services),
		"policies", len(scope.TrafficPolicies),
		"healthRecords", len(scope.EndpointHealth),
	)

	return scope, nil
}

// Compile-time interface check.
var _ inbound.AgentScopeManager = (*AgentScopeAppService)(nil)
