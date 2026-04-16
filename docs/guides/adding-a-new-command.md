# Adding a New Command

This guide walks through adding a new command to the Azure Developer CLI.

## Overview

azd uses an **ActionDescriptor** pattern to define commands. Each command consists of:

1. **ActionDescriptor** — Declares the command's metadata, flags, and output formats
2. **Action** — Implements `Run(ctx context.Context) (*ActionResult, error)`
3. **Flags struct** — Defines command-line flags with a `Bind()` method
4. **IoC registration** — Wires the action and flags into the dependency injection container

## Quick Start

### 1. Create the command file

Create `cli/azd/cmd/<command_name>.go`:

```go
package cmd

import (
    "context"

    "github.com/azure/azure-dev/cli/azd/cmd/actions"
    "github.com/azure/azure-dev/cli/azd/internal"
    "github.com/spf13/pflag"
)

type myCommandFlags struct {
    name string
}

func (f *myCommandFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
    local.StringVar(&f.name, "name", "", "Resource name")
}

type myCommandAction struct {
    flags *myCommandFlags
    svc   *SomeService
}

func newMyCommandAction(flags *myCommandFlags, svc *SomeService) actions.Action {
    return &myCommandAction{flags: flags, svc: svc}
}

func (a *myCommandAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    // Command logic here
    return &actions.ActionResult{
        Message: &actions.ResultMessage{Header: "Done!"},
    }, nil
}
```

### 2. Register the command

Add to the command tree in `cli/azd/cmd/root.go`:

```go
root.Add("mycommand", &actions.ActionDescriptorOptions{
    Command:        newMyCommandCmd(),
    ActionResolver: newMyCommandAction,
    FlagsResolver:  newMyCommandFlags,
})
```

### 3. Support output formats

If the command produces structured output, add format support:

```go
root.Add("mycommand", &actions.ActionDescriptorOptions{
    Command:        newMyCommandCmd(),
    ActionResolver: newMyCommandAction,
    FlagsResolver:  newMyCommandFlags,
    OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
})
```

### 4. Update snapshots

After adding the command, regenerate CLI snapshots:

```bash
UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'
```

## Design Principles

- **Verb-first structure:** Use simple verbs (`up`, `add`, `deploy`) with minimal nesting
- **Build on existing categories:** Extend existing command groups rather than creating new ones
- **Progressive disclosure:** Basic usage should be simple; advanced features discoverable when needed
- **Consistent parameters:** Reuse established flag patterns (`--subscription`, `--name`, `--output`)

## Detailed Reference

For the full implementation guide including middleware, telemetry integration, and advanced patterns, see:

- [cli/azd/docs/style-guidelines/new-azd-command.md](../../cli/azd/docs/style-guidelines/new-azd-command.md)
- [Guiding Principles](../../cli/azd/docs/style-guidelines/guiding-principles.md)
