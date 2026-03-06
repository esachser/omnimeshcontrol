package etcddns

import (
	"testing"
)

func TestFqdnToSkyDNSKey(t *testing.T) {
	m := NewEtcdDNSRecordManager(nil, "omnimesh.local")

	tests := []struct {
		fqdn string
		want string
	}{
		{
			fqdn: "myservice.production.omnimesh.local",
			want: "/skydns/local/omnimesh/production/myservice",
		},
		{
			fqdn: "myservice.production.omnimesh.local.",
			want: "/skydns/local/omnimesh/production/myservice",
		},
		{
			fqdn: "api.default.omnimesh.local",
			want: "/skydns/local/omnimesh/default/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.fqdn, func(t *testing.T) {
			got := m.fqdnToSkyDNSKey(tt.fqdn)
			if got != tt.want {
				t.Errorf("fqdnToSkyDNSKey(%q) = %q, want %q", tt.fqdn, got, tt.want)
			}
		})
	}
}

func TestServiceFQDN(t *testing.T) {
	m := NewEtcdDNSRecordManager(nil, "omnimesh.local")

	got := m.ServiceFQDN("production", "order-service")
	want := "order-service.production.omnimesh.local"
	if got != want {
		t.Errorf("ServiceFQDN() = %q, want %q", got, want)
	}
}
