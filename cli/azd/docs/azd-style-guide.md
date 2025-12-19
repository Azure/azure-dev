# Azure Developer CLI (`azd`) Style Guide

## Overview

This style guide establishes standards for code, user experience, testing, and documentation across the Azure Developer CLI project. Following these guidelines ensures consistency, maintainability, and a high-quality user experience.

## Code Style Guidelines

### Go Conventions

#### Required Before Each Commit

**CRITICAL**: Before committing any changes:

1. Run `gofmt -s -w .` from `cli/azd/` directory for proper formatting
2. Run `golangci-lint run ./...` from `cli/azd/` directory to check for linting issues
3. Run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress` to check spelling
4. All Go files must include the standard copyright header:

   ```go
   // Copyright (c) Microsoft Corporation. All rights reserved.
   // Licensed under the MIT License.
   ```

#### Naming Conventions

- **Packages**: Use short, lowercase, single-word names (e.g., `auth`, `project`, `telemetry`)
- **Interfaces**: Use descriptive names ending in `-er` when appropriate (e.g., `Runner`, `Provider`)
- **Constructors**: Prefix with `New` and use dependency injection (e.g., `NewProjectService`)
- **Private fields**: Use camelCase with leading lowercase (e.g., `userConfig`)
- **Public fields**: Use PascalCase (e.g., `ProjectName`)

#### Dependency Injection

**ALWAYS** use the IoC container for service registration and resolution:

```go
// Register services in container
ioc.RegisterSingleton(container, func() *MyService {
    return &MyService{
        dependency: ioc.Get[*SomeDependency](container),
    }
})

// Constructor pattern
type myService struct {
    dependency *SomeDependency
}

func NewMyService(dep *SomeDependency) *myService {
    return &myService{dependency: dep}
}
```

**NEVER** use direct instantiation for major components.

### Error Handling

- **Context**: Provide rich error context with user-facing messages
- **Wrapping**: Use `fmt.Errorf` with `%w` verb to wrap errors
- **User messages**: Make error messages actionable and clear
- **Trace IDs**: Include trace IDs for server errors when available

```go
if err != nil {
    return fmt.Errorf("failed to provision resources: %w", err)
}
```

### Context Propagation

**ALWAYS** propagate `context.Context` through call chains for:

- Cancellation support
- Timeout management
- Request-scoped values
- Telemetry span propagation

## User Experience Patterns

### Progress Reports

Progress reports provide real-time feedback during multi-step operations. Use progress reports to:

- Show status of long-running commands
- Display individual service provisioning/deployment states
- Help users troubleshoot by showing exactly which step failed

#### Progress Report States

Items in a progress report list can be in one of five states:

1. **Loading**: `|===    | [Verb] Message goes here`
   - Indicates operation in progress
   - Shows loading bar animation

2. **Done**: `(✓) Done: [Verb] Message goes here`
   - Green checkmark indicates successful completion
   - Past tense verb

3. **Failed**: `(✗) Failed: [Verb] Message goes here`
   - Red X indicates error
   - Include specific error message below

4. **Warning**: `(!) Warning: [Verb] Message goes here`
   - Yellow exclamation for non-blocking issues
   - Command continues but user should be aware

5. **Skipped**: `(-) Skipped: [Verb] Message goes here`
   - Gray dash indicates intentionally skipped step
   - Different from failed - this is expected behavior

#### Progress Report Guidelines

- **Indentation**: Progress items are always indented under the main command
- **Hierarchy**: Sub-steps appear indented under parent steps
- **Verb consistency**: Use present progressive for loading, past tense for completed
- **Contextual information**: Include resource names, identifiers when relevant

#### Examples

**Success scenario:**

```
Provisioning Azure resources (azd provision)
Provisioning Azure resources can take some time.

  (✓) Done: Creating App Service Plan: plan-r2w2adrz3rvwxu
  (✓) Done: Creating Log Analytics workspace: log-r2w2adrz3rvwxu
  (✓) Done: Creating Application Insights: appi-r2w2adrz3rvwxu
  (✓) Done: Creating App Service: app-api-r2w2adrz3rvwxu
```

**Failure scenario:**

```
Provisioning Azure resources (azd provision)

  (✓) Done: Creating App Service Plan: plan-r2w2adrz3rvwxu
  (✓) Done: Creating Log Analytics workspace: log-r2w2adrz3rvwxu
  (✗) Failed: Creating Cosmos DB: cosmos-r2w2adrz3rvwxu
  
  The '{US} West US 2 (westus)' region is currently experiencing high demand
  and cannot fulfill your request. Failed to create Cosmos DB account.

