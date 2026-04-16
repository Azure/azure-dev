# System Overview

High-level architecture of the Azure Developer CLI.

## Entry Points

```text
cli/azd/main.go → cmd/root.go (command tree) → cmd/container.go (IoC registration)
```

- **`main.go`** — Application entry point; initializes the root command
- **`cmd/root.go`** — Registers the entire command tree using ActionDescriptors
- **`cmd/container.go`** — Registers all services in the IoC container

## Directory Structure

```text
cli/azd/
├── main.go              # Entry point
├── cmd/                 # Command definitions (ActionDescriptor pattern)
│   ├── root.go          # Command tree registration
│   ├── container.go     # IoC service registration
│   ├── actions/         # Action framework (interfaces, results)
│   └── middleware/      # Cross-cutting concerns (telemetry, hooks, extensions)
├── pkg/                 # Reusable public packages
│   ├── ioc/             # Dependency injection container
│   ├── project/         # Project configuration (azure.yaml), service targets, frameworks
│   ├── infra/           # Infrastructure providers (Bicep, Terraform)
│   ├── azapi/           # Azure API clients
│   └── tools/           # External tool wrappers (bicep, gh, pack, etc.)
├── internal/            # Internal packages (telemetry, tracing, terminal)
├── test/                # Test utilities and functional tests
├── extensions/          # First-party extensions
└── docs/                # Implementation-level documentation
```

## Key Subsystems

### Command Execution

Commands follow the ActionDescriptor → CobraBuilder → Cobra pipeline:

1. ActionDescriptors define commands declaratively in `cmd/root.go`
2. CobraBuilder transforms descriptors into Cobra commands at startup
3. Middleware wraps command execution for telemetry, hooks, and extensions
4. Actions implement the business logic via the `actions.Action` interface

See [Command Execution Model](command-execution-model.md) for details.

### Dependency Injection

All services are registered in `cmd/container.go` using the IoC container (`pkg/ioc`). Services are resolved at runtime — never instantiated directly.

```go
container.MustRegisterTransient(resolveFn any)
```

### Infrastructure Provisioning

The provisioning pipeline compiles IaC templates, runs preflight checks, deploys to Azure, and tracks state:

1. Compile Bicep/Terraform templates
2. Run local preflight validation (permissions, resource conflicts)
3. Submit deployment to Azure Resource Manager
4. Track provision state hash for change detection

See [Provisioning Pipeline](provisioning-pipeline.md) for details.

### Extension Framework

Extensions communicate via gRPC and can provide new capabilities:

1. Extensions are discovered from registries (local or remote)
2. azd launches extension processes and connects via gRPC
3. Extensions implement capability interfaces (framework, service target, event handler, etc.)
4. The middleware pipeline routes events to registered extensions

See [Extension Framework](extension-framework.md) for details.

### Telemetry

OpenTelemetry-based tracing with centralized event prefixes:

- Command events: `cmd.*`
- VS Code RPC events: `vsrpc.*`
- MCP tool events: `mcp.*`

See [Observability Guide](../guides/observability.md) for instrumentation details.

## Related Resources

- [AGENTS.md](../../AGENTS.md) — Quick reference for AI coding agents
- [azd Style Guide](../../cli/azd/docs/style-guidelines/azd-style-guide.md) — Code and UX standards
