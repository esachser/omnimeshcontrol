package model

import (
	"reflect"
	"testing"
)

func TestService_EffectiveSelectorSets(t *testing.T) {
	tests := []struct {
		name string
		svc  Service
		want [][]string
	}{
		{
			name: "no selectors configured",
			svc:  Service{},
			want: nil,
		},
		{
			name: "legacy flat selectors treated as single set",
			svc: Service{
				Selectors: []string{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
			want: [][]string{
				{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
		},
		{
			name: "multiple selector sets for windows and linux",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
					{"unix:user:payment", "unix:pid:1234"},
				},
			},
			want: [][]string{
				{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
				{"unix:user:payment", "unix:pid:1234"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.svc.EffectiveSelectorSets()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EffectiveSelectorSets() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestService_ValidateSelectors(t *testing.T) {
	tests := []struct {
		name    string
		svc     Service
		wantErr bool
	}{
		{
			name:    "empty is valid",
			svc:     Service{},
			wantErr: false,
		},
		{
			name: "valid legacy selectors",
			svc: Service{
				Selectors: []string{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
			wantErr: false,
		},
		{
			name: "valid multiple selector sets",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
					{"unix:user:payment", "unix:pid:1234"},
				},
			},
			wantErr: false,
		},
		{
			name: "empty inner set",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1"},
					{},
				},
			},
			wantErr: true,
		},
		{
			name: "blank selector inside set",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "   "},
				},
			},
			wantErr: true,
		},
		{
			name: "blank legacy selector",
			svc: Service{
				Selectors: []string{"unix:user:app", ""},
			},
			wantErr: true,
		},
		{
			name: "both selectors and selectorSets set",
			svc: Service{
				Selectors:    []string{"unix:user:app"},
				SelectorSets: [][]string{{"windows:user_sid:S-1-5-21-1"}},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.svc.ValidateSelectors()
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateSelectors() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestServiceIdentity_EffectiveSelectorSets(t *testing.T) {
	id := ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/prod/sa/api",
		ServiceID: ServiceID{Namespace: "prod", Name: "api"},
		SelectorSets: [][]string{
			{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			{"unix:uid:1000", "unix:pid:42"},
		},
	}

	got := id.EffectiveSelectorSets()
	if len(got) != 2 {
		t.Fatalf("EffectiveSelectorSets() len = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got, id.SelectorSets) {
		t.Errorf("EffectiveSelectorSets() = %v, want %v", got, id.SelectorSets)
	}
}

func TestNormalizeServiceProtocol(t *testing.T) {
	tests := []struct {
		raw  string
		want ServiceProtocol
	}{
		{raw: "", want: ServiceProtocolAuto},
		{raw: "   ", want: ServiceProtocolAuto},
		{raw: "auto", want: ServiceProtocolAuto},
		{raw: "AUTO", want: ServiceProtocolAuto},
		{raw: "  auto ", want: ServiceProtocolAuto},
		{raw: "tcp", want: ServiceProtocolTCP},
		{raw: "TCP", want: ServiceProtocolTCP},
		{raw: " http ", want: ServiceProtocolHTTP},
		{raw: "HTTP", want: ServiceProtocolHTTP},
		{raw: "http1", want: ServiceProtocolHTTP1},
		{raw: "HTTP1", want: ServiceProtocolHTTP1},
		{raw: "http/1.0", want: ServiceProtocolHTTP1},
		{raw: "HTTP/1.0", want: ServiceProtocolHTTP1},
		{raw: "http/1.1", want: ServiceProtocolHTTP1},
		{raw: "HTTP/1.1", want: ServiceProtocolHTTP1},
		{raw: "http1.1", want: ServiceProtocolHTTP1},
		{raw: "http2", want: ServiceProtocolHTTP2},
		{raw: "HTTP2", want: ServiceProtocolHTTP2},
		{raw: "http/2", want: ServiceProtocolHTTP2},
		{raw: "HTTP/2", want: ServiceProtocolHTTP2},
		{raw: "h2c", want: ServiceProtocolHTTP2},
		{raw: "H2C", want: ServiceProtocolHTTP2},
		{raw: "grpc-web", want: ServiceProtocolHTTP2},
		{raw: "gRPC-Web", want: ServiceProtocolHTTP2},
		{raw: "grpc", want: ServiceProtocolGRPC},
		{raw: "gRPC", want: ServiceProtocolGRPC},
		{raw: "GRPC", want: ServiceProtocolGRPC},
		{raw: "unknown", want: ServiceProtocolAuto},
		{raw: "postgres", want: ServiceProtocolAuto},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := NormalizeServiceProtocol(tc.raw); got != tc.want {
				t.Errorf("NormalizeServiceProtocol(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestServiceProtocol_Methods(t *testing.T) {
	tests := []struct {
		name      string
		protocol  ServiceProtocol
		wantStr   string
		wantAgent string
		wantIsL7  bool
	}{
		{name: "auto", protocol: ServiceProtocolAuto, wantStr: "auto", wantAgent: "auto", wantIsL7: false},
		{name: "tcp", protocol: ServiceProtocolTCP, wantStr: "TCP", wantAgent: "tcp", wantIsL7: false},
		{name: "http", protocol: ServiceProtocolHTTP, wantStr: "HTTP", wantAgent: "http", wantIsL7: true},
		{name: "http1", protocol: ServiceProtocolHTTP1, wantStr: "http1", wantAgent: "http1", wantIsL7: true},
		{name: "http2", protocol: ServiceProtocolHTTP2, wantStr: "http2", wantAgent: "http2", wantIsL7: true},
		{name: "grpc", protocol: ServiceProtocolGRPC, wantStr: "gRPC", wantAgent: "http2", wantIsL7: true},
		{name: "unknown", protocol: ServiceProtocol(99), wantStr: "auto", wantAgent: "auto", wantIsL7: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.protocol.String(); got != tc.wantStr {
				t.Errorf("String() = %q, want %q", got, tc.wantStr)
			}
			if got := tc.protocol.AgentProtocol(); got != tc.wantAgent {
				t.Errorf("AgentProtocol() = %q, want %q", got, tc.wantAgent)
			}
			if got := tc.protocol.IsL7(); got != tc.wantIsL7 {
				t.Errorf("IsL7() = %v, want %v", got, tc.wantIsL7)
			}
		})
	}
}
