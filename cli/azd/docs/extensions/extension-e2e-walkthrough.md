# Extension End-to-End Walkthrough

Build a complete azd extension from scratch using the `azdext` SDK helpers. This
walkthrough creates a **resource-tagging** extension that:

1. Registers custom commands for managing Azure resource tags.
2. Exposes an MCP server so AI assistants can call the tagging tools.
3. Hooks into the `postprovision` lifecycle event to auto-tag resources.
4. Uses `MCPSecurityPolicy` to validate user-supplied URLs.

> **Prerequisites:**
>
> - Go ≥ 1.22
> - azd ≥ 1.23.7 (with extension SDK helpers from [#6856](https://github.com/Azure/azure-dev/pull/6856))
> - The `microsoft.azd.extensions` developer extension installed (`azd extension install microsoft.azd.extensions`)

---

## Table of Contents

- [Step 1: Scaffold the Extension](#step-1-scaffold-the-extension)
- [Step 2: Define the Root Command](#step-2-define-the-root-command)
- [Step 3: Add a Custom Command](#step-3-add-a-custom-command)
- [Step 4: Build an MCP Server with Tools](#step-4-build-an-mcp-server-with-tools)
- [Step 5: Register Lifecycle Event Handlers](#step-5-register-lifecycle-event-handlers)
- [Step 6: Wire It All Together](#step-6-wire-it-all-together)
- [Step 7: Build, Install, and Test](#step-7-build-install-and-test)
- [Project Structure Summary](#project-structure-summary)
- [What You Have Built](#what-you-have-built)

---

## Step 1: Scaffold the Extension

```bash
cd cli/azd/extensions
azd x init
```

Follow the prompts:

- **Name:** `contoso.azd.tagger`
- **Language:** Go
- **Capabilities:** `custom-commands`, `metadata`, `lifecycle-events`

This creates a directory with `extension.yaml`, `main.go`, build scripts, and a
`CHANGELOG.md`.

Edit `extension.yaml` to match:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/extension.schema.json
id: contoso.azd.tagger
namespace: tagger
displayName: Resource Tagger
description: Auto-tag Azure resources and expose tagging tools via MCP.
version: 0.1.0
capabilities:
  - custom-commands
  - metadata
  - lifecycle-events
```

---

## Step 2: Define the Root Command

Replace `main.go` with the SDK-helper entry point:

```go
package main

import (
    "github.com/azure/azure-dev/cli/azd/extensions/contoso.azd.tagger/internal/cmd"
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
    azdext.Run(cmd.NewRootCommand())
}
```

Create `internal/cmd/root.go`:

```go
package cmd

import (
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    "github.com/spf13/cobra"
)

const (
    extensionID = "contoso.azd.tagger"
    version     = "0.1.0"
)

func NewRootCommand() *cobra.Command {
    // NewExtensionRootCommand registers --debug, --no-prompt, --cwd,
    // -e/--environment, --output and sets up trace context automatically.
    rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
        Name:    "tagger",
        Version: version,
        Short:   "Manage Azure resource tags",
    })

    // Custom commands
    rootCmd.AddCommand(newTagCommand(extCtx))
    rootCmd.AddCommand(newMCPCommand(extCtx))

    // Standard lifecycle, metadata, and version commands
    rootCmd.AddCommand(azdext.NewListenCommand(configureListen))
    rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", extensionID, NewRootCommand))
    rootCmd.AddCommand(azdext.NewVersionCommand(extensionID, version, &extCtx.OutputFormat))

    return rootCmd
}
```

**What this gives you:**

- All azd global flags parsed and available via `extCtx`.
- OpenTelemetry trace context propagated from the parent azd process.
- gRPC access token injected into the command context.

---

## Step 3: Add a Custom Command

Create `internal/cmd/tag.go`:

```go
package cmd

import (
    "fmt"

    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    "github.com/spf13/cobra"
)

func newTagCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    var tagKey, tagValue string

    cmd := &cobra.Command{
        Use:   "tag",
        Short: "Apply a tag to resources in the current environment",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := extCtx.Context()

            // Create the azd gRPC client
            client, err := azdext.NewAzdClient()
            if err != nil {
                return fmt.Errorf("creating azd client: %w", err)
            }
            defer client.Close()

            // Use the ConfigHelper to read environment config
            configHelper, err := azdext.NewConfigHelper(client)
            if err != nil {
                return fmt.Errorf("creating config helper: %w", err)
            }

            // Read the resource group from env config
            rg, found, err := configHelper.GetEnvString(ctx, "AZURE_RESOURCE_GROUP")
            if err != nil {
                return fmt.Errorf("reading resource group: %w", err)
            }
            if !found {
                return &azdext.LocalError{
                    Message:    "no resource group configured",
                    Category:   azdext.LocalErrorCategoryValidation,
                    Suggestion: "Run 'azd provision' first to create Azure resources.",
                }
            }

            // Format-aware output
            output := azdext.NewOutput(azdext.OutputOptions{
                Format: azdext.OutputFormat(extCtx.OutputFormat),
            })

            // Log the operation
            logger := azdext.NewLogger("tagger")
            logger.Info("applying tag", "key", tagKey, "value", tagValue, "rg", rg)

            // ... tag application logic using Azure SDK ...

            output.Success("Tagged resources in %s: %s=%s", rg, tagKey, tagValue)
            return nil
        },
    }

    cmd.Flags().StringVar(&tagKey, "key", "", "Tag key (required)")
    cmd.Flags().StringVar(&tagValue, "value", "", "Tag value (required)")
    _ = cmd.MarkFlagRequired("key")
    _ = cmd.MarkFlagRequired("value")

    return cmd
}
```

**Key patterns demonstrated:**

- `extCtx.Context()` for a trace-aware, token-injected context.
- `ConfigHelper` for reading azd environment configuration.
- `LocalError` with category and suggestion for structured error reporting.
- `Output` for format-aware display (text or JSON).
- `Logger` for structured logging.

---

## Step 4: Build an MCP Server with Tools

Create `internal/cmd/mcp.go`:

```go
package cmd

import (
    "context"
    "fmt"
    "os"

    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    mcp "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
    "github.com/spf13/cobra"
)

func newMCPCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    return &cobra.Command{
        Use:   "mcp",
        Short: "Start the MCP server for AI-assisted tagging",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Build the MCP server with the fluent builder API
            mcpServer := azdext.NewMCPServerBuilder("tagger", "0.1.0").
                // Rate limit: max 10 concurrent, refill 2/sec
                WithRateLimit(10, 2.0).
                // Security policy to validate any user-provided URLs
                WithSecurityPolicy(azdext.DefaultMCPSecurityPolicy()).
                // System instructions for AI clients
                WithInstructions(`Use these tools to manage Azure resource tags.
Always confirm tag operations with the user before applying.`).
                // Register tools
                AddTool("list_tags", listTagsHandler, azdext.MCPToolOptions{
                    Description: "List tags on resources in a resource group",
                },
                    mcp.WithString("resourceGroup",
                        mcp.Required(),
                        mcp.Description("Azure resource group name"),
                    ),
                    mcp.WithString("subscription",
                        mcp.Description("Azure subscription ID (uses default if omitted)"),
                    ),
                ).
                AddTool("set_tag", setTagHandler, azdext.MCPToolOptions{
                    Description: "Set a tag on all resources in a resource group",
                },
                    mcp.WithString("resourceGroup",
                        mcp.Required(),
                        mcp.Description("Azure resource group name"),
                    ),
                    mcp.WithString("key",
                        mcp.Required(),
                        mcp.Description("Tag key"),
                    ),
                    mcp.WithString("value",
                        mcp.Required(),
                        mcp.Description("Tag value"),
                    ),
                ).
                Build()

            // Serve over stdio (standard MCP transport)
            sseServer := server.NewStdioServer(mcpServer)
            return sseServer.Listen(cmd.Context(), os.Stdin, os.Stdout)
        },
    }
}

// listTagsHandler demonstrates typed argument parsing and JSON result helpers.
func listTagsHandler(ctx context.Context, args azdext.ToolArgs) (*mcp.CallToolResult, error) {
    rg, err := args.RequireString("resourceGroup")
    if err != nil {
        return azdext.MCPErrorResult("missing argument: %v", err), nil
    }
    sub := args.OptionalString("subscription", "")

    logger := azdext.NewLogger("mcp.list_tags")
    logger.Info("listing tags", "resourceGroup", rg, "subscription", sub)

    // ... Azure SDK call to list tags ...
    tags := map[string]string{
        "environment": "production",
        "owner":       "platform-team",
    }

    return azdext.MCPJSONResult(tags), nil
}

// setTagHandler demonstrates security policy usage and error handling.
func setTagHandler(ctx context.Context, args azdext.ToolArgs) (*mcp.CallToolResult, error) {
    rg, err := args.RequireString("resourceGroup")
    if err != nil {
        return azdext.MCPErrorResult("missing argument: %v", err), nil
    }
    key, err := args.RequireString("key")
    if err != nil {
        return azdext.MCPErrorResult("missing argument: %v", err), nil
    }
    value, err := args.RequireString("value")
    if err != nil {
        return azdext.MCPErrorResult("missing argument: %v", err), nil
    }

    logger := azdext.NewLogger("mcp.set_tag")
    logger.Info("setting tag", "resourceGroup", rg, "key", key, "value", value)

    // ... Azure SDK call to set tag ...

    return azdext.MCPTextResult("Tag %s=%s applied to resource group %s", key, value, rg), nil
}
```

**Key patterns demonstrated:**

- `MCPServerBuilder` fluent API with rate limiting and security policy.
- `ToolArgs.RequireString` / `OptionalString` for typed argument access.
- `MCPTextResult`, `MCPJSONResult`, `MCPErrorResult` for response construction.
- `DefaultMCPSecurityPolicy` for SSRF protection.

---

## Step 5: Register Lifecycle Event Handlers

Create `internal/cmd/listen.go`:

```go
package cmd

import (
    "context"
    "fmt"

    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// configureListen is called by NewListenCommand to register event handlers.
func configureListen(host *azdext.ExtensionHost) {
    host.WithProjectEventHandler("postprovision", handlePostProvision)
}

func handlePostProvision(ctx context.Context, args *azdext.ProjectEventArgs) error {
    logger := azdext.NewLogger("tagger.postprovision")
    logger.Info("auto-tagging resources after provision", "project", args.Project.Name)

    client := args.Client // The host provides access to the azd gRPC client

    // Read environment values
    envResp, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{})
    if err != nil {
        logger.Warn("could not read environment", "error", err)
        return nil // Non-fatal: don't block provisioning
    }

    // Apply standard tags to all resources
    for key, value := range envResp.Values {
        logger.Debug("found env value", "key", key, "value", value)
    }

    logger.Info("auto-tagging complete")
    return nil
}
```

**Key patterns demonstrated:**

- `NewListenCommand` + configure callback for clean lifecycle registration.
- Project event handlers receive `ProjectEventArgs` with access to the project
  metadata and gRPC client.
- Non-fatal error handling — the handler logs warnings but doesn't block the
  parent azd workflow.

---

## Step 6: Wire It All Together

Your final `main.go`:

```go
package main

import (
    "github.com/azure/azure-dev/cli/azd/extensions/contoso.azd.tagger/internal/cmd"
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
    azdext.Run(cmd.NewRootCommand())
}
```

That is the **entire** entry point. `azdext.Run` handles:

- `FORCE_COLOR` detection
- Trace context propagation
- Access token injection
- Structured error reporting
- Exit code management

---

## Step 7: Build, Install, and Test

```bash
# Build for current platform
azd x build

# Build for all platforms
azd x build --all

# Test the custom command
azd tagger tag --key team --value platform -e dev

# Test the MCP server (connects over stdio)
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | azd tagger mcp

# Test lifecycle integration (azd invokes 'listen' automatically)
azd provision  # postprovision handler fires
```

---

## Project Structure Summary

```
contoso.azd.tagger/
├── main.go                      # Entry point: azdext.Run(cmd.NewRootCommand())
├── extension.yaml               # Extension manifest
├── CHANGELOG.md                 # Release notes
├── go.mod                       # Go module
├── go.sum
├── build.ps1                    # Windows build
├── build.sh                     # Unix build
└── internal/
    └── cmd/
        ├── root.go              # Root command + subcommand wiring
        ├── tag.go               # Custom 'tag' command
        ├── mcp.go               # MCP server with tools
        └── listen.go            # Lifecycle event handlers
```

---

## What You Have Built

| Capability | SDK Helper Used | Lines Saved |
|------------|----------------|-------------|
| Root command with global flags + tracing | `NewExtensionRootCommand` | ~40 lines |
| Entry point with error reporting | `Run` | ~15 lines |
| Listen command with host setup | `NewListenCommand` | ~20 lines |
| MCP server with rate limiting + security | `MCPServerBuilder` | ~60 lines |
| Typed MCP argument parsing | `ToolArgs` | ~10 lines per tool |
| MCP response construction | `MCPTextResult`, `MCPJSONResult`, `MCPErrorResult` | ~5 lines per tool |
| SSRF/path protection | `DefaultMCPSecurityPolicy` | ~50 lines |
| Metadata + version commands | `NewMetadataCommand`, `NewVersionCommand` | ~30 lines |
| **Total boilerplate eliminated** | | **~250+ lines** |

---

## See Also

- [Extension SDK Reference](./extension-sdk-reference.md) — Full API reference for all helpers.
- [Extension Migration Guide](./extension-migration-guide.md) — Migrate existing extensions from legacy patterns.
- [Extension Framework](./extension-framework.md) — General framework documentation.
- [Extension Framework Services](./extension-framework-services.md) — gRPC service reference.
- [Extension Style Guide](./extensions-style-guide.md) — Design guidelines.
