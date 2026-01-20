# Azure Developer CLI (`azd`) Style Guide

## Overview

This style guide establishes standards for code, user experience, testing, and documentation across the Azure Developer CLI project. Following these guidelines ensures consistency, maintainability, and a high-quality user experience.

## Code Style Guidelines

### Go Conventions

#### Pre-commit Checks

Run these commands from `cli/azd/` and ensure all files include the copyright header:

```bash
gofmt -s -w .
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
```

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
```

#### Naming

- **Packages**: Short, lowercase, single-word (e.g., `auth`, `project`).
- **Interfaces**: Descriptive, typically ending in `-er` (e.g., `Runner`).
- **Constructors**: `New` prefix, injecting dependencies (e.g., `NewProjectService`).
- **Fields**: `PascalCase` for public, `camelCase` for private.

#### Dependency Injection (IoC)

**ALWAYS** use the IoC container. **NEVER** use direct instantiation for major components.

```go
// 1. Register with container
ioc.RegisterSingleton(container, func() *MyService {
    return &MyService{
        dependency: ioc.Get[*SomeDependency](container),
    }
})

// 2. Definition & Constructor
type myService struct {
    dependency *SomeDependency
}

func NewMyService(dep *SomeDependency) *myService {
    return &myService{dependency: dep}
}
```

### Error Handling & Context

- **Errors**: Wrap with `fmt.Errorf` ("%w") to add actionable context and trace IDs.
- **Context**: **ALWAYS** propagate `context.Context` for cancellation and telemetry.

```go
func (s *Service) Run(ctx context.Context) error {
    if err := s.doWork(ctx); err != nil {
        return fmt.Errorf("failed to provision resources: %w", err)
    }
    return nil
}
```

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

### CLI Color Standards

The CLI uses industry-standard [ANSI defined colors](https://en.wikipedia.org/wiki/ANSI_escape_code#Colors) to ensure accessibility and compatibility across all terminals. This allows users to customize color palettes through their IDE or terminal preferences.

**Important**: Colors will appear differently depending on which terminal and theme (dark/light) the customer prefers. Always test output in both dark and light terminal themes.

#### Color Naming Convention

Each color set has a pair: standard and bright variants:

- **ANSI naming**: "[color]" (FG 30-37) and "Bright [color]" (FG 90-97)
- **PowerShell naming**: "Dark [color]" and "[color]"

Note: There is a discrepancy in the naming convention between ANSI Color coding (which uses "Bright [color]" and "[color]") and PowerShell (which uses "[color]" and "Dark [color]"). Developers working with ANSI Color definitions should use the darker version of the two.

#### ANSI Color Code Reference

**Standard 3/4-bit Colors** (Theme-adaptive - Recommended):

| FG | BG | Name | Foreground Code | Background Code | Standard RGB |
| --- | --- | --- | --- | --- | --- |
| 30 | 40 | Black | `\033[30m` | `\033[40m` | 0, 0, 0 |
| 31 | 41 | Red | `\033[31m` | `\033[41m` | 170, 0, 0 |
| 32 | 42 | Green | `\033[32m` | `\033[42m` | 0, 170, 0 |
| 33 | 43 | Yellow | `\033[33m` | `\033[43m` | 170, 85, 0 |
| 34 | 44 | Blue | `\033[34m` | `\033[44m` | 0, 0, 170 |
| 35 | 45 | Magenta | `\033[35m` | `\033[45m` | 170, 0, 170 |
| 36 | 46 | Cyan | `\033[36m` | `\033[46m` | 0, 170, 170 |
| 37 | 47 | White | `\033[37m` | `\033[47m` | 170, 170, 170 |
| 90 | 100 | Bright Black (Gray) | `\033[90m` | `\033[100m` | 85, 85, 85 |
| 91 | 101 | Bright Red | `\033[91m` | `\033[101m` | 255, 85, 85 |
| 92 | 102 | Bright Green | `\033[92m` | `\033[102m` | 85, 255, 85 |
| 93 | 103 | Bright Yellow | `\033[93m` | `\033[103m` | 255, 255, 85 |
| 94 | 104 | Bright Blue | `\033[94m` | `\033[104m` | 85, 85, 255 |
| 95 | 105 | Bright Magenta | `\033[95m` | `\033[105m` | 255, 85, 255 |
| 96 | 106 | Bright Cyan | `\033[96m` | `\033[106m` | 85, 255, 255 |
| 97 | 107 | Bright White | `\033[97m` | `\033[107m` | 255, 255, 255 |

**24-bit RGB Colors** (Exact colors - Use sparingly):
- Foreground: `\033[38;2;R;G;Bm` where R, G, B are 0-255
- Background: `\033[48;2;R;G;Bm` where R, G, B are 0-255
- Example: `\033[38;2;255;0;0m` for exact red foreground

**Note**: `\033` is the escape character in octal notation (can also be written as `\x1b` in hex or `\e` in some languages). Standard RGB values shown are from the VGA specification, but actual rendering varies by terminal and user theme preferences.

**Recommendation**: Use standard 3/4-bit colors (30-37, 90-97) as they adapt to the user's terminal theme preferences (dark/light mode). Only use 24-bit RGB colors when exact color matching is required. For complete ANSI escape code documentation, see [ANSI escape code - Colors](https://en.wikipedia.org/wiki/ANSI_escape_code#Colors).

#### Standard Color Usage

| Purpose | Color | ANSI Code | Usage |
| --- | --- | --- | --- |
| **Primary text** | Black (on light) / White (on dark) | Default | Standard command output |
| **Commands & parameters** | Bright Blue | `\033[94m` | Command names, parameters, system events |
| **Hyperlinks** | Bright Cyan | `\033[96m` | URLs, portal links |
| **Success** | Green | `\033[32m` | Success messages, completed operations |
| **Warning** | Yellow / Bright Yellow | `\033[33m` / `\033[93m` | Warning messages, non-critical issues |
| **Error** | Red | `\033[31m` | Error messages, failures |

#### Implementation Guidelines

- **Consistency**: Use the same color for the same purpose across all commands
- **Accessibility**: Never rely solely on color to convey information (use icons/symbols too)
- **Terminal compatibility**: Colors will render differently across terminals - test in multiple environments
- **Theme support**: Test in both light and dark terminal themes
- **Fallback**: Ensure output remains readable if colors are disabled

#### Examples

```go
// Success message
fmt.Println("\033[32m(✓) Done:\033[0m Creating resource")

// Warning message
fmt.Println("\033[33m(!) Warning:\033[0m Configuration may need update")

// Error message
fmt.Println("\033[31m(✗) Failed:\033[0m Unable to connect")

// Hyperlink (using OSC 8 hyperlinks when supported)
fmt.Printf("View in portal: \033[96m%s\033[0m\n", url)
```

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
