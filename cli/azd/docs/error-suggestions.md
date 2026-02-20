# Error Suggestions

Azure Developer CLI includes an extensible error handling pipeline that transforms cryptic error messages into user-friendly guidance. When users encounter well-known errors (quota limits, authentication failures, deployment conflicts, missing tools, etc.), azd displays:

1. A **user-friendly message** explaining what went wrong
2. An **actionable suggestion** for next steps
3. A **documentation link** for more information
4. The **original error** (in grey) for technical reference

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                        Error Occurs                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  ErrorHandlerPipeline evaluates rules from                      │
│  resources/error_suggestions.yaml                               │
│                                                                 │
│  For each rule:                                                 │
│    1. errorType?  → Match Go error type via reflection          │
│    2. properties? → Check struct fields via dot-path            │
│    3. patterns?   → Match error text (substring/regex)          │
│    4. handler?    → Invoke named handler for dynamic response   │
│                                                                 │
│  All specified conditions must pass. First matching rule wins.  │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┴───────────────┐
              │                               │
              ▼                               ▼
┌─────────────────────────┐     ┌─────────────────────────────────┐
│   Rule Matched          │     │   No Match                      │
│   Wrap error with       │     │   Return original error         │
│   ErrorWithSuggestion   │     │   (may go to AI if enabled)     │
└─────────────────────────┘     └─────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────────┐
│  UxMiddleware displays:                                         │
│  1. User-friendly message (ERROR: ...)                          │
│  2. Actionable suggestion (Suggestion: ...)                     │
│  3. Documentation link (Learn more: ...)                        │
│  4. Original error in grey (technical details)                  │
└─────────────────────────────────────────────────────────────────┘
```

### Example Output

When a user hits a quota error, instead of seeing a wall of red text, they see:

```
ERROR: Your Azure subscription has reached a resource quota limit.

Suggestion: Request a quota increase through the Azure portal, or try deploying to a different region.
Learn more: https://learn.microsoft.com/azure/quotas/quickstart-increase-quota-portal

Deployment failed: QuotaExceeded for resource type Microsoft.Compute/virtualMachines in location eastus...
```

## Adding New Rules

The error rules are defined in [`resources/error_suggestions.yaml`](../resources/error_suggestions.yaml). This file is designed to be easily editable by anyone—no Go programming knowledge required for most cases.

### Three Matching Strategies

#### 1. Text Patterns (simplest)

Match against the error message text. Good for tool errors, CLI messages, and any error without a typed Go struct.

```yaml
- patterns:
    - "quota exceeded"         # Case-insensitive substring
    - "QuotaExceeded"
  message: "Your Azure subscription has reached a resource quota limit."
  suggestion: "Request a quota increase through the Azure portal."
  docUrl: "https://learn.microsoft.com/azure/quotas/..."
```

#### 2. Error Type + Properties (typed errors)

Match against specific Go error types using reflection. This lets you target structured errors and inspect their fields. For ARM deployment errors, use `DeploymentErrorLine` to match error codes at any depth in the error tree.

```yaml
# Match ARM deployment errors with a specific error code
# DeploymentErrorLine nodes are found at any depth via multi-unwrap
- errorType: "DeploymentErrorLine"
  properties:
    Code: "FlagMustBeSetForRestore"
  message: "A soft-deleted resource is blocking deployment."
  suggestion: "Run 'azd down --purge' to permanently remove it, then retry."
  docUrl: "https://learn.microsoft.com/azure/key-vault/general/key-vault-recovery"

# Match auth errors (direct type match)
- errorType: "AuthFailedError"
  message: "Authentication with Azure failed."
  suggestion: "Run 'azd auth login' to sign in again."
```

**How it works:**
- `errorType` is the Go struct type name (e.g., `DeploymentErrorLine`, `ExitError`)
- The error chain is walked (including multi-unwrap trees) to find the matching type
- `properties` uses dot notation to access struct fields via reflection (e.g., `Code`)
- Both type AND properties must match on the same error node
- By default, patterns and property values use case-insensitive substring matching
- Set `regex: true` on the rule to treat all patterns and property values as regular expressions

#### 3. Combined: Error Type + Text Patterns

When error codes are too broad (like generic "Conflict"), combine type matching with text patterns to narrow the match:

```yaml
- errorType: "DeploymentErrorLine"
  regex: true
  properties:
    Code: "Conflict"
  patterns:
    - "(?i)soft.?delete"  # Also require this text in the error message
  message: "A soft-deleted resource is causing a deployment conflict."
  suggestion: "Purge the resource in the Azure portal, then retry."
```

### Named Handlers (dynamic suggestions)

For cases that need code to compute a suggestion (e.g., including the current region or checking live state), you can reference a named `ErrorHandler` registered in the IoC container:

```yaml
- errorType: "DeploymentErrorLine"
  properties:
    Code: "SkuNotAvailable"
  handler: "skuNotAvailableHandler"
