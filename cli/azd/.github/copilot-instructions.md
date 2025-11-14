# GitHub Copilot Instructions for Azure Developer CLI (azd)

## Project Overview

The Azure Developer CLI (azd) is a comprehensive command-line tool built in Go that streamlines Azure application development and deployment. The project follows Microsoft coding standards and uses a layered architecture with dependency injection, structured command patterns, and comprehensive testing.

## Getting Started

### Prerequisites
- [Go](https://go.dev/dl/) 1.24
- [VS Code](https://code.visualstudio.com/) with [Go extension](https://marketplace.visualstudio.com/items?itemName=golang.Go)

### Building & Testing
```bash
# Build
cd cli/azd
go build

# Run tests (unit only)
go test ./... -short

# Run all tests (including end-to-end)
go test ./...
```

### Development Guidelines
- Check existing [bug issues](https://github.com/Azure/azure-dev/issues?q=is%3Aopen+is%3Aissue+label%3Abug) or [enhancement issues](https://github.com/Azure/azure-dev/issues?q=is%3Aopen+is%3Aissue+label%3Aenhancement)
- Open an issue before starting work on significant changes
- Submit pull requests following the established patterns

## Architecture & Design Patterns

### Core Architecture
- **Layered Architecture**: `ActionDescriptor Tree → CobraBuilder → Cobra Commands → CLI`
- **Dependency Injection**: IoC container pattern for service resolution
- **Command Pattern**: Actions implement the `Action` interface with `Run(ctx context.Context) (*ActionResult, error)`
- **Model Context Protocol (MCP)**: Server implementation for AI agent interactions

### Key Components
- **ActionDescriptor**: Higher-order component defining commands, flags, middleware, and relationships
- **Actions**: Application logic handling CLI commands (`cmd/actions/`)
- **Tools**: External tool integrations and MCP server tools
- **Packages**: Reusable business logic (`pkg/`)
- **Internal**: Internal implementation details (`internal/`)

## Command Development

For detailed guidance on adding new commands, see:
- **[docs/new-azd-command.md](./docs/new-azd-command.md)** - Comprehensive guide for adding new commands

### Quick Reference
- Follow the ActionDescriptor pattern for new commands
- Use dependency injection for service resolution
- Implement proper error handling and output formatting
- Support multiple output formats (JSON, Table, None)

## Code Quality Standards

### Required Linting Pipeline
Always run this complete pipeline before submitting changes:
```bash
cspell lint '**/*.go' --config ./.vscode/cspell.yaml --root . --no-progress && \
golines . -w -m 125 && \
golangci-lint run --timeout 5m && \
../../eng/scripts/copyright-check.sh . --fix
```

**Pipeline Components:**
- `cspell`: Spell checking for Go files
- `golines`: Line length formatting for Go files (125 char limit)
- `golangci-lint`: Go code quality and style checking
- `copyright-check.sh`: Ensures proper Microsoft copyright headers

### Line Length & Formatting
- **Maximum line length for Go files**: 125 characters (enforced by `lll` linter)
- Use `golines` with `-m 125` flag for automatic formatting of Go code
- Break long strings in Go code using string concatenation with `+`
- **Documentation files (Markdown)**: No strict line length limit, prioritize readability

### Spelling & Documentation
- Use cspell with project config: `--config ./.vscode/cspell.yaml`
- Add technical terms to document-specific overrides in `.vscode/cspell.yaml`
- Pattern for document-specific words:
```yaml
overrides:
  - filename: path/to/file.ext
    words:
      - technicalterm1
      - technicalterm2
```

### Copyright Headers
All Go files must include Microsoft copyright header:
```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
```

## Extensions Development

### Building Extensions
Extensions are located in `extensions/` directory and use the extension framework:
```bash
# Build and install extension (example using demo extension)
cd extensions/microsoft.azd.demo
azd x build

# Test extension (using extension's namespace from extension.yaml)
azd demo <command>
```

## MCP Tools Development

### Tool Pattern
MCP tools follow the ServerTool interface pattern from `github.com/mark3labs/mcp-go/server`. Each tool should have:
- Constructor function: `NewXXXTool() server.ServerTool`
- Handler function: `handleXXX(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)`
- Proper tool descriptions and parameter definitions
- Snake_case tool names (e.g., `azd_plan_init`)

## Package Structure Guidelines

### Import Organization
1. Standard library imports
2. External dependencies
3. Azure/azd internal packages
4. Local package imports

### Internal vs Package Separation
- `internal/`: Implementation details, not meant for external use
- `pkg/`: Reusable business logic that could be imported by other projects
- Clear interface boundaries between packages

## Testing Requirements

### Test Commands
```bash
# Unit tests only
go test ./... -short

# All tests including end-to-end
go test ./...
```

### Test File Patterns
- Unit tests: `*_test.go` alongside source files
- Functional tests: `test/functional/` directory
- Mock exclusions configured in `.golangci.yaml`

## Error Handling & Logging

### Error Patterns
- Use `fmt.Errorf` for error wrapping with context
- Return meaningful error messages for CLI users
- Handle context cancellation appropriately

### Output Formatting
- Support multiple output formats: JSON, Table, None
- Use structured output for machine consumption
- Provide user-friendly messages for human consumption

## Documentation Standards

### Code Documentation
- Public functions and types must have Go doc comments
- Comments should start with the function/type name
- Provide context and usage examples where helpful

### Inline Documentation
- Use clear variable and function names
- Add comments for complex business logic
- Document non-obvious dependencies or assumptions

## Security & Best Practices

### Enabled Linters
- `errorlint`: Error handling best practices
- `gosec`: Security vulnerability detection
- `lll`: Line length enforcement (125 chars)
- `staticcheck`: Advanced static analysis

### Security Considerations
- Handle sensitive data appropriately (credentials, tokens)
- Validate all user inputs
- Use secure defaults for configuration
- Follow Azure security best practices

## Validation Checklist

Before submitting any changes, ensure:

- [ ] All linting pipeline steps pass without errors
- [ ] Copyright headers are present on all new files
- [ ] Spelling check passes with appropriate dictionary entries
- [ ] Line length under 125 characters for Go files (Markdown files have no strict limit)
- [ ] Tests pass (unit and integration where applicable)
- [ ] Error handling is comprehensive and user-friendly
- [ ] Documentation is updated for new features
- [ ] Command patterns follow established conventions
- [ ] MCP tools follow ServerTool interface pattern
- [ ] Package organization follows internal/pkg separation
- [ ] Import statements are properly organized

## Common Patterns to Follow

### Key Principles
- Use ActionDescriptor pattern for command registration
- Leverage dependency injection through IoC container
- Follow established naming conventions (see docs/new-azd-command.md)
- Implement proper error handling and output formatting
- Use structured configuration with sensible defaults

### Go Code Structure Standards

When creating or modifying Go struct files, follow this organization order:

1. **Package Documentation**: At the top of main file or `doc.go`
   ```go
   // Package packagename provides...
   package packagename
   ```

2. **Constants**: Package-level constants
3. **Package-level Variables**: Global variables and error definitions
4. **Type Declarations**: Structs, interfaces, and custom types
   - For 3+ types, consider using `types.go` file
   - Custom error types with struct declarations (or in `types.go` if many)
5. **Primary Struct Declaration(s)**: Main structs for the package
6. **Constructor Functions**: `NewXXX()` functions
7. **Public Struct Methods**: `func (s *Struct) PublicMethod()`
   - Group interface implementations with comment: `// For XXX interface support`
8. **Private Struct Methods**: `func (s *struct) privateMethod()`
9. **Private Package Functions**: `func privateFunction()`

**File Organization Guidelines:**
- **Complex packages**: Use `types.go` for type definitions, `init.go` for initialization
- **Package init**: Place `init()` functions at top of file or in dedicated `init.go`
- **Struct embedding**: No special placement rules - treat embedded and embedding structs equally

This instruction set ensures consistency with the established codebase patterns and helps maintain the high-quality standards expected in the Azure Developer CLI project.
