package codec

import (
	"reflect"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestAuthzPolicySpec_FullRoundTrip(t *testing.T) {
	validFrom := time.Date(2026, 3, 4, 16, 0, 0, 0, time.UTC)
	validUntil := time.Date(2026, 3, 4, 18, 0, 0, 0, time.UTC)

	original := &model.AuthorizationPolicy{
		Name:      "order-to-payment-rich",
		Namespace: "production",
		Action:    model.PolicyActionDryRun,
		Source: model.PolicySelector{
			Namespace:   "production",
			Name:        "order-service",
			Labels:      map[string]string{"version": "v2", "tier": "backend"},
			SpiffeID:    "spiffe://omnimesh.local/ns/production/sa/order-service",
			TrustDomain: "omnimesh.local",
		},
		Destination: model.PolicySelector{
			Namespace:   "production",
			Name:        "payment-service",
			Labels:      map[string]string{"pci": "true"},
			SpiffeID:    "spiffe://omnimesh.local/ns/production/sa/payment-service",
			TrustDomain: "omnimesh.local",
		},
		DestinationPorts: []int{8080, 50051},
		Rules: []model.AuthzRule{
			{
				HTTP: &model.HTTPAuthzRule{
					Methods:      []string{"POST", "GET"},
					PathPrefixes: []string{"/api/v1/charge", "/api/v1/refund"},
					PathExact:    []string{"/healthz"},
					Headers:      map[string]string{"x-api-version": "v1"},
				},
			},
			{
				GRPC: &model.GRPCAuthzRule{
					Services: []string{"payment.v1.PaymentService"},
					Methods:  []string{"Charge", "Refund"},
				},
			},
		},
		ValidFrom:  &validFrom,
		ValidUntil: &validUntil,
	}

	res, err := AuthzPolicyToResource(original)
	if err != nil {
		t.Fatalf("AuthzPolicyToResource() error = %v", err)
	}
	if res.APIVersion != "policy.omnimesh.io/v1" || res.Kind != "AuthorizationPolicy" {
		t.Fatalf("unexpected resource envelope: %+v", res)
	}

	decoded, err := ResourceToAuthzPolicy(res)
	if err != nil {
		t.Fatalf("ResourceToAuthzPolicy() error = %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip mismatch\noriginal: %+v\ndecoded:  %+v", original, decoded)
	}
}

func TestAuthzPolicySpec_SnakeCaseAliasesAndMinimal(t *testing.T) {
	t.Run("snake_case aliases", func(t *testing.T) {
		raw := []byte(`{
			"action": "DRY_RUN",
			"source": {
				"namespace": "prod",
				"name": "orders",
				"spiffe_id": "spiffe://omnimesh.local/ns/prod/sa/orders",
				"trust_domain": "omnimesh.local"
			},
			"destination": {
				"namespace": "prod",
				"name": "payments"
			},
			"destination_ports": [8080],
			"rules": [{
				"http": {
					"methods": ["POST"],
					"path_prefixes": ["/api/v1"],
					"path_exact": ["/healthz"]
				}
			}],
			"valid_from": "2026-03-04T16:00:00Z",
			"valid_until": "2026-03-04T18:00:00Z"
		}`)

		p, err := DecodeAuthzPolicySpec(raw, "p1", "prod")
		if err != nil {
			t.Fatalf("DecodeAuthzPolicySpec() error = %v", err)
		}
		if p.Action != model.PolicyActionDryRun {
			t.Errorf("Action = %v, want DRY_RUN", p.Action)
		}
		if p.Source.SpiffeID != "spiffe://omnimesh.local/ns/prod/sa/orders" {
			t.Errorf("Source.SpiffeID = %q", p.Source.SpiffeID)
		}
		if p.Source.TrustDomain != "omnimesh.local" {
			t.Errorf("Source.TrustDomain = %q", p.Source.TrustDomain)
		}
		if len(p.DestinationPorts) != 1 || p.DestinationPorts[0] != 8080 {
			t.Errorf("DestinationPorts = %v, want [8080]", p.DestinationPorts)
		}
		if len(p.Rules) != 1 || p.Rules[0].HTTP == nil || len(p.Rules[0].HTTP.PathPrefixes) != 1 || len(p.Rules[0].HTTP.PathExact) != 1 {
			t.Errorf("Rules = %+v, want 1 HTTP rule with path_prefixes and path_exact", p.Rules)
		}
		if p.ValidFrom == nil || p.ValidUntil == nil {
			t.Fatalf("expected ValidFrom and ValidUntil to be parsed")
		}
	})

	t.Run("minimal L4 policy", func(t *testing.T) {
		raw := []byte(`{"action":"DENY","source":{"namespace":"staging","name":"*"},"destination":{"namespace":"production","name":"redis"}}`)
		p, err := DecodeAuthzPolicySpec(raw, "deny-redis", "production")
		if err != nil {
			t.Fatalf("DecodeAuthzPolicySpec() error = %v", err)
		}
		if p.Action != model.PolicyActionDeny || p.Source.Name != "*" || p.Destination.Name != "redis" {
			t.Errorf("unexpected decoded policy: %+v", p)
		}
	})

	t.Run("invalid action and timestamp errors", func(t *testing.T) {
		if _, err := DecodeAuthzPolicySpec([]byte(`{"action":"INVALID"}`), "p", "ns"); err == nil {
			t.Error("expected error for invalid action")
		}
		if _, err := DecodeAuthzPolicySpec([]byte(`{"validFrom":"not-a-time"}`), "p", "ns"); err == nil {
			t.Error("expected error for invalid validFrom")
		}
		if _, err := DecodeAuthzPolicySpec([]byte(`{"validFrom":"2026-03-04T16:00:00Z","valid_from":"2026-03-04T17:00:00Z"}`), "p", "ns"); err == nil {
			t.Error("expected error for conflicting camelCase and snake_case validFrom")
		}
	})
}
