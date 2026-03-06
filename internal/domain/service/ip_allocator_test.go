package service

import (
	"net/netip"
	"testing"
)

func TestNewIPAllocator(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{
			name:    "valid CIDR",
			cidr:    "100.64.0.0/10",
			wantErr: false,
		},
		{
			name:    "small CIDR",
			cidr:    "10.0.0.0/24",
			wantErr: false,
		},
		{
			name:    "invalid CIDR",
			cidr:    "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, err := netip.ParsePrefix(tt.cidr)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("failed to parse test CIDR %q: %v", tt.cidr, err)
				}
				// Test with an invalid prefix.
				alloc, allocErr := NewIPAllocator(netip.Prefix{})
				if allocErr == nil {
					t.Errorf("NewIPAllocator() with invalid prefix: expected error, got %v", alloc)
				}
				return
			}

			alloc, allocErr := NewIPAllocator(prefix)
			if (allocErr != nil) != tt.wantErr {
				t.Errorf("NewIPAllocator() error = %v, wantErr %v", allocErr, tt.wantErr)
				return
			}
			if !tt.wantErr && alloc == nil {
				t.Error("NewIPAllocator() returned nil allocator")
			}
		})
	}
}

func TestIPAllocator_ValidateAddr(t *testing.T) {
	cidr := netip.MustParsePrefix("100.64.0.0/10")
	alloc, err := NewIPAllocator(cidr)
	if err != nil {
		t.Fatalf("NewIPAllocator() error = %v", err)
	}

	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{
			name:    "address in CIDR",
			addr:    "100.64.0.1",
			wantErr: false,
		},
		{
			name:    "address at end of CIDR",
			addr:    "100.127.255.254",
			wantErr: false,
		},
		{
			name:    "address outside CIDR",
			addr:    "192.168.1.1",
			wantErr: true,
		},
		{
			name:    "address just outside CIDR",
			addr:    "100.128.0.1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			err := alloc.ValidateAddr(addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAddr(%s) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}
