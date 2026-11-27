package grpcadapter

import (
	"context"
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/authz"
	"github.com/esachser/omnimeshcontrol/internal/application/namespace"
	"github.com/esachser/omnimeshcontrol/internal/application/service"
	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestControlPlaneServer(t *testing.T) (*ControlPlaneServer, *apptest.MockIdentityManager) {
	t.Helper()

	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	dns := apptest.NewMockDNSManager()
	identity := apptest.NewMockIdentityManager()
	depStore := apptest.NewMockDependencyStore()
	cidr := netip.MustParsePrefix("100.64.0.0/10")

	nsApp := namespace.NewNamespaceAppService(resources, cidr, nil)
	if err := nsApp.Apply(context.Background(), &model.Namespace{Name: "production"}); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	svcApp := service.NewServiceAppService(service.ServiceAppServiceConfig{
		Resources:  resources,
		State:      state,
		DNS:        dns,
		Identity:   identity,
		Namespaces: nsApp,
		DNSZone:    "omnimesh.local",
	})
	authzApp := authz.NewAuthzAppService(resources, depStore, domainservice.NewAuthzEvaluator(), nil)

	return NewControlPlaneServer(svcApp, nsApp, nil, nil, authzApp, nil), identity
}

func TestControlPlaneServer_ApplyAndGetService_WithSelectorSets(t *testing.T) {
	srv, identity := newTestControlPlaneServer(t)
	ctx := context.Background()

	wantSets := [][]string{
		{"windows:user_sid:S-1-5-21-1001", "windows:sha256:abcdef123456"},
		{"unix:user:payment", "unix:pid:4321"},
	}

	specBytes, err := json.Marshal(map[string]any{
		"protocol":     "gRPC",
		"port":         50051,
		"selectorSets": wantSets,
	})
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}

	applyResp, err := srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{
			ApiVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata: &omnimeshv1.ResourceMetadata{
				Name:      "payment-service",
				Namespace: "production",
			},
			Spec: specBytes,
		},
	})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}

	registered, ok := identity.Identities["production/payment-service"]
	if !ok {
		t.Fatal("expected identity to be registered")
	}
	if !reflect.DeepEqual(registered.SelectorSets, wantSets) {
		t.Errorf("registered SelectorSets = %v, want %v", registered.SelectorSets, wantSets)
	}

	var respSpec struct {
		SelectorSets [][]string `json:"selectorSets"`
	}
	if err := json.Unmarshal(applyResp.Resource.Spec, &respSpec); err != nil {
		t.Fatalf("unmarshal apply response spec: %v", err)
	}
	if !reflect.DeepEqual(respSpec.SelectorSets, wantSets) {
		t.Errorf("apply response selectorSets = %v, want %v", respSpec.SelectorSets, wantSets)
	}

	getResp, err := srv.GetResource(ctx, &omnimeshv1.GetResourceRequest{
		Kind:      "Service",
		Namespace: "production",
		Name:      "payment-service",
	})
	if err != nil {
		t.Fatalf("GetResource() error = %v", err)
	}

	var getSpec struct {
		SelectorSets [][]string `json:"selectorSets"`
	}
	if err := json.Unmarshal(getResp.Resource.Spec, &getSpec); err != nil {
		t.Fatalf("unmarshal get response spec: %v", err)
	}
	if !reflect.DeepEqual(getSpec.SelectorSets, wantSets) {
		t.Errorf("get response selectorSets = %v, want %v", getSpec.SelectorSets, wantSets)
	}
}

func TestControlPlaneServer_ApplyService_InvalidSelectorSets(t *testing.T) {
	srv, _ := newTestControlPlaneServer(t)
	ctx := context.Background()

	specBytes, _ := json.Marshal(map[string]any{
		"protocol":     "HTTP",
		"port":         8080,
		"selectorSets": [][]string{{"windows:user_sid:S-1"}, {}},
	})

	_, err := srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{
			ApiVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata: &omnimeshv1.ResourceMetadata{
				Name:      "invalid-svc",
				Namespace: "production",
			},
			Spec: specBytes,
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ApplyResource() status code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestControlPlaneServer_ApplyAndGetService_ProtocolVariants(t *testing.T) {
	tests := []struct {
		name         string
		rawProtocol  string
		wantProtocol string
	}{
		{name: "empty defaults to auto", rawProtocol: "", wantProtocol: "auto"},
		{name: "auto", rawProtocol: "auto", wantProtocol: "auto"},
		{name: "tcp", rawProtocol: "tcp", wantProtocol: "TCP"},
		{name: "http", rawProtocol: "http", wantProtocol: "HTTP"},
		{name: "http/1.1", rawProtocol: "http/1.1", wantProtocol: "http1"},
		{name: "h2c", rawProtocol: "h2c", wantProtocol: "http2"},
		{name: "grpc-web", rawProtocol: "grpc-web", wantProtocol: "http2"},
		{name: "gRPC", rawProtocol: "gRPC", wantProtocol: "gRPC"},
		{name: "unknown defaults to auto", rawProtocol: "redis", wantProtocol: "auto"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newTestControlPlaneServer(t)
			ctx := context.Background()

			specBytes, err := json.Marshal(map[string]any{
				"protocol": tc.rawProtocol,
				"port":     8080,
			})
			if err != nil {
				t.Fatalf("marshal spec: %v", err)
			}

			applyResp, err := srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
				Resource: &omnimeshv1.Resource{
					ApiVersion: "core.omnimesh.io/v1",
					Kind:       "Service",
					Metadata: &omnimeshv1.ResourceMetadata{
						Name:      "test-svc",
						Namespace: "production",
					},
					Spec: specBytes,
				},
			})
			if err != nil {
				t.Fatalf("ApplyResource() error = %v", err)
			}

			var respSpec struct {
				Protocol string `json:"protocol"`
			}
			if err := json.Unmarshal(applyResp.Resource.Spec, &respSpec); err != nil {
				t.Fatalf("unmarshal apply spec: %v", err)
			}
			if respSpec.Protocol != tc.wantProtocol {
				t.Errorf("apply response protocol = %q, want %q", respSpec.Protocol, tc.wantProtocol)
			}

			getResp, err := srv.GetResource(ctx, &omnimeshv1.GetResourceRequest{
				Kind:      "Service",
				Namespace: "production",
				Name:      "test-svc",
			})
			if err != nil {
				t.Fatalf("GetResource() error = %v", err)
			}
			if err := json.Unmarshal(getResp.Resource.Spec, &respSpec); err != nil {
				t.Fatalf("unmarshal get spec: %v", err)
			}
			if respSpec.Protocol != tc.wantProtocol {
				t.Errorf("get response protocol = %q, want %q", respSpec.Protocol, tc.wantProtocol)
			}
		})
	}
}

