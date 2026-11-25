package grpcadapter

import (
	"context"
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/namespace"
	"github.com/esachser/omnimeshcontrol/internal/application/service"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestControlPlaneServer(t *testing.T) (*ControlPlaneServer, *apptest.MockIdentityManager) {
	t.Helper()

	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	dns := apptest.NewMockDNSManager()
	identity := apptest.NewMockIdentityManager()
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

	return NewControlPlaneServer(svcApp, nsApp, nil, nil, nil, nil), identity
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
