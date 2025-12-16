# GitHub Copilot instructions

This is the Azure Developer CLI (azd) - a sophisticated Go-based CLI tool for managing Azure application development workflows. It handles infrastructure provisioning with Bicep/Terraform, application deployment, environment management, project lifecycle automation, and provides extensible hooks system with gRPC extensions. Please follow these comprehensive guidelines when contributing.

## Core Architecture Overview

### Application Entry Point & Container Bootstrap
- **Main application**: `cli/azd/main.go` - Entry point with telemetry initialization, version checking, and IoC container bootstrap
- **Root command**: `cli/azd/cmd/root.go` - Defines complete Cobra command tree with middleware registration and action descriptors

### Dependency Injection System (Critical)
- **IoC Container**: `cli/azd/pkg/ioc/container.go` - Custom dependency injection container supporting hierarchical scopes
- **Service Registration**: Supports singleton, scoped, and transient lifetimes with NestedContainer patterns
- **Pattern**: All major components use constructor injection via the IoC container
- **Service Resolution**: Components resolve dependencies through `RegisterSingleton`, `RegisterScoped`, `RegisterTransient`

### Command Architecture
- **Action Descriptors**: Commands defined as action descriptors with middleware chains, not traditional Cobra handlers
- **Middleware Pipeline**: Extensive middleware system for telemetry, auth guards, hooks, debugging
- **Command Builder**: `cli/azd/cmd/cobra_builder.go` - Dynamically builds Cobra commands from action descriptors

### Key Architectural Patterns
1. **Dependency Injection**: Everything flows through IoC container - never use direct instantiation for major components
2. **Middleware Composition**: Commands execute through middleware chains (telemetry, auth, hooks, debug)
3. **Action-Based**: Commands implement `actions.Action` interface rather than traditional Cobra handlers
4. **Extensible Hooks**: Event-driven system with both internal hooks and external gRPC extensions

## Code Standards

### Required before each commit
**IMPORTANT**: Before committing any changes, ensure all the following checks are performed:
- From `cli/azd/` directory, run `gofmt -s -w .` before committing any changes to ensure proper code formatting
- From `cli/azd/` directory, run `golangci-lint run ./...` to check for linting issues
- From `cli/azd/` directory, run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress` to check spelling
- All Go files must include the standard copyright header:
  ```go
  // Copyright (c) Microsoft Corporation. All rights reserved.
  // Licensed under the MIT License.
  ```

### Development flow

**Build `azd` binary:**
```bash
cd cli/azd
go build
```

**Test:**
```bash
go test ./... -short
```
**IMPORTANT**: Allow up to 10 minutes for all the tests to complete.

## Repository structure
- `cli/azd/`: Main CLI application and command definitions
- `cli/azd/cmd/`: CLI command implementations (Cobra framework with action descriptors)
- `cli/azd/pkg/`: Core packages (IoC, project management, Azure APIs, infrastructure providers)
- `cli/azd/internal/`: Internal packages (telemetry, tracing, versioning, command utilities)
- `cli/azd/test/`: Test helpers, mocks, and snapshot testing utilities
- `templates/`: Sample azd templates and common project patterns
- `schemas/`: JSON schemas for azure.yaml project configuration
- `ext/`: Extensions for VS Code, Azure DevOps, and Dev Containers
- `eng/`: Build scripts and CI/CD pipelines

## Critical Development Patterns

### Dependency Injection Registration
When creating new services, always register them in the IoC container:
```go
// In a container registration function
ioc.RegisterSingleton(container, func() *MyService {
    return &MyService{
        dependency: ioc.Get[*SomeDependency](container),
    }
})
```

### Action Implementation Pattern
Commands should implement the Action interface:
```go
type myAction struct {
    dependency *SomeService
}

func newMyAction(dep *SomeService) actions.Action {
    return &myAction{dependency: dep}
}

func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    // Implementation
    return &actions.ActionResult{Message: &actions.ResultMessage{Header: "Success"}}, nil
}
```

### Middleware Integration
For cross-cutting concerns, implement middleware:
```go
type MyMiddleware struct {
    options *Options
}