func TestControlPlaneServer_ApplyGetListAuthorizationPolicy_FullSpec(t *testing.T) {
	srv, _ := newTestControlPlaneServer(t)
	ctx := context.Background()

	fullSpec, err := json.Marshal(map[string]any{
		"action": "DRY_RUN",
		"source": map[string]any{
			"namespace":   "production",
			"name":        "order-service",
			"labels":      map[string]string{"tier": "app"},
			"spiffeID":    "spiffe://omnimesh.local/ns/production/sa/order-service",
			"trustDomain": "omnimesh.local",
		},
		"destination": map[string]any{
			"namespace": "production",
			"name":      "payment-service",
			"labels":    map[string]string{"pci": "true"},
		},
		"destinationPorts": []int{8080, 50051},
		"rules": []map[string]any{
			{
				"http": map[string]any{
					"methods":      []string{"POST"},
					"pathPrefixes": []string{"/api/v1/charge"},
					"pathExact":    []string{"/healthz"},
					"headers":      map[string]string{"x-api-version": "v1"},
				},
			},
			{
				"grpc": map[string]any{
					"services": []string{"payment.v1.PaymentService"},
					"methods":  []string{"Charge"},
				},
			},
		},
		"validFrom":  "2026-03-04T16:00:00Z",
		"validUntil": "2026-03-04T18:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal fullSpec: %v", err)
	}

	applyResp, err := srv.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{
			ApiVersion: "policy.omnimesh.io/v1",
			Kind:       "AuthorizationPolicy",
			Metadata:   &omnimeshv1.ResourceMetadata{Name: "order-to-payment", Namespace: "production"},
			Spec:       fullSpec,
		},
	})
	if err != nil {
		t.Fatalf("ApplyResource(AuthorizationPolicy) error = %v", err)
	}

	gotApply, err := codec.DecodeAuthzPolicySpec(applyResp.Resource.Spec, "order-to-payment", "production")
	if err != nil {
		t.Fatalf("decode apply response spec: %v", err)
	}
	if gotApply.Action != model.PolicyActionDryRun ||
		gotApply.Source.SpiffeID != "spiffe://omnimesh.local/ns/production/sa/order-service" ||
		gotApply.Source.Labels["tier"] != "app" ||
		gotApply.Destination.Labels["pci"] != "true" ||
		len(gotApply.DestinationPorts) != 2 ||
		len(gotApply.Rules) != 2 ||
		gotApply.ValidFrom == nil || gotApply.ValidUntil == nil {
		t.Fatalf("unexpected decoded apply policy: %+v", gotApply)
	}

	getResp, err := srv.GetResource(ctx, &omnimeshv1.GetResourceRequest{
		Kind:      "AuthorizationPolicy",
		Namespace: "production",
		Name:      "order-to-payment",
	})
	if err != nil {
		t.Fatalf("GetResource() error = %v", err)
	}
	gotGet, err := codec.DecodeAuthzPolicySpec(getResp.Resource.Spec, "order-to-payment", "production")
	if err != nil {
		t.Fatalf("decode get response spec: %v", err)
	}
	if !reflect.DeepEqual(gotApply, gotGet) {
		t.Fatalf("get response mismatch\napply: %+v\nget:   %+v", gotApply, gotGet)
	}

	listResp, err := srv.ListResources(ctx, &omnimeshv1.ListResourcesRequest{
		Kind:      "AuthorizationPolicy",
		Namespace: "production",
	})
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(listResp.Resources) != 1 {
		t.Fatalf("ListResources() len = %d, want 1", len(listResp.Resources))
	}
}
