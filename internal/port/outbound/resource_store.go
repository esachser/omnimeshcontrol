// Package outbound defines the driven port interfaces — contracts for infrastructure
// that the application layer needs. Implementations live in internal/adapter/outbound/.
package outbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// ResourceEventType indicates the kind of change in a watch event.
type ResourceEventType int

const (
	// ResourceEventPut indicates a resource was created or updated.
	ResourceEventPut ResourceEventType = iota
	// ResourceEventDelete indicates a resource was deleted.
	ResourceEventDelete
)

// ResourceEvent represents a single change observed via a watch.
type ResourceEvent struct {
	// Type indicates whether this is a put or delete event.
	Type ResourceEventType
	// Key is the storage key of the resource that changed.
	Key string
	// Resource is the resource value (nil on delete events).
	Resource *model.Resource
	// Revision is the etcd revision (or equivalent) of this event.
	Revision int64
}

// ResourceStore is the generic storage interface for all declarative resources.
// It provides CRUD operations and watch capabilities. The default implementation
// maps to /omnimesh/resources/ keys in etcd.
type ResourceStore interface {
	// Put creates or updates a resource, returning the new revision.
	Put(ctx context.Context, key string, resource *model.Resource) (revision int64, err error)
	// Get retrieves a resource by key. Returns domain.ErrResourceNotFound if not found.
	Get(ctx context.Context, key string) (*model.Resource, error)
	// List retrieves all resources under a key prefix.
	List(ctx context.Context, prefix string) ([]*model.Resource, error)
	// Delete removes a resource by key.
	Delete(ctx context.Context, key string) error
	// DeleteByPrefix removes all resources under a key prefix.
	DeleteByPrefix(ctx context.Context, prefix string) error
	// Watch returns a channel of events for resources under the given prefix.
	// The channel is closed when the context is cancelled.
	Watch(ctx context.Context, prefix string) (<-chan ResourceEvent, error)
	// CompareAndPut performs an optimistic concurrency update. It only succeeds
	// if the resource's current revision matches expectedRev.
	CompareAndPut(ctx context.Context, key string, expectedRev int64, resource *model.Resource) (int64, error)
}
