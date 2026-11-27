// Package grpcadapter implements the gRPC inbound adapter that drives
// the application services via the generated protobuf service interfaces.
package grpcadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/application/telemetry"
	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
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
	metrics    outbound.MetricsRecorder
	tracer     outbound.TraceExporter
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
		metrics:    telemetry.NoopMetricsRecorder{},
		tracer:     telemetry.NoopTraceExporter{},
	}
}

// SetTelemetry injects optional metrics and tracing ports into the server.
func (s *ControlPlaneServer) SetTelemetry(metrics outbound.MetricsRecorder, tracer outbound.TraceExporter) {
	s.metrics = telemetry.EnsureMetrics(metrics)
	s.tracer = telemetry.EnsureTracer(tracer)
}

// ApplyResource handles apply requests for all resource kinds.
func (s *ControlPlaneServer) ApplyResource(ctx context.Context, req *omnimeshv1.ApplyResourceRequest) (*omnimeshv1.ApplyResourceResponse, error) {
	start := time.Now()
	if req == nil || req.Resource == nil {
		s.metrics.IncrCounter(ctx, model.MetricCPResourcesValidationErrors, map[string]string{
			"kind":       "unknown",
			"error_type": "structural",
		})
		s.metrics.IncrCounter(ctx, model.MetricCPResourcesApplyTotal, map[string]string{
			"kind":      "unknown",
			"namespace": "",
			"result":    "error",
		})
		return nil, status.Error(codes.InvalidArgument, "resource is required")
	}

	r := req.Resource
	ns := ""
	if r.Metadata != nil {
		ns = r.Metadata.Namespace
	}

	ctx, span := s.tracer.StartSpan(ctx, "ControlPlaneService.ApplyResource", map[string]string{
		"omnimesh.resource.kind":      r.Kind,
		"omnimesh.resource.namespace": ns,
	})
	defer span.End()

	var (
		resp *omnimeshv1.ApplyResourceResponse
		err  error
	)
	switch r.Kind {
	case "Service":
		resp, err = s.applyService(ctx, r)
	case "Namespace":
		resp, err = s.applyNamespace(ctx, r)
	case "NetworkTarget":
		resp, err = s.applyNetworkTarget(ctx, r)
	case "TrafficPolicy":
		resp, err = s.applyTrafficPolicy(ctx, r)
	case "AuthorizationPolicy":
		resp, err = s.applyAuthorizationPolicy(ctx, r)
	default:
		s.metrics.IncrCounter(ctx, model.MetricCPResourcesValidationErrors, map[string]string{
			"kind":       r.Kind,
			"error_type": "structural",
		})
		err = status.Errorf(codes.InvalidArgument, "unsupported resource kind: %s", r.Kind)
	}

	durationMs := float64(time.Since(start).Microseconds()) / 1000.0
	s.metrics.RecordHistogram(ctx, model.MetricCPResourcesApplyDurationMs, durationMs, map[string]string{
		"kind": r.Kind,
	})

	result := "success"
	if err != nil {
		result = "error"
		span.SetError(err)
	}
	s.metrics.IncrCounter(ctx, model.MetricCPResourcesApplyTotal, map[string]string{
		"kind":      r.Kind,
		"namespace": ns,
		"result":    result,
	})
	return resp, err
}

func (s *ControlPlaneServer) recordValidationError(ctx context.Context, kind, errorType string) {
	s.metrics.IncrCounter(ctx, model.MetricCPResourcesValidationErrors, map[string]string{
		"kind":       kind,
		"error_type": errorType,
	})
}

func (s *ControlPlaneServer) applyService(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		s.recordValidationError(ctx, r.Kind, "structural")
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

	svc, err := mapper.ResourceToService(resource)
	if err != nil {
		s.recordValidationError(ctx, r.Kind, "structural")
		return nil, status.Errorf(codes.InvalidArgument, "invalid spec: %v", err)
	}

	if err := s.services.Apply(ctx, svc); err != nil {
		if errors.Is(err, domain.ErrInvalidSelectors) || errors.Is(err, domain.ErrInvalidName) || errors.Is(err, domain.ErrInvalidNamespace) {
			s.recordValidationError(ctx, r.Kind, "semantic")
			return nil, status.Errorf(codes.InvalidArgument, "apply service: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "apply service: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: serviceToProto(svc),
	}, nil
}

func (s *ControlPlaneServer) applyNamespace(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		s.recordValidationError(ctx, r.Kind, "structural")
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
		s.recordValidationError(ctx, r.Kind, "structural")
		return nil, status.Errorf(codes.InvalidArgument, "invalid namespace: %v", err)
	}

	if err := s.namespaces.Apply(ctx, ns); err != nil {
		if errors.Is(err, domain.ErrInvalidName) || errors.Is(err, domain.ErrInvalidCIDR) {
			s.recordValidationError(ctx, r.Kind, "semantic")
		}
		return nil, status.Errorf(codes.Internal, "apply namespace: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: namespaceToProto(ns),
	}, nil
}

func (s *ControlPlaneServer) applyNetworkTarget(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		s.recordValidationError(ctx, r.Kind, "structural")
		return nil, status.Error(codes.InvalidArgument, "metadata is required")
	}

	var spec struct {
		GatewayAddress string `json:"gatewayAddress"`
	}
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			s.recordValidationError(ctx, r.Kind, "structural")
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
		s.recordValidationError(ctx, r.Kind, "structural")
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
		s.recordValidationError(ctx, r.Kind, "structural")
		return nil, status.Errorf(codes.InvalidArgument, "invalid traffic policy: %v", err)
	}

	_, getErr := s.policies.GetTrafficPolicy(ctx, policy.Namespace, policy.Name)
	if getErr != nil {
		if !errors.Is(getErr, domain.ErrPolicyNotFound) {
			return nil, status.Errorf(codes.Internal, "get traffic policy: %v", getErr)
		}
		if err := s.policies.CreateTrafficPolicy(ctx, policy); err != nil {
			s.recordValidationError(ctx, r.Kind, "semantic")
			return nil, status.Errorf(codes.Internal, "create traffic policy: %v", err)
		}
	} else {
		if err := s.policies.UpdateTrafficPolicy(ctx, policy); err != nil {
			s.recordValidationError(ctx, r.Kind, "semantic")
			return nil, status.Errorf(codes.Internal, "update traffic policy: %v", err)
		}
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: trafficPolicyResourceToProto(policy),
	}, nil
}

