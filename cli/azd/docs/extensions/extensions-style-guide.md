# Extension Development Guidelines for `azd`

## Overview

This guide provides design guidelines and best practices for developing extensions to the Azure Developer CLI (`azd`). Following these guidelines ensures that extensions maintain consistency with core `azd` functionality and provide a seamless user experience.

## Design Guidelines for Extensions

### 1. **Command Integration Strategy**

- New functionality should extend existing command categories
- Use a verb-first structure, where the primary action (e.g., add, create, delete, list) is the top-level command, and the target entity or context follows as an argument or subcommand.
- Example: `azd add <new-resource-type>` instead of `azd <new-resource-type> add`

### 2. **Parameter and Flag Consistency**

- Reuse established parameter patterns across new commands
- Maintain consistent naming conventions (e.g., `--subscription`, `--name`, `--type`)
- Provide sensible defaults to reduce cognitive load

### 3. **Help and Discoverability**

- Integrate new functionality into existing `azd help` structure
- Provide contextual guidance within established command flows
- Maintain documentation consistency across core and extended features

### 4. **Template and Resource Integration**

- Leverage existing template system for new resource types
- Follow established template discovery and management patterns
- Integrate new resources into azd's resource lifecycle management

### 5. **CI/CD and IaC Guidance**

- Provide support for GitHub Actions and Azure DevOps
- Consider support for a range of IaC providers (Bicep, Terraform, etc.)

## Implementation Benefits

- **User Familiarity**: Builds on known command patterns and reduces learning curve
- **Discoverability**: New capabilities are found through existing workflows
- **Consistency**: Predictable behavior across all azd commands and extensions
- **Maintainability**: Systematic approach reduces complexity and technical debt
- **Extensibility**: Clear framework for adding capabilities without breaking existing patterns
- **Ecosystem Growth**: Provides foundation for third-party extensions and integrations

## Future Considerations

This framework enables:

- Advanced workflow automation
- Enhanced developer productivity features
- Consistent user experience across all `azd` functionality
- Integration of new Azure services and capabilities
- Third-party extension development

## Error Handling in Extensions

Extensions communicate with the azd host over gRPC. When an extension returns an error, it must be
serialized into an `ExtensionError` proto message so that the host can display it to users and
classify it in telemetry.

### Structured Error Types

The `azdext` package provides two structured error types:

- **`azdext.ServiceError`** — for HTTP/gRPC service failures (e.g., Azure API returned 429).
  Fields: `Message`, `ErrorCode`, `StatusCode`, `ServiceName`, `Suggestion`.

- **`azdext.LocalError`** — for local errors such as validation, auth, config, or internal failures.
  Fields: `Message`, `Code`, `Category`, `Suggestion`.

Both types implement `Error()`. They are detected via `errors.As` during serialization.

### Telemetry Classification

The host classifies extension errors into telemetry codes using the pattern:

| Error type | Telemetry code pattern |
|-----------|----------------------|
| `ServiceError` with `ErrorCode` | `ext.service.<errorCode>` |
| `ServiceError` with `StatusCode` | `ext.service.<serviceName>.<statusCode>` |
| `LocalError` | `ext.<category>.<code>` |
| Unclassified | `ext.run.failed` |

### The Two-Layer Pattern (Recommended)

**Command layer** (`internal/cmd/`): Creates structured errors with category, code, and suggestion.
This is the only layer that should create `LocalError` / `ServiceError` values.

**Business logic** (`internal/pkg/`): Returns plain Go errors with `fmt.Errorf("context: %w", err)`.
Does not create structured errors.

This separation ensures the command layer has the full context to choose the right category
and write a helpful suggestion, while business logic stays generic and testable.

### Including Original Error Context

Structured errors do not currently support Go's `errors.Unwrap` — the original error is not
preserved in the error chain. To retain debugging context, include the original error's message
in the `Message` field:

```go
return exterrors.Validation(code, fmt.Sprintf("manifest is invalid: %s", err), suggestion)
```

Only the structured metadata (`Message`, `Code`, `Category`, `Suggestion`, and service fields)
is transmitted over gRPC to the host. The original Go error is not available on the host side.

### Error Chain Precedence

When `WrapError` serializes an error for gRPC, it checks the chain via `errors.As` and picks
the **first** match in this order:

1. `ServiceError` (highest priority)
2. `LocalError`
3. `azcore.ResponseError` (auto-detected Azure SDK errors)
4. gRPC `Unauthenticated` (safety-net auth classification)
5. Fallback (unclassified)

Because Go's `errors.As` walks from outermost to innermost, the command-boundary pattern
naturally produces the correct classification.

### Error Code Conventions

Error codes should be:
- Lowercase `snake_case` (e.g., `missing_subscription_id`, `invalid_agent_manifest`)
- Descriptive of the specific failure, not the general category
- Unique within the extension
- Defined as `const` values (not inline strings) for consistency and grep-ability

### Display and UX

When a structured error has a non-empty `Suggestion`, the azd host displays it as a
formatted "ERROR + Suggestion" block. When there is no suggestion, only the error message
is shown. Extensions should provide suggestions for user-fixable errors (validation,
auth, dependency) and omit them for internal/unexpected errors.

---

*For core design principles that apply to all `azd` functionality, see [guiding-principles.md](../style-guidelines/guiding-principles.md).*