ERROR: Unable to complete provisioning of Azure resources, 'azd up' failed
```

**Skipped scenario:**

```
Note: Steps were skipped because _____ (directory, or specified service name)
If you want to deploy all services, you can run azd deploy --all or
move to root directory.

  (✓) Done: [Verb] Message goes here
  (✓) Done: [Verb] Message goes here
  (-) Skipped: [Verb] Message goes here
```

### Command Output Standards

- **Structured output**: Support `--output json` for machine-readable output
- **Progress indicators**: Use spinners and progress bars for long operations
- **Colorization**: Use consistent colors (green=success, red=error, yellow=warning)
- **URLs**: Always include relevant portal/console URLs for created resources

## Testing Standards

### Test Structure

Use table-driven tests consistently:

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

### Snapshot Testing

- **CLI help output**: Test via snapshot files in `cli/azd/cmd/testdata/*.snap`
- **Updating snapshots**: Set `UPDATE_SNAPSHOTS=true` before running `go test` when output changes
- **Review changes**: Always review snapshot diffs to ensure they're intentional

### Mock Usage

- Use testify/mock framework for mocking dependencies
- Place mocks in `cli/azd/test/mocks/` directory
- Mock external dependencies (Azure SDK, file system, HTTP clients)

## Architecture Patterns

### Action-Based Commands

Commands implement the `Action` interface rather than traditional Cobra handlers:

```go
type myAction struct {
    dependency *SomeService
}

func newMyAction(dep *SomeService) actions.Action {
    return &myAction{dependency: dep}
}

func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    // Implementation
    return &actions.ActionResult{
        Message: &actions.ResultMessage{
            Header: "Success",
        },
    }, nil
}
```

### Middleware Pattern

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

### Telemetry Integration

- Add telemetry spans for all major operations
- Use OpenTelemetry conventions
- Include relevant attributes (command, duration, result)
- Respect `AZURE_DEV_COLLECT_TELEMETRY=no` opt-out

## Documentation Standards

### Code Comments

- **Public APIs**: Document all public functions, types, and methods
- **Complex logic**: Add inline comments explaining non-obvious code
- **TODOs**: Use `// TODO:` with context and owner when applicable

### Help Text

- **Consistency**: Match terminology across all commands
- **Examples**: Include at least one example per command
- **Flags**: Document all flags with clear descriptions
- **See also**: Cross-reference related commands

### Markdown Documentation

- Use proper formatting with headers, code blocks, and lists
- Include examples with both command and expected output
- Keep language simple and action-oriented
- Link to related documentation when appropriate

## Command Design Patterns

See [guiding-principles.md](guiding-principles.md) for detailed command structure design principles.

### Key Principles

- Use verb-first structure (`azd add <resource>`, not `azd <resource> add`)
- Build on existing command categories (View, Edit, Run)
- Maintain consistent parameter patterns
- Follow progressive disclosure (simple by default, advanced when needed)

## File Organization

### Package Structure

```
cli/azd/
├── cmd/           # Command definitions
├── pkg/           # Core packages
│   ├── auth/      # Authentication
│   ├── project/   # Project management
│   ├── infra/     # Infrastructure providers
│   └── ...
├── internal/      # Internal packages
│   ├── telemetry/ # Telemetry system
│   └── ...
└── test/          # Test utilities and mocks
```

### File Naming

- `*_test.go` for test files
- `mock_*.go` for mocks
- Keep related functionality in same package
- Split large files when they exceed ~500 lines

## Azure Integration Guidelines

### Authentication

- Support multiple auth methods (device code, service principal, managed identity)
- Handle token refresh gracefully
- Provide clear error messages for auth failures

### SDK Usage

- Use Azure SDK for Go consistently
- Handle rate limiting and retries
- Log Azure API calls for debugging
- Include correlation IDs in requests

## Performance Considerations

- Minimize Azure API calls
- Cache authentication tokens appropriately
- Use parallel operations where safe
- Provide progress feedback for operations >2 seconds

## Security Guidelines

- Never log sensitive information (tokens, passwords, keys)
- Respect Azure RBAC and subscription boundaries
- Validate user input before processing
- Use secure defaults for all operations

---

*For extension development guidelines, see [extensions-style-guide.md](extensions-style-guide.md).*
*For core design principles, see [guiding-principles.md](guiding-principles.md).*
