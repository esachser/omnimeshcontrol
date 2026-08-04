package scope

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
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
	routes      outbound.RouteDistributor
	targets     inbound.TargetManager
	authz       inbound.AuthorizationPolicyManager
	logger      *slog.Logger
}

// NewAgentScopeAppService creates a new AgentScopeAppService.
func NewAgentScopeAppService(
	resources outbound.ResourceStore,
	state outbound.StateStore,
	policyStore outbound.TrafficPolicyStore,
	healthStore outbound.EndpointHealthStore,
	depStore outbound.DependencyStore,
	routes outbound.RouteDistributor,
	targets inbound.TargetManager,
	authz inbound.AuthorizationPolicyManager,
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
		routes:      routes,
		targets:     targets,
		authz:       authz,
		logger:      logger,
	}
}

// GetAgentScope builds the full scope for an agent.
func (s *AgentScopeAppService) GetAgentScope(ctx context.Context, nodeID string) (*model.AgentScope, error) {
	scope := &model.AgentScope{NodeID: nodeID}

	allResources, err := s.resources.List(ctx, "core.omnimesh.io/v1/Service/")
	if err != nil {
		return nil, fmt.Errorf("scope: list services: %w", err)
	}

	for _, r := range allResources {
		svc, mapErr := mapper.ResourceToService(r)
		if mapErr != nil {
			continue
		}
		addr, addrErr := s.state.GetIPAllocation(ctx, svc.ID)
		if addrErr == nil {
			svc.MeshAddr = addr
		}
		scope.Services = append(scope.Services, *svc)
	}

	for _, svc := range scope.Services {
		policy, pErr := s.policyStore.GetPolicyByService(ctx, svc.ID)
		if pErr == nil && policy != nil {
			scope.TrafficPolicies = append(scope.TrafficPolicies, *policy)
		}
	}

	for _, svc := range scope.Services {
		healthRecords, hErr := s.healthStore.ListEndpointHealth(ctx, svc.ID)
		if hErr == nil {
			for _, h := range healthRecords {
				scope.EndpointHealth = append(scope.EndpointHealth, *h)
			}
		}
	}

	reqNode, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		s.logger.Warn("failed to get requesting node locality", "nodeID", nodeID, "error", err)
	}
	reqTarget := ""
	if reqNode != nil {
		reqTarget = reqNode.Target
	}

	if s.routes != nil {
		allRoutes := s.routes.AllRoutes()
		for _, tr := range allRoutes {
			routeCopy := model.TargetRoute{
				Name:          tr.Name,
				Namespace:     tr.Namespace,
				TargetService: tr.TargetService,
				NodeID:        tr.NodeID,
				Labels:        tr.Labels,
			}

			destNode, nErr := s.state.GetNode(ctx, tr.NodeID)
			if nErr == nil && destNode != nil {
				routeCopy.Address = destNode.Addr
				routeCopy.Target = destNode.Target
				if destNode.Target != reqTarget && destNode.Target != "" {
					netTarget, tErr := s.targets.Get(ctx, destNode.Target)
					if tErr == nil && netTarget != nil && netTarget.GatewayAddress != "" {
						routeCopy.TargetAddress = netTarget.GatewayAddress
					}
				}
			}

			scope.Routes = append(scope.Routes, routeCopy)
		}
	}

	authzPolicies, err := s.authz.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("scope: list authorization policies: %w", err)
	}
	scope.AuthzPolicies = make([]model.AuthorizationPolicy, len(authzPolicies))
	for i, ap := range authzPolicies {
		scope.AuthzPolicies[i] = *ap
	}

	s.logger.Info("agent scope computed",
		"nodeID", nodeID,
		"services", len(scope.Services),
		"policies", len(scope.TrafficPolicies),
		"healthRecords", len(scope.EndpointHealth),
		"routes", len(scope.Routes),
	)

	return scope, nil
}

// Compile-time interface check.
var _ inbound.AgentScopeManager = (*AgentScopeAppService)(nil)
