// Package grpcadapter implements the gRPC inbound adapter that drives
// the application services via the generated protobuf service interfaces.
package grpcadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
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
	logger     *slog.Logger
}

// NewControlPlaneServer creates a new ControlPlaneServer.
func NewControlPlaneServer(
	services inbound.ServiceManager,
	namespaces inbound.NamespaceManager,
	logger *slog.Logger,
) *ControlPlaneServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &ControlPlaneServer{
		services:   services,
		namespaces: namespaces,
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
		Protocol string `json:"protocol"`
		Port     int    `json:"port"`
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
		Labels: r.Metadata.Labels,
		Port:   spec.Port,
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

	ns := &model.Namespace{
		Name:   r.Metadata.Name,
		Labels: r.Metadata.Labels,
	}

	if err := s.namespaces.Apply(ctx, ns); err != nil {
		return nil, status.Errorf(codes.Internal, "apply namespace: %v", err)
	}

	return &omnimeshv1.ApplyResourceResponse{
		Resource: namespaceToProto(ns),
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

// Suppress unused import warning.
var _ = fmt.Sprintf
