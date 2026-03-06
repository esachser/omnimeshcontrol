package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// IdentityManager manages SPIFFE workload identities. The default implementation
// integrates with a SPIRE Server to register and deregister workload entries.
type IdentityManager interface {
	// RegisterWorkload registers a SPIFFE identity for a mesh service.
	RegisterWorkload(ctx context.Context, identity model.ServiceIdentity) error
	// DeregisterWorkload removes the SPIFFE identity for a mesh service.
	DeregisterWorkload(ctx context.Context, serviceID model.ServiceID) error
}
