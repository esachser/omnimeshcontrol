# Omnimesh Control Plane — Agent Rules

## Project Overview

**Omnimesh Control Plane** (`omnimeshcontrol`) is a Go-based control plane for the **Omnimesh** service mesh. It is responsible for managing, configuring, and orchestrating the data plane proxies, service discovery, traffic policies, observability, and security across a distributed mesh of services.

All code contributions must reflect production-grade quality, idiomatic Go, and strict adherence to **Hexagonal Architecture** (Ports & Adapters).

---

## 1. Go Code Quality Standards

### 1.1 Idiomatic Go

- Follow [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) guidelines.
- Use `gofmt` / `goimports` formatting — no exceptions.
- Prefer simplicity and readability over cleverness.
- Use meaningful, concise names: short names for small scopes, descriptive names for exported symbols.
- Avoid `init()` functions; prefer explicit initialization.

### 1.2 Error Handling

- **Never** ignore errors. Every error must be handled or explicitly documented if intentionally discarded.
- Wrap errors with context using `fmt.Errorf("operation: %w", err)` to build traceable error chains.
- Define domain-specific sentinel errors (e.g., `ErrServiceNotFound`, `ErrPolicyConflict`) in the domain layer.
- Use error types implementing `errors.Is` / `errors.As` when callers need to differentiate behavior.
- Avoid `panic` except for truly unrecoverable programmer errors during initialization.

### 1.3 Naming Conventions

- **Packages**: short, lowercase, singular nouns (e.g., `service`, `policy`, `proxy`). Avoid `util`, `common`, `helpers`.
- **Interfaces**: name by behavior, not by the implementor (e.g., `ServiceRegistry`, `PolicyEnforcer`). Do **not** prefix with `I`.
- **Exported types/functions**: use clear, self-documenting names with GoDoc comments.
- **Unexported helpers**: keep them close to their usage, in the same file when possible.

### 1.4 Concurrency

- Prefer passing `context.Context` as the first parameter for cancellation and deadline propagation.
- Use channels for communication, mutexes for state protection — pick the right tool.
- Always handle context cancellation and timeouts gracefully.
- Document goroutine ownership: who starts it, who stops it, and how.

### 1.5 Testing

- Write **table-driven tests** as the default testing pattern.
- Achieve high coverage on domain and application layers; adapters should be tested with integration tests.
- Use interfaces for dependency injection — never import concrete adapters in the domain layer.
- Use `testing.T.Helper()` in test helper functions.
- Name test files `*_test.go` next to the code they test.
- Use `testify` only if already in the dependency tree; prefer standard library `testing` and hand-written assertions.

### 1.6 Documentation

- Every exported symbol **must** have a GoDoc comment.
- Package-level doc comments should explain the package's purpose and its role within the architecture.
- Use `// TODO(owner):` for tracked work items. Do not leave bare `// TODO` without attribution.

---

## 2. Hexagonal Architecture (Ports & Adapters)

### 2.1 Layer Structure

The project follows a strict **Hexagonal Architecture** with the following layers:

```
internal/
├── domain/          # Core business logic, entities, value objects, domain services
│   ├── model/       # Domain entities and value objects
│   ├── service/     # Domain services (pure business rules)
│   └── event/       # Domain events
│
├── port/            # Ports — interfaces defining boundaries
│   ├── inbound/     # Driving ports (use cases / application API)
│   └── outbound/    # Driven ports (repository, external service contracts)
│
├── application/     # Application services — orchestrate domain logic via ports
│
└── adapter/         # Adapters — concrete implementations of ports
    ├── inbound/     # Driving adapters (gRPC handlers, HTTP handlers, CLI)
    │   ├── grpc/
    │   └── http/
    └── outbound/    # Driven adapters (database, external APIs, message brokers)
        ├── persistence/
        ├── messaging/
        └── discovery/
```

### 2.2 Dependency Rule

> **Dependencies always point inward.** The domain layer has ZERO external dependencies.

```
Adapters → Application → Domain
Adapters → Ports
Application → Ports
Domain → (nothing)
```

- **Domain** (`internal/domain`): Pure Go. No framework imports, no infrastructure imports, no third-party libraries. Only the Go standard library for basic types.
- **Ports** (`internal/port`): Interfaces only. No implementations. Ports define the contracts between layers.
- **Application** (`internal/application`): Orchestrates domain logic. Depends on ports (interfaces), never on adapters.
- **Adapters** (`internal/adapter`): Implement port interfaces. This is where frameworks, databases, gRPC, HTTP, and external SDKs live.

### 2.3 Port Design Rules

