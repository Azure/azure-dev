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
- **Do not reuse reserved global flag names** — see section below

### Reserved Global Flags

azd pre-parses a set of global flags from the command line **before** dispatching to extensions.
The extension SDK (`NewExtensionRootCommand`) also registers these same flags on every
extension's root command. Because both azd and the extension parse the same `argv`, extensions
**must not** register flags that collide with these reserved names.

This is not a new restriction — it has been true since the extension system was designed.
The SDK now enforces it at startup via `ValidateNoReservedFlagConflicts()`.

#### Reserved flag names

| Long Name | Short | Purpose |
|-----------|-------|---------|
| `environment` | `e` | Selects the azd environment |
| `cwd` | `C` | Sets the working directory |
| `debug` | — | Enables debug logging |
| `no-prompt` | — | Non-interactive mode |
| `output` | `o` | Output format (json, table, none) |
| `help` | `h` | Command help (cobra built-in) |
| `docs` | — | Opens command documentation |
| `trace-log-file` | — | Diagnostics trace file |
| `trace-log-url` | — | OpenTelemetry trace endpoint |

#### What this means for extension authors

**DO:**
- Use any flag name that is not in the table above
- Use any single-letter short flag except `e`, `C`, `o`, `h`
- Access the environment name via `extCtx.Environment` (the SDK provides it automatically)
- Ignore reserved flags you don't need — the SDK handles them

**DON'T:**
- Register `--environment` or `-e` on any subcommand (use `--env-name` or `--target-env` if you need a second environment reference)
- Register `--debug`, `--cwd`, `--output`, `--help`, or their short forms for a different purpose
- Assume you can "override" a global flag on a subcommand — azd's pre-parser will consume it first

#### What happens if you collide

If your extension uses the azd SDK (`azdext.Run()`), the SDK validates all flags at startup.
A collision produces a clear error:

```
extension defines flags that conflict with reserved azd global flags:
  - command "custom create": flag --endpoint/-e conflicts with reserved global flag --environment
    (short flag -e is reserved by azd for --environment)
Remove or rename these flags to avoid conflicts with azd's global flags.
Reserved flags: environment, cwd, debug, no-prompt, output, help, docs, trace-log-file, trace-log-url
```

#### Background

For the full technical specification of how flags flow between azd and extensions, including
the pre-parsing pipeline, environment variable propagation, and enforcement implementation,
see [Extension Flag Architecture Spec](../design/extension-flag-architecture.md).

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

### Recommended Layering Pattern

**Entry-point or orchestration layer**: Usually creates structured errors once it can confidently choose the final category, code, and suggestion. This often includes command handlers, top-level actions, or other user-facing coordination code.

**Lower-level helpers, parsers, and clients**: Usually return plain Go errors with `fmt.Errorf("context: %w", err)` and let a higher layer classify the failure.

Treat this as guidance, not a strict package boundary. The important part is that structured classification happens in a layer with enough context to produce the right telemetry and a useful suggestion.

### Error Chain Precedence

When `WrapError` serializes an error for gRPC, it checks the chain via `errors.As` and picks
the **first** match in this order:

1. `ServiceError` (highest priority)
2. `LocalError`
3. `azcore.ResponseError` (auto-detected Azure SDK errors)
4. gRPC `Unauthenticated` (safety-net auth classification)
5. Fallback (unclassified)

Because Go's `errors.As` walks from outermost to innermost, classifying near the outer orchestration layer naturally produces the intended classification.

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
