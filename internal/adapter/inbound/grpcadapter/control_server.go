// Package grpcadapter implements the gRPC inbound adapter that drives
// the application services via the generated protobuf service interfaces.
package grpcadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ControlPlaneServer implements the ControlPlaneService gRPC server.
type ControlPlaneServer struct {
	omnimeshv1.UnimplementedControlPlaneServiceServer
	services   inbound.ServiceManager
	namespaces inbound.NamespaceManager
	targets    inbound.TargetManager
	policies   inbound.TrafficPolicyManager
	authz      inbound.AuthorizationPolicyManager
	logger     *slog.Logger
}

// NewControlPlaneServer creates a new ControlPlaneServer.
func NewControlPlaneServer(
	services inbound.ServiceManager,
	namespaces inbound.NamespaceManager,
	targets inbound.TargetManager,
	policies inbound.TrafficPolicyManager,
	authz inbound.AuthorizationPolicyManager,
	logger *slog.Logger,
) *ControlPlaneServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &ControlPlaneServer{
		services:   services,
		namespaces: namespaces,
		targets:    targets,
		policies:   policies,
		authz:      authz,
		logger:     logger,
	}
}

// ApplyResource handles apply requests for all resource kinds.
func (s *ControlPlaneServer) ApplyResource(ctx context.Context, req *omnimeshv1.ApplyResourceRequest) (*omnimeshv1.ApplyResourceResponse, error) {
	if req.Resource == nil {
		return nil, status.Error(codes.InvalidArgument, "resource is required")
	}

	r := req.Resource
	switch r.Kind {
	case "Service":
		return s.applyService(ctx, r)
	case "Namespace":
		return s.applyNamespace(ctx, r)
	case "NetworkTarget":
		return s.applyNetworkTarget(ctx, r)
	case "TrafficPolicy":
		return s.applyTrafficPolicy(ctx, r)
	case "AuthorizationPolicy":
		return s.applyAuthorizationPolicy(ctx, r)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported resource kind: %s", r.Kind)
	}
}

func (s *ControlPlaneServer) applyService(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	// Parse spec.
	var spec struct {
		Protocol  string   `json:"protocol"`
		Port      int      `json:"port"`
		Selectors []string `json:"selectors,omitempty"`
	}
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid spec: %v", err)
		}
	}

	svc := &model.Service{
		ID: model.ServiceID{
			Namespace: r.Metadata.Namespace,
			Name:      r.Metadata.Name,
		},
		Labels:    r.Metadata.Labels,
		Port:      spec.Port,
		Selectors: spec.Selectors,
	}

	switch spec.Protocol {
	case "HTTP":
		svc.Protocol = model.ServiceProtocolHTTP
	case "gRPC":
		svc.Protocol = model.ServiceProtocolGRPC
	default:
		svc.Protocol = model.ServiceProtocolTCP
	}

	if err := s.services.Apply(ctx, svc); err != nil {
		return nil, status.Errorf(codes.Internal, "apply service: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: serviceToProto(svc),
	}, nil
}

func (s *ControlPlaneServer) applyNamespace(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	resource := &model.Resource{
		APIVersion: r.ApiVersion,
		Kind:       r.Kind,
		Metadata: model.ResourceMetadata{
			Name:      r.Metadata.Name,
			Namespace: r.Metadata.Namespace,
			Labels:    r.Metadata.Labels,
		},
		Spec: r.Spec,
	}

	ns, err := mapper.ResourceToNamespace(resource)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid namespace: %v", err)
	}

	if err := s.namespaces.Apply(ctx, ns); err != nil {
		return nil, status.Errorf(codes.Internal, "apply namespace: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: namespaceToProto(ns),
	}, nil
}

func (s *ControlPlaneServer) applyNetworkTarget(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	var spec struct {
		GatewayAddress string `json:"gatewayAddress"`
	}
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid spec: %v", err)
		}
	}

	target := &model.NetworkTarget{
		Name:           r.Metadata.Name,
		GatewayAddress: spec.GatewayAddress,
	}

	if err := s.targets.Apply(ctx, target); err != nil {
		return nil, status.Errorf(codes.Internal, "apply network target: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: networkTargetToProto(target),
	}, nil
}

func (s *ControlPlaneServer) applyTrafficPolicy(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	resource := &model.Resource{
		APIVersion: r.ApiVersion,
		Kind:       r.Kind,
		Metadata: model.ResourceMetadata{
			Name:      r.Metadata.Name,
			Namespace: r.Metadata.Namespace,
			Labels:    r.Metadata.Labels,
		},
		Spec: r.Spec,
	}

	policy, err := codec.ResourceToTrafficPolicy(resource)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid traffic policy: %v", err)
	}

	_, getErr := s.policies.GetTrafficPolicy(ctx, policy.Namespace, policy.Name)
	if getErr != nil {
		if !errors.Is(getErr, domain.ErrPolicyNotFound) {
			return nil, status.Errorf(codes.Internal, "get traffic policy: %v", getErr)
		}
		if err := s.policies.CreateTrafficPolicy(ctx, policy); err != nil {
			return nil, status.Errorf(codes.Internal, "create traffic policy: %v", err)
		}
	} else {
		if err := s.policies.UpdateTrafficPolicy(ctx, policy); err != nil {
			return nil, status.Errorf(codes.Internal, "update traffic policy: %v", err)
		}
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: trafficPolicyResourceToProto(policy),
	}, nil
}