func (s *ControlPlaneServer) applyAuthorizationPolicy(ctx context.Context, r *omnimeshv1.Resource) (*omnimeshv1.ApplyResourceResponse, error) {
	if r.Metadata == nil {
		s.recordValidationError(ctx, r.Kind, "structural")
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

	policy, err := codec.ResourceToAuthzPolicy(resource)
	if err != nil {
		s.recordValidationError(ctx, r.Kind, "structural")
		return nil, status.Errorf(codes.InvalidArgument, "invalid authorization policy: %v", err)
	}

	// Try get first — if exists, update (idempotent apply).
	_, getErr := s.authz.Get(ctx, policy.Namespace, policy.Name)
	if getErr != nil {
		if err := s.authz.Create(ctx, policy); err != nil {
			if errors.Is(err, domain.ErrDuplicatePolicy) {
				s.recordValidationError(ctx, r.Kind, "semantic")
				return nil, status.Errorf(codes.AlreadyExists, "authorization policy already exists: %v", err)
			}
			if errors.Is(err, domain.ErrInvalidName) {
				s.recordValidationError(ctx, r.Kind, "semantic")
				return nil, status.Errorf(codes.InvalidArgument, "create authorization policy: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "create authorization policy: %v", err)
		}
	} else {
		if err := s.authz.Update(ctx, policy); err != nil {
			if errors.Is(err, domain.ErrInvalidName) {
				s.recordValidationError(ctx, r.Kind, "semantic")
				return nil, status.Errorf(codes.InvalidArgument, "update authorization policy: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "update authorization policy: %v", err)
		}
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: authzPolicyToProto(policy),
	}, nil
}

// GetResource retrieves a resource by kind/namespace/name.
func (s *ControlPlaneServer) GetResource(ctx context.Context, req *omnimeshv1.GetResourceRequest) (*omnimeshv1.GetResourceResponse, error) {
	ctx, span := s.tracer.StartSpan(ctx, "ControlPlaneService.GetResource", map[string]string{
		"omnimesh.resource.kind":      req.GetKind(),
		"omnimesh.resource.namespace": req.GetNamespace(),
		"omnimesh.resource.name":      req.GetName(),
	})
	defer span.End()

	resp, err := s.getResource(ctx, req)
	if err != nil {
		span.SetError(err)
	}
	return resp, err
}

func (s *ControlPlaneServer) getResource(ctx context.Context, req *omnimeshv1.GetResourceRequest) (*omnimeshv1.GetResourceResponse, error) {
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
	ctx, span := s.tracer.StartSpan(ctx, "ControlPlaneService.ListResources", map[string]string{
		"omnimesh.resource.kind":      req.GetKind(),
		"omnimesh.resource.namespace": req.GetNamespace(),
	})
	defer span.End()

	resp, err := s.listResources(ctx, req)
	if err != nil {
		span.SetError(err)
	}
	return resp, err
}

func (s *ControlPlaneServer) listResources(ctx context.Context, req *omnimeshv1.ListResourcesRequest) (*omnimeshv1.ListResourcesResponse, error) {
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
	ctx, span := s.tracer.StartSpan(ctx, "ControlPlaneService.DeleteResource", map[string]string{
		"omnimesh.resource.kind":      req.GetKind(),
		"omnimesh.resource.namespace": req.GetNamespace(),
		"omnimesh.resource.name":      req.GetName(),
	})
	defer span.End()

	resp, err := s.deleteResource(ctx, req)
	result := "success"
	if err != nil {
		result = "error"
		span.SetError(err)
	}
	s.metrics.IncrCounter(ctx, model.MetricCPResourcesDeleteTotal, map[string]string{
		"kind":      req.GetKind(),
		"namespace": req.GetNamespace(),
		"result":    result,
	})
	return resp, err
}

func (s *ControlPlaneServer) deleteResource(ctx context.Context, req *omnimeshv1.DeleteResourceRequest) (*omnimeshv1.DeleteResourceResponse, error) {
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
	if len(svc.SelectorSets) > 0 {
		spec["selectorSets"] = svc.SelectorSets
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
	specJSON, _ := codec.EncodeAuthzPolicySpec(policy)

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
