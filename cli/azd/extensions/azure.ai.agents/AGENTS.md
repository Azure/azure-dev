# Azure AI Agents Extension - Agent Instructions

Instructions for AI coding agents working on the `azure.ai.agents` azd extension.

## Overview

This extension adds Foundry Agent Service support to the Azure Developer CLI (`azd`).
It provides commands for initializing, deploying, running, and monitoring AI agents
via `azd ai agent <command>`.

The extension runs as a separate Go binary that communicates with the azd host over gRPC.
It lives under `cli/azd/extensions/azure.ai.agents/` and has its own `go.mod`.

## Directory Structure

```
cli/azd/extensions/azure.ai.agents/
├── main.go                         # Extension entry point
├── extension.yaml                  # Extension metadata and capabilities
├── internal/
│   ├── cmd/                        # Command handlers (init, run, invoke, show, etc.)
│   │   └── root.go                 # Command tree registration
│   ├── exterrors/                   # Error factories and codes (see Error Handling)
│   │   ├── errors.go               # Structured error factory functions
│   │   └── codes.go                # Machine-readable error code constants
│   ├── pkg/                        # Business logic and API clients
│   │   └── agents/                 # Agent YAML parsing, service clients
│   ├── project/                    # Project-level configuration
│   ├── tools/                      # External tool wrappers
│   └── version/                    # Version constants
├── schemas/                        # JSON/YAML schemas
└── tests/                          # Test data and fixtures
```

## Development

### Build

```bash
cd cli/azd/extensions/azure.ai.agents

# Build using developer extension (preferred for local development)
azd x build

# Or build using Go directly
go build
```

### Test

```bash
# All tests
go test ./... -short

# Specific package
go test ./internal/exterrors/... -count=1

# Specific test
go test ./internal/cmd/... -run TestInit -count=1
```

### Pre-Commit Checklist

```bash
gofmt -s -w .
go fix ./...
golangci-lint run ./...
cspell lint "**/*.go" --relative --config ./cspell.yaml --no-progress
```

- **Line length**: 125 chars max for Go
- **Spelling**: Add technical terms to `cspell.yaml` overrides
- **Copyright**: All Go files need the Microsoft header

### Local Development with azd Core Changes

When making coordinated changes to both the azd core (`cli/azd/pkg/azdext/`) and
this extension, add a `replace` directive to `go.mod`:

```
replace github.com/azure/azure-dev/cli/azd => ../../
```

Run `go mod tidy` after adding/removing the directive. **Remove the replace
directive before merging.**

## Error Handling

Error handling in this extension uses a structured error system that provides:
- Machine-readable codes for telemetry classification
- Human-readable messages for display
- Optional user-facing suggestions for remediation

### The Two-Layer Pattern

```
┌──────────────────────────────────────────────────────────────────┐
│  Command Boundary (internal/cmd/)                                │
│                                                                  │
│  Translates errors into structured types with category, code,    │
│  and suggestion. This is where you choose the error category.    │
│                                                                  │
│  return exterrors.Validation(                                     │
│      exterrors.CodeInvalidAgentManifest,                          │
│      fmt.Sprintf("agent manifest is invalid: %s", err),          │
│      "check the agent.yaml schema documentation")                │
└────────────────────────────┬─────────────────────────────────────┘
                             │
                             │  plain Go errors (fmt.Errorf + %w)
                             │
┌────────────────────────────┴─────────────────────────────────────┐
│  Business Logic (internal/pkg/)                                   │
│                                                                   │
│  Returns plain Go errors. Wraps lower-level errors with           │
│  fmt.Errorf("context: %w", err) for debugging.                    │
│  Does NOT create structured errors — that's the command layer's   │
│  job.                                                             │
└──────────────────────────────────────────────────────────────────┘
```

**Why this separation?** The command layer has the full context to decide the
right category (validation? dependency? auth?) and to write a helpful suggestion.
Business logic often doesn't know why it was called or what the user should do next.

### When to Use Each Error Type

