// Package etcddns provides the DNS adapter that writes /skydns/ keys to etcd
// for CoreDNS consumption.
package etcddns

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdDNSRecordManager implements the DNSRecordManager outbound port by writing
// SkyDNS-format keys to etcd. CoreDNS reads these keys directly via its etcd plugin.
type EtcdDNSRecordManager struct {
	client *clientv3.Client
	zone   string // e.g., "omnimesh.local"
}

// NewEtcdDNSRecordManager creates a new DNS adapter for the given zone.
func NewEtcdDNSRecordManager(client *clientv3.Client, zone string) *EtcdDNSRecordManager {
	return &EtcdDNSRecordManager{
		client: client,
		zone:   zone,
	}
}

// UpsertRecord creates or updates a DNS record.
func (m *EtcdDNSRecordManager) UpsertRecord(ctx context.Context, fqdn string, addr netip.Addr) error {
	key := m.fqdnToSkyDNSKey(fqdn)
	val := fmt.Sprintf(`{"host":"%s"}`, addr)
	if _, err := m.client.Put(ctx, key, val); err != nil {
		return fmt.Errorf("dns: upsert %q: %w", fqdn, err)
	}
	return nil
}

// DeleteRecord removes a DNS record.
func (m *EtcdDNSRecordManager) DeleteRecord(ctx context.Context, fqdn string) error {
	key := m.fqdnToSkyDNSKey(fqdn)
	if _, err := m.client.Delete(ctx, key); err != nil {
		return fmt.Errorf("dns: delete %q: %w", fqdn, err)
	}
	return nil
}

// fqdnToSkyDNSKey converts an FQDN to the SkyDNS etcd key format.
// Example: "myservice.production.omnimesh.local" → "/skydns/local/omnimesh/production/myservice"
func (m *EtcdDNSRecordManager) fqdnToSkyDNSKey(fqdn string) string {
	// Remove trailing dot if present.
	fqdn = strings.TrimSuffix(fqdn, ".")
	parts := strings.Split(fqdn, ".")

	// Reverse the parts for SkyDNS key format.
	reversed := make([]string, len(parts))
	for i, part := range parts {
		reversed[len(parts)-1-i] = part
	}

	return "/skydns/" + strings.Join(reversed, "/")
}

// ServiceFQDN generates the FQDN for a mesh service.
// Format: "{name}.{namespace}.{zone}"
func (m *EtcdDNSRecordManager) ServiceFQDN(namespace, name string) string {
	return name + "." + namespace + "." + m.zone
}

// Compile-time interface check.
var _ outbound.DNSRecordManager = (*EtcdDNSRecordManager)(nil)
