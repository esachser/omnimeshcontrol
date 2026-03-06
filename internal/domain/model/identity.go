package model

// ServiceIdentity represents the SPIFFE identity of a service in the mesh.
// Each service receives a SPIFFE Verifiable Identity Document (SVID) that
// enables mTLS authentication.
type ServiceIdentity struct {
	// SPIFFEID is the SPIFFE identity URI (e.g., "spiffe://omnimesh.local/ns/production/sa/order-service").
	SPIFFEID string
	// ServiceID is the mesh service this identity belongs to.
	ServiceID ServiceID
}