func (m *MyMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
    // Pre-processing
    result, err := next(ctx)
    // Post-processing
    return result, err
}
```

### Project Configuration & Hooks
- **Project Config**: `cli/azd/pkg/project/project_config.go` - Central project configuration with azure.yaml parsing
- **Hooks System**: `cli/azd/pkg/project/framework_service.go` & middleware - Supports both internal and external hooks
- **Event Handlers**: Projects and services can register lifecycle event handlers

### Infrastructure Providers
- **Bicep Provider**: `cli/azd/pkg/infra/provisioning/bicep/` - Default ARM/Bicep infrastructure provisioning
- **Terraform Provider**: `cli/azd/pkg/infra/provisioning/terraform/` - Alternative Terraform infrastructure provisioning
- **Provider Interface**: Abstractions allow pluggable infrastructure backends

## Testing Approach

### Unit Testing Patterns
- **Table-Driven Tests**: Use table-driven test patterns consistently
- **Snapshot Testing**: CLI help output tested via snapshot files in `cli/azd/cmd/testdata/*.snap`
- **Update Snapshots**: Set `UPDATE_SNAPSHOTS=true` before running `go test` when CLI output changes
- **Mock Framework**: Extensive mocking with testify/mock in `cli/azd/test/mocks/`

### Test Structure Example
```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"ValidInput", "test", "result", false},
        {"InvalidInput", "", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := MyFunction(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            require.Equal(t, tt.expected, result)
        })
    }
}
```

## Azure Integration Patterns

### Azure SDK Integration
- **ARM Client**: Consistent use of Azure SDK for Go with proper credential management
- **Authentication**: `cli/azd/pkg/auth/` - Multi-method auth (device code, service principal, managed identity)
- **Subscription Management**: Context-aware subscription and tenant management

### Telemetry & Observability
- **OpenTelemetry**: Comprehensive tracing with spans and baggage for command correlation
- **Telemetry System**: `cli/azd/internal/telemetry/` - Background telemetry upload with local storage queue
- **Privacy**: Telemetry respects user opt-out via `AZURE_DEV_COLLECT_TELEMETRY=no`
- **Debug Tracing**: Support for `--trace-log-file` and `--trace-log-url` flags

### Error Handling & User Experience
- **Structured Errors**: Comprehensive error mapping in `cli/azd/internal/cmd/errors.go`
- **User-Friendly Messages**: Azure deployment errors parsed and formatted for readability
- **Trace ID Integration**: Server errors include trace IDs for troubleshooting
- **Cancellation Support**: Proper context cancellation throughout the application

## Extensions & Hooks System

### Internal Hooks
- **Lifecycle Events**: Project and service lifecycle events (provision, deploy, package, etc.)
- **Event Registration**: Components can register handlers for specific lifecycle phases
- **Error Aggregation**: Multiple event handlers can fail, errors are aggregated

### External Extensions (gRPC)
- **Extension Server**: `cli/azd/grpc/` - gRPC server for external extensions in multiple languages
- **Language Bindings**: JavaScript (`ext/azd/extensions/.../javascript/`) and .NET support
- **Event Manager**: External extensions can register for project/service lifecycle events

## Key Guidelines
1. **Dependency Injection First**: Always use IoC container for service registration and resolution
2. **Action-Based Commands**: Implement commands as actions with middleware rather than direct Cobra handlers  
3. **Table-Driven Tests**: Use consistent table-driven testing patterns with comprehensive test cases
4. **Telemetry Integration**: Include appropriate telemetry spans for new operations
5. **Error Context**: Provide rich error context with appropriate user-facing messages
6. **Context Propagation**: Always propagate context.Context through call chains for cancellation support
7. **Snapshot Testing**: Update snapshots when changing CLI help text or command structure

## Changelog updates for releases

When asked to prepare a release changelog, use the appropriate custom agent instructions:
- `.github/agents/changelog-core.agent.md` for core CLI releases
- `.github/agents/changelog-extension.agent.md` for extension releases
