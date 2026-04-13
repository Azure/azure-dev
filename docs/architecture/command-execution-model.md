# Command Execution Model

How CLI commands are registered, resolved, and executed in azd.

## Pipeline Overview

```text
ActionDescriptor → CobraBuilder → Cobra Command → Middleware → Action.Run()
```

### 1. ActionDescriptor Registration

Commands are declared in `cmd/root.go` as a tree of ActionDescriptors:

```go
root := actions.NewActionDescriptor("azd", nil)

root.Add("up", &actions.ActionDescriptorOptions{
    Command:        newUpCmd(),
    ActionResolver: newUpAction,
    FlagsResolver:  newUpFlags,
    OutputFormats:  []actions.OutputFormat{actions.OutputFormatJson},
})
```

Each descriptor specifies:

- **Command** — Cobra command metadata (name, description, examples)
- **ActionResolver** — Factory function to create the action via IoC
- **FlagsResolver** — Factory function to create the flags struct
- **OutputFormats** — Supported output formats (JSON, table, none)

### 2. CobraBuilder

At startup, the CobraBuilder walks the ActionDescriptor tree and generates standard Cobra commands. This decouples command definition from Cobra's API.

### 3. Middleware Pipeline

Before an action runs, the middleware chain processes cross-cutting concerns:

| Middleware | Purpose |
|---|---|
| Telemetry | Creates root span, records command events and attributes |
| Hooks | Executes pre/post lifecycle hooks from `azure.yaml` |
| Extensions | Routes events to registered extensions |
| Debug | Handles `--debug` flag behavior |

Middleware is registered in `cmd/middleware/` and composed as a chain around the action.

### 4. Action Execution

Actions implement the `actions.Action` interface:

```go
type Action interface {
    Run(ctx context.Context) (*ActionResult, error)
}
```

The `ActionResult` contains a `Message` (displayed to the user) and optional structured data used by output formatters (JSON, table). Actions receive dependencies via constructor injection through the IoC container.

### 5. Output Formatting

When a command supports `--output`, the framework formats the `ActionResult`:

- **JSON** — Serializes the result data as JSON
- **Table** — Renders the result data as a formatted table
- **None** — Displays only the result message

## Error Handling

- Actions return errors wrapped with `fmt.Errorf("context: %w", err)`
- `internal.ErrorWithSuggestion` adds user-facing fix suggestions
- The middleware pipeline catches and formats errors for display
- Context cancellations are handled gracefully

## Adding a New Command

See [Adding a New Command](../guides/adding-a-new-command.md) for a step-by-step guide.

## Detailed Reference

- [cli/azd/docs/style-guidelines/new-azd-command.md](../../cli/azd/docs/style-guidelines/new-azd-command.md) — Full implementation reference
- [Guiding Principles](../../cli/azd/docs/style-guidelines/guiding-principles.md) — Command design philosophy