func (s *ControlPlaneServer) applyAuthorizationPolicy(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	var spec struct {
		Action string `json:"action"`
		Source struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"source"`
		Destination struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"destination"`
	}
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid spec: %v", err)
		}
	}

	policy := &model.AuthorizationPolicy{
		Name:      r.Metadata.Name,
		Namespace: r.Metadata.Namespace,
		Source: model.PolicySelector{
			Namespace: spec.Source.Namespace,
			Name:      spec.Source.Name,
		},
		Destination: model.PolicySelector{
			Namespace: spec.Destination.Namespace,
			Name:      spec.Destination.Name,
		},
	}

	switch spec.Action {
	case "DENY", "deny":
		policy.Action = model.PolicyActionDeny
	default:
		policy.Action = model.PolicyActionAllow
	}

	// Try get first — if exists, delete and recreate (idempotent apply).
	_, getErr := s.authz.Get(ctx, policy.Namespace, policy.Name)
	if getErr != nil {
		// Policy does not exist — create.
		if err := s.authz.Create(ctx, policy); err != nil {
			if errors.Is(err, domain.ErrDuplicatePolicy) {
				return nil, status.Errorf(codes.AlreadyExists, "authorization policy already exists: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "create authorization policy: %v", err)
		}
	} else {
		// Policy exists — update.
		if err := s.authz.Update(ctx, policy); err != nil {
			return nil, status.Errorf(codes.Internal, "update authorization policy: %v", err)
		}
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: authzPolicyToProto(policy),
	}, nil
}

// GetResource retrieves a resource by kind/namespace/name.
func (s *ControlPlaneServer) GetResource(ctx context.Context, req *omnimeshv1.GetResourceRequest) (*omnimeshv1.GetResourceResponse, error) {
	switch req.Kind {
	case "Service":
		svc, err := s.services.Get(ctx, req.Namespace, req.Name)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return &omnimeshv1.GetResourceResponse{Resource: serviceToProto(svc)}, nil

	case "Namespace":
		ns, err := s.namespaces.Get(ctx, req.Name)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return &omnimeshv1.GetResourceResponse{Resource: namespaceToProto(ns)}, nil

	case "NetworkTarget":
		target, err := s.targets.Get(ctx, req.Name)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return &omnimeshv1.GetResourceResponse{Resource: networkTargetToProto(target)}, nil

	case "TrafficPolicy":
		policy, err := s.policies.GetTrafficPolicy(ctx, req.Namespace, req.Name)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return &omnimeshv1.GetResourceResponse{Resource: trafficPolicyResourceToProto(policy)}, nil

	case "AuthorizationPolicy":
		policy, err := s.authz.Get(ctx, req.Namespace, req.Name)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return &omnimeshv1.GetResourceResponse{Resource: authzPolicyToProto(policy)}, nil

	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported kind: %s", req.Kind)
	}
}

// ListResources lists resources by kind and optional namespace.
func (s *ControlPlaneServer) ListResources(ctx context.Context, req *omnimeshv1.ListResourcesRequest) (*omnimeshv1.ListResourcesResponse, error) {
	switch req.Kind {
	case "Service":
		services, err := s.services.List(ctx, req.Namespace)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var protos []*omnimeshv1.Resource
		for _, svc := range services {
			protos = append(protos, serviceToProto(svc))
		}
		return &omnimeshv1.ListResourcesResponse{Resources: protos}, nil

	case "Namespace":
		namespaces, err := s.namespaces.List(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var protos []*omnimeshv1.Resource
		for _, ns := range namespaces {
			protos = append(protos, namespaceToProto(ns))
		}
		return &omnimeshv1.ListResourcesResponse{Resources: protos}, nil

	case "NetworkTarget":
		targets, err := s.targets.List(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var protos []*omnimeshv1.Resource
		for _, t := range targets {
			protos = append(protos, networkTargetToProto(t))
		}
		return &omnimeshv1.ListResourcesResponse{Resources: protos}, nil

	case "TrafficPolicy":
		policies, err := s.policies.ListTrafficPolicies(ctx, req.Namespace)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var protos []*omnimeshv1.Resource
		for _, policy := range policies {
			protos = append(protos, trafficPolicyResourceToProto(policy))
		}
		return &omnimeshv1.ListResourcesResponse{Resources: protos}, nil

	case "AuthorizationPolicy":
		policies, err := s.authz.List(ctx, req.Namespace)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var protos []*omnimeshv1.Resource
		for _, policy := range policies {
			protos = append(protos, authzPolicyToProto(policy))
		}
		return &omnimeshv1.ListResourcesResponse{Resources: protos}, nil

	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported kind: %s", req.Kind)
	}
}

// DeleteResource deletes a resource by kind/namespace/name.
func (s *ControlPlaneServer) DeleteResource(ctx context.Context, req *omnimeshv1.DeleteResourceRequest) (*omnimeshv1.DeleteResourceResponse, error) {
	switch req.Kind {
	case "Service":
		if err := s.services.Delete(ctx, req.Namespace, req.Name); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &omnimeshv1.DeleteResourceResponse{}, nil

	case "Namespace":
		if err := s.namespaces.Delete(ctx, req.Name, req.Force); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &omnimeshv1.DeleteResourceResponse{}, nil

	case "NetworkTarget":
		if err := s.targets.Delete(ctx, req.Name); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &omnimeshv1.DeleteResourceResponse{}, nil

	case "TrafficPolicy":
		if err := s.policies.DeleteTrafficPolicy(ctx, req.Namespace, req.Name); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &omnimeshv1.DeleteResourceResponse{}, nil

	case "AuthorizationPolicy":
		if err := s.authz.Delete(ctx, req.Namespace, req.Name); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &omnimeshv1.DeleteResourceResponse{}, nil

	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported kind: %s", req.Kind)
	}
}

// --- Proto Converters ---

func serviceToProto(svc *model.Service) *omnimeshv1.Resource {
	spec := map[string]any{
		"protocol": svc.Protocol.String(),
		"port":     svc.Port,
	}
	if len(svc.Selectors) > 0 {
		spec["selectors"] = svc.Selectors
	}
	if svc.MeshAddr.IsValid() {
		spec["meshAddr"] = svc.MeshAddr.String()
	}
	specJSON, _ := json.Marshal(spec)

	return &omnimeshv1.Resource{
		ApiVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata: &omnimeshv1.ResourceMetadata{
			Name:      svc.ID.Name,
			Namespace: svc.ID.Namespace,
			Labels:    svc.Labels,
		},
		Spec: specJSON,
	}
}

func namespaceToProto(ns *model.Namespace) *omnimeshv1.Resource {
	spec := map[string]any{
		"isolation": ns.Isolation.String(),
	}
	if ns.CIDR.IsValid() {
		spec["cidr"] = ns.CIDR.String()
	}
	specJSON, _ := json.Marshal(spec)

	return &omnimeshv1.Resource{
		ApiVersion: "core.omnimesh.io/v1",
		Kind:       "Namespace",
		Metadata: &omnimeshv1.ResourceMetadata{
			Name:   ns.Name,
			Labels: ns.Labels,
		},
		Spec: specJSON,
	}
}

func networkTargetToProto(t *model.NetworkTarget) *omnimeshv1.Resource {
	spec := map[string]any{
		"gatewayAddress": t.GatewayAddress,
	}
	specJSON, _ := json.Marshal(spec)

	return &omnimeshv1.Resource{
		ApiVersion: "core.omnimesh.io/v1",
		Kind:       "NetworkTarget",
		Metadata: &omnimeshv1.ResourceMetadata{
			Name: t.Name,
		},
		Spec: specJSON,
	}
}

func trafficPolicyResourceToProto(policy *model.TrafficPolicy) *omnimeshv1.Resource {
	specJSON, _ := codec.EncodeTrafficPolicySpec(policy)

	return &omnimeshv1.Resource{
		ApiVersion: "traffic.omnimesh.io/v1",
		Kind:       "TrafficPolicy",
		Metadata: &omnimeshv1.ResourceMetadata{
			Name:      policy.Name,
			Namespace: policy.Namespace,
		},
		Spec: specJSON,
	}
}

func authzPolicyToProto(policy *model.AuthorizationPolicy) *omnimeshv1.Resource {
	spec := map[string]any{
		"action": policy.Action.String(),
		"source": map[string]any{
			"namespace": policy.Source.Namespace,
			"name":      policy.Source.Name,
		},
		"destination": map[string]any{
			"namespace": policy.Destination.Namespace,
			"name":      policy.Destination.Name,
		},
	}
	specJSON, _ := json.Marshal(spec)

	return &omnimeshv1.Resource{
		ApiVersion: "policy.omnimesh.io/v1",
		Kind:       "AuthorizationPolicy",
		Metadata: &omnimeshv1.ResourceMetadata{
			Name:      policy.Name,
			Namespace: policy.Namespace,
		},
		Spec: specJSON,
	}
}

// Suppress unused import warning.
var _ = fmt.Sprintf
