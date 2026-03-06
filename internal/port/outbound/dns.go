package outbound

import (
	"context"
	"net/netip"
)

// DNSRecordManager manages DNS records for mesh services. The default implementation
// writes /skydns/ keys for CoreDNS consumption.
type DNSRecordManager interface {
	// UpsertRecord creates or updates a DNS record mapping the given FQDN to an IP address.
	UpsertRecord(ctx context.Context, fqdn string, addr netip.Addr) error
	// DeleteRecord removes the DNS record for the given FQDN.
	DeleteRecord(ctx context.Context, fqdn string) error
}