| Situation | What to use | Example |
|-----------|------------|---------|
| User input or manifest is invalid | `exterrors.Validation` | Missing required field in agent.yaml |
| Required resource is missing or unavailable | `exterrors.Dependency` | AI project endpoint not configured |
| Authentication or authorization failure | `exterrors.Auth` | Credential creation failed |
| Version or feature mismatch | `exterrors.Compatibility` | azd version too old |
| User cancellation (e.g., Ctrl+C) | `exterrors.Cancelled` | User cancelled subscription prompt |
| Azure HTTP API returned an error | `exterrors.ServiceFromAzure` | ARM deployment failed |
| azd host gRPC call failed | `exterrors.FromAiService` / `FromPrompt` | Model catalog call failed |
| Unexpected internal failure | `exterrors.Internal` | JSON marshalling failed |
| Error in a helper / business logic layer | `fmt.Errorf("context: %w", err)` | Pass it up for the command layer to classify |

### Including Original Error Context

When creating a structured error, include the original error's message in the
Message field so that debugging context is preserved in telemetry:

```go
// Include the original error message for debugging context
return exterrors.Validation(
    exterrors.CodeInvalidAgentManifest,
    fmt.Sprintf("agent manifest is invalid: %s", err),
    "check the agent.yaml schema documentation",
)
```

Note: Structured errors (`LocalError` / `ServiceError`) do not currently support
Go's `errors.Unwrap` interface — the original error is not part of the error chain.
Only the structured metadata (Message, Code, Category, Suggestion) is transmitted
over gRPC to the azd host.

### Error Chain Precedence

When `WrapError` serializes an error for gRPC transmission, it checks the error
chain (via `errors.As`) and picks the **first** match in this order:

1. `ServiceError` — service/HTTP failures (highest priority)
2. `LocalError` — local/config/auth failures
3. `azcore.ResponseError` — raw Azure SDK errors
4. gRPC Unauthenticated — safety-net auth classification
5. Fallback — unclassified

Because `errors.As` walks from outermost to innermost, the command-boundary
pattern naturally produces the correct telemetry classification.

### Error Codes

Define new error codes as constants in `internal/exterrors/codes.go`. Codes must be:
- lowercase `snake_case` (e.g., `missing_subscription_id`)
- Descriptive of the specific failure, not the general category
- Unique within the extension

### How Errors Flow to the azd Host

```
Extension process                    gRPC                    azd host
─────────────────                    ────                    ────────
exterrors.Validation(...)       →  WrapError()  →  ExtensionError proto
                                                           │
                                                   UnwrapError()
                                                           │
                                                   ┌───────┴──────────┐
                                                   │ UX middleware:    │
                                                   │  displays message │
                                                   │  + suggestion     │
                                                   ├──────────────────┤
                                                   │ Telemetry:       │
                                                   │  ext.validation. │
                                                   │  invalid_manifest │
                                                   └──────────────────┘
```

### Common Patterns

**Converting Azure SDK errors at the command boundary:**

```go
result, err := client.CreateAgent(ctx, request)
if err != nil {
    return exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
}
```

**Converting gRPC errors from azd host calls:**

```go
models, err := aiClient.ListModels(ctx, &azdext.ListModelsRequest{})
if err != nil {
    return exterrors.FromAiService(err, exterrors.CodeModelCatalogFailed)
}
```

**Handling prompt errors:**

```go
sub, err := promptClient.PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
if err != nil {
    return exterrors.FromPrompt(err, "failed to select subscription")
}
```

## Key Conventions

- **Import order**: stdlib → external → `github.com/azure/azure-dev/cli/azd/` → local `azureaiagent/`
- **Context propagation**: Always pass `ctx context.Context` as the first parameter
- **Subscription tenant**: When creating credentials from a prompted subscription,
  use `Subscription.UserTenantId` (user access tenant), NOT `Subscription.TenantId`
  (resource tenant). For multi-tenant/guest users these differ.
- **Modern Go**: This project uses Go 1.26. Prefer `slices`, `maps`, `min()`/`max()`,
  `range` over integers, and other modern features.
