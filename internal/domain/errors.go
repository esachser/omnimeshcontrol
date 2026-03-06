// Package domain contains core business logic, domain services, and error definitions.
// This package has ZERO external dependencies — only Go standard library types.
package domain

import "errors"

// Sentinel errors for the domain layer. These are used by application and adapter
// layers to determine error handling behavior via errors.Is / errors.As.

// Service errors.
var (
	// ErrServiceNotFound indicates the requested service does not exist.
	ErrServiceNotFound = errors.New("service not found")
	// ErrServiceAlreadyExists indicates a service with the same ID already exists.
	ErrServiceAlreadyExists = errors.New("service already exists")
)

// Namespace errors.
var (
	// ErrNamespaceNotFound indicates the requested namespace does not exist.
	ErrNamespaceNotFound = errors.New("namespace not found")
	// ErrNamespaceAlreadyExists indicates a namespace with the same name already exists.
	ErrNamespaceAlreadyExists = errors.New("namespace already exists")
	// ErrNamespaceNotEmpty indicates the namespace still contains resources and cannot be deleted.
	ErrNamespaceNotEmpty = errors.New("namespace is not empty")
)

// Node errors.
var (
	// ErrNodeNotFound indicates the requested node does not exist.
	ErrNodeNotFound = errors.New("node not found")
	// ErrNodeAlreadyRegistered indicates a node with the same ID is already registered.
	ErrNodeAlreadyRegistered = errors.New("node already registered")
)

// IP allocation errors.
var (
	// ErrIPExhausted indicates the mesh CIDR has no available IP addresses.
	ErrIPExhausted = errors.New("mesh IP address space exhausted")
	// ErrIPConflict indicates the requested IP is already allocated to another service.
	ErrIPConflict = errors.New("mesh IP address conflict")
)

// Traffic policy errors.
var (
	// ErrPolicyNotFound indicates the requested traffic policy does not exist.
	ErrPolicyNotFound = errors.New("traffic policy not found")
	// ErrPolicyConflict indicates a policy already exists for the target service.
	ErrPolicyConflict = errors.New("traffic policy conflict: a policy already exists for this service")
	// ErrInvalidRetryConfig indicates an invalid retry configuration.
	ErrInvalidRetryConfig = errors.New("invalid retry configuration")
	// ErrInvalidCBConfig indicates an invalid circuit breaker configuration.
	ErrInvalidCBConfig = errors.New("invalid circuit breaker configuration")
	// ErrInvalidRateLimitConfig indicates an invalid rate limit configuration.
	ErrInvalidRateLimitConfig = errors.New("invalid rate limit configuration")
	// ErrProtocolMismatch indicates a policy feature is incompatible with the service protocol.
	ErrProtocolMismatch = errors.New("policy feature incompatible with service protocol")
)

// Authorization policy errors.
var (
	// ErrAuthzPolicyNotFound indicates the requested authorization policy does not exist.
	ErrAuthzPolicyNotFound = errors.New("authorization policy not found")
	// ErrDuplicatePolicy indicates an authorization policy with the same name already exists.
	ErrDuplicatePolicy = errors.New("authorization policy already exists")
)

// Resource errors.
var (
	// ErrResourceNotFound indicates the requested resource does not exist.
	ErrResourceNotFound = errors.New("resource not found")
	// ErrRevisionConflict indicates an optimistic concurrency conflict.
	ErrRevisionConflict = errors.New("resource revision conflict")
)

// Validation errors.
var (
	// ErrInvalidName indicates a resource name is invalid (empty, too long, or contains invalid characters).
	ErrInvalidName = errors.New("invalid resource name")
	// ErrInvalidNamespace indicates a namespace reference is invalid.
	ErrInvalidNamespace = errors.New("invalid namespace")
)