- Inbound ports define **use cases** from the perspective of external actors (e.g., `RegisterService`, `ApplyTrafficPolicy`).
- Outbound ports define **contracts for infrastructure** the application needs (e.g., `ServiceRepository`, `PolicyStore`, `EventPublisher`).
- Ports must live in the `internal/port` package and be defined as Go interfaces.
- A single port interface should follow the **Interface Segregation Principle** — keep interfaces small and focused.

### 2.4 Adapter Design Rules

- Each adapter implements exactly one (or a small, related set of) port interface(s).
- Adapters are wired together at application startup in `cmd/` via dependency injection (constructor injection).
- Adapters must **not** leak infrastructure types into the domain. Map external DTOs/protobuf types to domain models at the adapter boundary.
- Use the **Adapter pattern** for translating between external data formats and domain models.

---

## 3. Omnimesh Control Plane Domain Concepts

When working on this project, be aware of the core domain concepts:

| Concept              | Description                                                                 |
| -------------------- | --------------------------------------------------------------------------- |
| **Service**          | A logical unit in the mesh, identified by name and namespace                |
| **Proxy**            | A data plane sidecar managed by the control plane (e.g., Envoy-compatible) |
| **Traffic Policy**   | Rules governing routing, retries, timeouts, circuit breaking                |
| **Security Policy**  | mTLS configuration, authorization policies, identity management             |
| **Service Discovery**| Registry and health checking for mesh-connected services                    |
| **Observability**    | Metrics, tracing, and logging configuration pushed to proxies               |
| **Configuration**    | xDS or equivalent config distribution to data plane proxies                 |

All domain modeling should reflect these bounded contexts. Avoid anemic domain models — entities should encapsulate behavior, not just data.

---

## 4. Project Conventions

### 4.1 Directory Layout

```
omnimeshcontrol/
├── cmd/                    # Application entry points
│   └── omnimeshcontrol/    # Main binary
│       └── main.go
├── internal/               # Private application code (hex arch layers)
│   ├── domain/
│   ├── port/
│   ├── application/
│   └── adapter/
├── pkg/                    # Public reusable libraries (if any)
├── api/                    # API definitions (protobuf, OpenAPI specs)
├── configs/                # Default configuration files
├── rfcs/                   # Design documents and RFCs
├── scripts/                # Build, deploy, and utility scripts
├── go.mod
└── go.sum
```

### 4.2 Configuration

- Use environment variables or configuration files (YAML/TOML) — never hardcode values.
- Configuration should be loaded in the adapter layer and injected into application services.
- Define a configuration struct in `internal/adapter/` and validate it at startup.

### 4.3 Logging & Observability

- Use structured logging (e.g., `log/slog` from the standard library).
- Pass loggers via dependency injection, not globals.
- Add OpenTelemetry-compatible tracing and metrics at the adapter layer.

### 4.4 API Design

- Prefer gRPC for internal control plane communication.
- Provide an HTTP/REST gateway where external / human-facing access is needed.
- Define protobuf schemas in `api/proto/` and generate Go code into `api/gen/`.
- Adapter handlers must translate between protobuf/HTTP DTOs and domain models at the boundary.

### 4.5 Dependency Injection

- Use **constructor injection** — pass dependencies as interface parameters in `New*` functions.
- Wire everything together in `cmd/omnimeshcontrol/main.go`.
- Do **not** use DI frameworks or service locators. Explicit wiring is preferred.

---

## 5. Code Review Checklist

Before considering any code complete, verify:

- [ ] No domain imports in adapters reference concrete implementations of other adapters.
- [ ] Domain layer has zero imports outside `internal/domain` and the Go standard library.
- [ ] All port interfaces are in `internal/port/`.
- [ ] Errors are wrapped with context and use domain-specific sentinel errors where appropriate.
- [ ] All exported symbols have GoDoc comments.
- [ ] Tests exist for domain logic (unit) and adapters (integration).
- [ ] `context.Context` is threaded through all operations that may block or be cancelled.
- [ ] No global mutable state.
- [ ] Configuration is injected, not hardcoded.
- [ ] Protobuf / DTO ↔ domain model mapping happens exclusively in the adapter layer.

## 6. RFCs

- RFCs are stored in the `rfcs/` directory.
- Each RFC should be a markdown file with a unique name (e.g., `rfc-0001-initial-design.md`).
- RFCs should be submitted for review and approval before implementation.
- RFCs filenames should be in the format `rfc-<number>-<title>.md`.
- RFCs files should start with a short title, then a blank line, an indicative of status, then a blank line.
- RFCs files should include information about the problem being solved, the solution proposed, and any potential drawbacks or trade-offs.
- RFCs files should include a list of references to other RFCs, if any.
- RFCs files should include architecture diagrams, if any.