```

When a handler is set, the static `message`/`suggestion`/`docUrl` fields are ignored — the handler computes the full response dynamically.

The handler implements the `ErrorHandler` interface:

```go
// pkg/errorhandler/handler.go
type ErrorHandler interface {
    Handle(ctx context.Context, err error) *ErrorWithSuggestion
}
```

Example — the built-in `SkuNotAvailableHandler` includes the current `AZURE_LOCATION` in its suggestion:

```go
func (h *SkuNotAvailableHandler) Handle(_ context.Context, err error) *ErrorWithSuggestion {
    location := os.Getenv("AZURE_LOCATION")

    suggestion := "Try a different region with 'azd env set AZURE_LOCATION <region>'."
    if location != "" {
        suggestion = fmt.Sprintf(
            "The current region is '%s'. Try a different region with "+
                "'azd env set AZURE_LOCATION <region>'. "+
                "To see available SKUs, run 'az vm list-skus --location %s --output table'.",
            location, location,
        )
    }

    return &ErrorWithSuggestion{
        Err:        err,
        Message:    "The requested VM size or SKU is not available in this region.",
        Suggestion: suggestion,
        DocUrl:     "https://learn.microsoft.com/...",
    }
}
```

Register handlers by name in `cmd/container.go`:

```go
container.MustRegisterNamedSingleton("skuNotAvailableHandler",
    errorhandler.NewSkuNotAvailableHandler,
)
```

### Fields Reference

| Field | Required | Description |
|-------|----------|-------------|
| `patterns` | At least one of `patterns` or `errorType` | List of strings/regex to match against error text |
| `errorType` | At least one of `patterns` or `errorType` | Go error struct type name (matched via reflection) |
| `properties` | No (requires `errorType`) | Map of dot-path field names to expected values |
| `regex` | No | When true, all patterns and property values use regex matching |
| `message` | Yes (unless `handler` is set) | User-friendly explanation of what went wrong |
| `suggestion` | Yes (unless `handler` is set) | Actionable next steps for the user |
| `docUrl` | No | Link to relevant documentation |
| `handler` | No | Name of IoC-registered `ErrorHandler` for dynamic suggestions |

### Pattern Types

#### Simple Substring (default)

Case-insensitive substring matching:

```yaml
patterns:
  - "quota exceeded"  # Matches "QuotaExceeded", "QUOTA EXCEEDED", etc.
```

#### Regular Expression

Set `regex: true` on the rule to treat all patterns and property values as regular expressions:

```yaml
regex: true
patterns:
  - "(?i)authorization.*failed"
  - "BCP\\d{3}"
```

| Pattern | Meaning |
|---------|---------|
| `(?i)` | Case-insensitive |
| `.*` | Match any characters |
| `\\d+` | One or more digits |
| `(foo|bar)` | Match "foo" or "bar" |

**Note:** In YAML, backslashes must be escaped as `\\`.

### Rule Evaluation

- **First match wins**: Rules are evaluated in order from top to bottom
- **All conditions must pass**: If a rule has both `errorType` and `patterns`, both must match
- **Order matters**: Place more specific rules before general ones

### Best Practices

1. **Keep messages simple**: Explain what went wrong in plain language

   ```yaml
   message: "Your Azure subscription has reached a resource quota limit."
   ```

2. **Make suggestions actionable**: Tell users exactly what to do

   ```yaml
   suggestion: "Run 'azd auth login' to sign in again."
   ```

3. **Use `errorType` for structured errors**: When the error has a known Go type, prefer type matching over text patterns — it's more reliable and less fragile

4. **Order by specificity**: Place more specific rules before general ones. For example, `Conflict + soft-delete keyword` rules must come before the bare `Conflict` rule. Text-only patterns should come last since they're the broadest.

5. **Test your rules**: Run `go test ./pkg/errorhandler/...` after making changes

## File Layout

| File | Purpose |
|------|---------|
| `resources/error_suggestions.yaml` | Error rules (edit this!) |
| `pkg/errorhandler/types.go` | YAML schema types |
| `pkg/errorhandler/pipeline.go` | Rule evaluation pipeline |
| `pkg/errorhandler/reflect.go` | Reflection-based error type/property matching (supports multi-unwrap) |
| `pkg/errorhandler/matcher.go` | Text pattern matching engine |
| `pkg/errorhandler/handler.go` | `ErrorHandler` interface for custom handlers |
| `pkg/errorhandler/sku_handler.go` | Example handler: `SkuNotAvailableHandler` |
| `pkg/errorhandler/errors.go` | `ErrorWithSuggestion` type |
| `pkg/output/ux/error_with_suggestion.go` | UX display component |

## Architecture

### ErrorHandlerPipeline

The pipeline is the single entry point. It evaluates YAML rules in order, checking each rule's conditions (errorType, properties, patterns). When all conditions pass, it either returns a static suggestion from the rule's fields or invokes a named handler.

The error type matcher uses depth-first traversal with multi-unwrap support (`Unwrap() []error`), so it can find typed errors at any depth in an error tree — including nested ARM deployment errors.

### ErrorWithSuggestion

The canonical error type lives in `pkg/errorhandler` so extensions can also create and return user-friendly errors:

```go
type ErrorWithSuggestion struct {
    Err        error   // Original error
    Message    string  // User-friendly explanation
    Suggestion string  // Actionable next steps
    DocUrl     string  // Optional documentation link
}
```

A type alias in `internal/` provides backward compatibility for existing code.

### Extension Participation

Extensions can participate in error handling by:

1. **Returning `ErrorWithSuggestion`**: Extensions can directly wrap errors with suggestions
2. **Registering named handlers**: Extensions can register `ErrorHandler` implementations via IoC for dynamic suggestion computation
