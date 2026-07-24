## Reference: Capabilities & the `azdext` SDK (Go)

The `azdext` SDK (`github.com/azure/azure-dev/cli/azd/pkg/azdext`) removes boilerplate. For the
full API, read `extension-sdk-reference.md` (see the doc map). Other languages (.NET, JS, Python)
use generated gRPC clients from the scaffold; the concepts below are language-agnostic even though
the examples are Go.

### Entry point

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

`azdext.Run` handles color detection, trace-context propagation, gRPC access-token injection,
structured error reporting, and exit codes.

### Root command with global flags

`NewExtensionRootCommand` registers azd's reserved global flags (`--debug`, `--no-prompt`,
`-C/--cwd`, `-e/--environment`, `-o/--output`) and returns an `ExtensionContext`.

```go
func NewRootCommand() *cobra.Command {
    rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
        Name:  "tagger",
        Short: "Manage Azure resource tags",
    })

    rootCmd.AddCommand(newTagCommand(extCtx))                  // custom-commands
    rootCmd.AddCommand(azdext.NewListenCommand(configureListen)) // lifecycle-events
    rootCmd.AddCommand(newMCPCommand(extCtx))                  // mcp-server
    rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", extensionID, NewRootCommand)) // metadata
    rootCmd.AddCommand(azdext.NewVersionCommand(extensionID, version, &extCtx.OutputFormat))
    return rootCmd
}
```

> Do **not** redefine reserved global flags (`--debug`, `--no-prompt`, `--cwd`, `--environment`,
> `--output`) — they are inherited. See the style guide's "Reserved Global Flags".

### Talking to azd (gRPC services)

```go
client, err := azdext.NewAzdClient()
defer client.Close()
// Services: client.Environment(), client.Project(), client.UserConfig(),
// client.Prompt(), client.Compose(), client.Deployment(), client.Events(), ...
```

`ConfigHelper` simplifies reading env/user config; `azdext.WithAccessToken(ctx)` injects the token.

### custom-commands

A plain Cobra command registered on the root. Read env via the gRPC client / `ConfigHelper`, emit
format-aware output with `azdext.NewOutput`, and return structured errors with `azdext.LocalError`
(fields: `Message`, `Category`, `Suggestion`).

### lifecycle-events

Register handlers with a configure callback passed to `NewListenCommand`:

```go
func configureListen(host *azdext.ExtensionHost) {
    host.WithProjectEventHandler("postprovision", handlePostProvision)
    // host.WithServiceEventHandler("predeploy", ...)
}
```

Handlers receive args (project/service metadata + `Client`). Keep them **non-fatal**: log warnings
rather than blocking the parent azd workflow unless the failure is truly fatal. `azd` invokes the
extension's `listen` command automatically during the relevant lifecycle phase.

### mcp-server

Build with the fluent `MCPServerBuilder`, then serve over stdio:

```go
mcpServer := azdext.NewMCPServerBuilder("tagger", version).
    WithRateLimit(10, 2.0).
    WithSecurityPolicy(azdext.DefaultMCPSecurityPolicy()). // SSRF/path protection
    WithInstructions("Use these tools to manage Azure resource tags.").
    AddTool("set_tag", setTagHandler, azdext.MCPToolOptions{Description: "Set a tag"},
        mcp.WithString("resourceGroup", mcp.Required(), mcp.Description("RG name")),
        mcp.WithString("key", mcp.Required()), mcp.WithString("value", mcp.Required()),
    ).
    Build()
return server.NewStdioServer(mcpServer).Listen(cmd.Context(), os.Stdin, os.Stdout)
```

Handlers use `azdext.ToolArgs` (`RequireString` / `OptionalString`) and return
`MCPTextResult` / `MCPJSONResult` / `MCPErrorResult`. Also add an `mcp` section to
`extension.yaml`. This is what makes an extension usable by AI agents.

### Provider capabilities (advanced)

- **service-target-provider** — embed `azdext.BaseServiceTargetProvider` and implement deploy hooks;
  add a `providers:` entry of type `service-target`.
- **framework-service-provider** — implement the `FrameworkServiceProvider` interface (Restore /
  Build / Package). See `extension-framework-services.md` for a full Rust example.
- **provisioning-provider** — custom infrastructure provisioning; add a `providers:` entry.
- **validation-provider** — local pre-provision checks dispatched by core azd's Bicep provider.

Model any provider extension after `microsoft.azd.demo`, which implements all of them.

### Error handling & style

Follow `extensions-style-guide.md`: use structured error types (`LocalError` with category +
suggestion), correct error-chain precedence, and consistent flag naming. In first-party Go
extensions, use `fmt.Print*` for user-facing stdout and `log.Print*` only for hidden stderr debug
diagnostics.
