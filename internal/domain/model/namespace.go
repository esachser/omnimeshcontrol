package model

// NamespaceIsolation determines the default authorization behavior within a namespace.
type NamespaceIsolation int

const (
	// NamespaceIsolationStrict is the default: services within the namespace
	// cannot communicate unless an explicit ALLOW policy exists.
	NamespaceIsolationStrict NamespaceIsolation = iota
	// NamespaceIsolationOpen allows free communication between services
	// within the namespace (no explicit ALLOW policy required).
	NamespaceIsolationOpen
)

// String returns the string representation of a NamespaceIsolation.
func (n NamespaceIsolation) String() string {
	switch n {
	case NamespaceIsolationOpen:
		return "open"
	default:
		return "strict"
	}
}

// Namespace is a logical grouping for mesh resources. A namespace must exist
// before any namespaced resource can be created within it.
type Namespace struct {
	Name      string
	Labels    map[string]string
	Isolation NamespaceIsolation
}
