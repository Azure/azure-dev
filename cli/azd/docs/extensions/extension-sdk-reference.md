# Extension SDK Reference

This document is the API reference for the `azdext` SDK helpers introduced in [PR #6856](https://github.com/Azure/azure-dev/pull/6856). These helpers eliminate boilerplate that every azd extension must otherwise implement manually, covering command scaffolding, MCP server construction, typed argument parsing, security policy, and service-target base implementations.

> **Package import:** `"github.com/azure/azure-dev/cli/azd/pkg/azdext"`

---

## Table of Contents

- [Entry Point & Lifecycle](#entry-point--lifecycle)
  - [Run](#run)
  - [RunOption / WithPreExecute](#runoption--withpreexecute)
- [Command Scaffolding](#command-scaffolding)
  - [NewExtensionRootCommand](#newextensionrootcommand)
  - [ExtensionCommandOptions](#extensioncommandoptions)
  - [ExtensionContext](#extensioncontext)
  - [NewListenCommand](#newlistencommand)
  - [NewMetadataCommand](#newmetadatacommand)
  - [NewVersionCommand](#newversioncommand)
- [MCP Server Builder](#mcp-server-builder)
  - [NewMCPServerBuilder](#newmcpserverbuilder)
  - [MCPServerBuilder Methods](#mcpserverbuilder-methods)
  - [MCPToolHandler](#mcptoolhandler)
  - [MCPToolOptions](#mcptooloptions)
- [Typed Argument Parsing](#typed-argument-parsing)
  - [ToolArgs](#toolargs)
  - [ParseToolArgs](#parsetoolargs)
- [MCP Result Helpers](#mcp-result-helpers)
  - [MCPTextResult](#mcptextresult)
  - [MCPJSONResult](#mcpjsonresult)
  - [MCPErrorResult](#mcperrorresult)
- [MCP Security Policy](#mcp-security-policy)
  - [NewMCPSecurityPolicy](#newmcpsecuritypolicy)
  - [DefaultMCPSecurityPolicy](#defaultmcpsecuritypolicy)
  - [MCPSecurityPolicy Methods](#mcpsecuritypolicy-methods)
- [Service Target Providers](#service-target-providers)
  - [ServiceTargetProvider Interface](#servicetargetprovider-interface)
  - [BaseServiceTargetProvider](#baseservicetargetprovider)
- [Extension Host](#extension-host)
  - [NewExtensionHost](#newextensionhost)
  - [ExtensionHost Methods](#extensionhost-methods)
- [Client & Utilities](#client--utilities)
  - [AzdClient](#azdclient)
  - [ConfigHelper](#confighelper)
  - [TokenProvider](#tokenprovider)
  - [Logger](#logger)
  - [Output](#output)
  - [Runtime Utilities](#runtime-utilities)
    - [Shell Helpers](#shell-helpers)
    - [Tool Discovery Helpers](#tool-discovery-helpers)
    - [Interactive/TUI Helpers](#interactivetui-helpers)
    - [Atomic File Helpers](#atomic-file-helpers)
- [Error Handling](#error-handling)
  - [LocalError](#localerror)
  - [ServiceError](#serviceerror)
  - [LocalErrorCategory](#localerrorcategory)

---

## Entry Point & Lifecycle

### Run

```go
func Run(rootCmd *cobra.Command, opts ...RunOption)
```

`Run` is the **recommended entry point** for all azd extensions. It handles the
full lifecycle that every extension needs:

1. Reads `FORCE_COLOR` environment variable and configures `color.NoColor`.
2. Silences cobra's built-in error output (extensions control error display).
3. Creates a context with OpenTelemetry trace propagation from `TRACEPARENT`/`TRACESTATE`.
4. Injects the gRPC access token via `WithAccessToken`.
5. Executes the cobra command tree.
6. On failure, reports the error to azd via gRPC `ReportError` for structured telemetry.
7. Displays the error and any suggestion text to stderr.
8. Calls `os.Exit(1)` on failure.

**Usage:**

```go
func main() {
    rootCmd := cmd.NewRootCommand()
    azdext.Run(rootCmd)
}
```

### RunOption / WithPreExecute

```go
type RunOption func(*runConfig)

func WithPreExecute(fn func(ctx context.Context, cmd *cobra.Command) error) RunOption
```

`WithPreExecute` registers a hook that runs **after** context creation but
**before** command execution. If the hook returns a non-nil error, `Run` prints
it and exits. This is useful for extensions that need special setup such as
dual-mode host detection or working-directory changes.

**Usage:**

```go
func main() {
    rootCmd := cmd.NewRootCommand()
    azdext.Run(rootCmd, azdext.WithPreExecute(func(ctx context.Context, cmd *cobra.Command) error {
        // Validate prerequisites
        if _, err := exec.LookPath("docker"); err != nil {
            return fmt.Errorf("docker is required: %w", err)
        }
        return nil
    }))
}
```

---

## Command Scaffolding

### NewExtensionRootCommand

```go
func NewExtensionRootCommand(opts ExtensionCommandOptions) (*cobra.Command, *ExtensionContext)
```

Creates a root `cobra.Command` pre-configured for azd extensions. It
automatically:

- Registers azd's global flags (`--debug`, `--no-prompt`, `--cwd`,
  `-e`/`--environment`, `--output`).
- Reads `AZD_*` environment variables set by the azd framework.
- Sets up OpenTelemetry trace context from `TRACEPARENT`/`TRACESTATE` env vars.
- Calls `WithAccessToken()` on the command context.

The returned command has `PersistentPreRunE` configured to populate the
`ExtensionContext` before any subcommand runs.

**Usage:**

```go
rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
    Name:    "my-extension",
    Version: "1.0.0",
    Short:   "My custom azd extension",
})

// Add subcommands
rootCmd.AddCommand(newServeCommand(extCtx))

azdext.Run(rootCmd)
```

### ExtensionCommandOptions

```go
type ExtensionCommandOptions struct {
    Name    string // Extension name (used as cobra Use field)
    Version string // Extension version
    Use     string // Overrides the default Use string (defaults to Name)
    Short   string // Short description
    Long    string // Long description
}
```

### ExtensionContext

```go
type ExtensionContext struct {
    Debug        bool   // --debug flag value
    NoPrompt     bool   // --no-prompt flag value
    Cwd          string // --cwd flag value
    Environment  string // -e/--environment flag value
    OutputFormat string // --output flag value
}

func (ec *ExtensionContext) Context() context.Context
```

`Context()` returns a `context.Context` with the tracing span and access token
already injected. Use this context for all downstream calls (gRPC, HTTP,
Azure SDK).

### NewListenCommand

```go
func NewListenCommand(configure func(host *ExtensionHost)) *cobra.Command
```

Creates the standard `listen` command for lifecycle-event extensions. The
`configure` callback receives an `ExtensionHost` to register service targets,
framework services, and event handlers before the host starts its gRPC listener.

If `configure` is nil, the host runs with no custom registrations.

**Usage:**

```go
rootCmd.AddCommand(azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
    host.WithServiceTarget("myhost", func() azdext.ServiceTargetProvider {
        return &MyProvider{}
    })
    host.WithProjectEventHandler("preprovision", myHandler)
}))
```

### NewMetadataCommand

```go
func NewMetadataCommand(
    schemaVersion, extensionId string,
    rootCmdProvider func() *cobra.Command,
) *cobra.Command
```

Creates the standard hidden `metadata` command that outputs extension command
metadata for IntelliSense/discovery. `rootCmdProvider` returns the root command
to introspect.

### NewVersionCommand

```go
func NewVersionCommand(extensionId, version string, outputFormat *string) *cobra.Command
```

Creates the standard `version` command. Pass a pointer to the output-format
string so JSON output is supported when `--output json` is used.

---

## MCP Server Builder

### NewMCPServerBuilder

```go
func NewMCPServerBuilder(name, version string) *MCPServerBuilder
```

Creates a new builder for an MCP (Model Context Protocol) server. The builder
provides a fluent API to configure tools, resources, rate limiting, security
policies, and instructions.

### MCPServerBuilder Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `WithRateLimit` | `(burst int, refillRate float64) *MCPServerBuilder` | Configure a token-bucket rate limiter. `burst` = max concurrent requests; `refillRate` = tokens/second. |
| `WithSecurityPolicy` | `(policy *MCPSecurityPolicy) *MCPServerBuilder` | Attach a security policy for URL/path validation on tool calls. |
| `WithInstructions` | `(instructions string) *MCPServerBuilder` | Set system instructions that guide AI clients on how to use the server's tools. |
| `WithResourceCapabilities` | `(subscribe, listChanged bool) *MCPServerBuilder` | Enable resource support. |
| `WithPromptCapabilities` | `(listChanged bool) *MCPServerBuilder` | Enable prompt support. |
| `WithServerOption` | `(opt server.ServerOption) *MCPServerBuilder` | Add a raw `mcp-go` server option for capabilities not directly exposed by the builder. |
| `AddTool` | `(name string, handler MCPToolHandler, opts MCPToolOptions, params ...mcp.ToolOption) *MCPServerBuilder` | Register a tool with the server. The handler receives parsed `ToolArgs` (not raw `mcp.CallToolRequest`). |
| `AddResources` | `(resources ...server.ServerResource) *MCPServerBuilder` | Register static resources. |
| `Build` | `() *server.MCPServer` | Create the configured MCP server. |
| `SecurityPolicy` | `() *MCPSecurityPolicy` | Return the configured security policy, or `nil`. |

**Usage:**

```go
mcpServer := azdext.NewMCPServerBuilder("my-ext", "1.0.0").
    WithRateLimit(10, 2.0).
    WithSecurityPolicy(azdext.DefaultMCPSecurityPolicy()).
    WithInstructions("Use these tools to manage Azure resources.").
    AddTool("list_resources", listHandler, azdext.MCPToolOptions{
        Description: "List Azure resources in a resource group",
    },
        mcp.WithString("resourceGroup", mcp.Required(), mcp.Description("Resource group name")),
        mcp.WithString("subscription", mcp.Description("Subscription ID")),
    ).
    Build()
```

### MCPToolHandler

```go
type MCPToolHandler func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error)
```

Handler function for MCP tools. The `args` parameter provides typed access to
tool arguments (see [ToolArgs](#toolargs)).

### MCPToolOptions

```go
type MCPToolOptions struct {
    Description string // Human-readable tool description
}
```

---

## Typed Argument Parsing

### ToolArgs

Wraps parsed MCP tool arguments for typed, safe access. JSON numbers from MCP
requests arrive as `float64`; the `RequireInt`/`OptionalInt` methods handle
conversion automatically.

| Method | Signature | Description |
|--------|-----------|-------------|
| `RequireString` | `(key string) (string, error)` | Returns a string or error if missing/wrong type. |
| `OptionalString` | `(key, defaultValue string) string` | Returns a string or the default. |
| `RequireInt` | `(key string) (int, error)` | Returns an int or error if missing/wrong type. |
| `OptionalInt` | `(key string, defaultValue int) int` | Returns an int or the default. |
| `OptionalBool` | `(key string, defaultValue bool) bool` | Returns a bool or the default. |
| `OptionalFloat` | `(key string, defaultValue float64) float64` | Returns a float64 or the default. |
| `Has` | `(key string) bool` | True if the key exists in the arguments. |
| `Raw` | `() map[string]interface{}` | Returns the underlying argument map. |

### ParseToolArgs

```go
func ParseToolArgs(request mcp.CallToolRequest) ToolArgs
```

Extracts the arguments map from an MCP `CallToolRequest`.

**Usage:**

```go
func listHandler(ctx context.Context, args azdext.ToolArgs) (*mcp.CallToolResult, error) {
    rg, err := args.RequireString("resourceGroup")
    if err != nil {
        return azdext.MCPErrorResult("missing required argument: %v", err), nil
    }
    sub := args.OptionalString("subscription", "")
    limit := args.OptionalInt("limit", 50)

    // ... perform operation ...

    return azdext.MCPJSONResult(results), nil
}
```

---

## MCP Result Helpers

### MCPTextResult

```go
func MCPTextResult(format string, args ...interface{}) *mcp.CallToolResult
```

Creates a text-content `CallToolResult` using `fmt.Sprintf` formatting.

### MCPJSONResult

```go
func MCPJSONResult(data interface{}) *mcp.CallToolResult
```

Marshals `data` to JSON and creates a text-content `CallToolResult`. Returns an
error result if marshaling fails.

### MCPErrorResult

```go
func MCPErrorResult(format string, args ...interface{}) *mcp.CallToolResult
```

Creates an error `CallToolResult` with `IsError` set to `true`.

---

## MCP Security Policy

The `MCPSecurityPolicy` validates URLs and file paths used by MCP tool calls to
prevent SSRF, directory traversal, and data exfiltration.

### NewMCPSecurityPolicy

```go
func NewMCPSecurityPolicy() *MCPSecurityPolicy
```

Creates an empty security policy. Chain methods to build up the desired rules.

### DefaultMCPSecurityPolicy

```go
func DefaultMCPSecurityPolicy() *MCPSecurityPolicy
```

Returns a policy with recommended defaults:

- Cloud metadata endpoints blocked (AWS, GCP, Azure IMDS).
- RFC 1918 private networks blocked.
- HTTPS required (except localhost/127.0.0.1).
- Common sensitive headers redacted (`Authorization`, `Cookie`, `X-Api-Key`, etc.).

### MCPSecurityPolicy Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `BlockMetadataEndpoints` | `() *MCPSecurityPolicy` | Block cloud metadata service endpoints (`169.254.169.254`, `fd00:ec2::254`, `metadata.google.internal`, etc.). |
| `BlockPrivateNetworks` | `() *MCPSecurityPolicy` | Block RFC 1918 private networks, loopback, link-local, CGNAT (RFC 6598), and deprecated IPv6 transition mechanisms. |
| `RequireHTTPS` | `() *MCPSecurityPolicy` | Require HTTPS for all URLs except `localhost`/`127.0.0.1`. |
| `RedactHeaders` | `(headers ...string) *MCPSecurityPolicy` | Mark headers that should be blocked/redacted in outgoing requests. |
| `ValidatePathsWithinBase` | `(basePaths ...string) *MCPSecurityPolicy` | Restrict file paths to the given base directories. Resolves symlinks and blocks `../` traversal. |
| `CheckURL` | `(rawURL string) error` | Validate a URL against the policy. Returns `nil` if allowed. |
| `CheckPath` | `(path string) error` | Validate a file path against the policy. |
| `IsHeaderBlocked` | `(header string) bool` | Check if a header name is in the redacted set. |

**Usage:**

```go
policy := azdext.NewMCPSecurityPolicy().
    BlockMetadataEndpoints().
    BlockPrivateNetworks().
    RequireHTTPS().
    RedactHeaders("Authorization", "X-Custom-Secret").
    ValidatePathsWithinBase("/home/user/project")

if err := policy.CheckURL(userProvidedURL); err != nil {
    return azdext.MCPErrorResult("blocked URL: %v", err), nil
}
```

---

## Service Target Providers

### ServiceTargetProvider Interface

```go
type ServiceTargetProvider interface {
    Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
    Endpoints(ctx context.Context, serviceConfig *ServiceConfig, targetResource *TargetResource) ([]string, error)
    GetTargetResource(ctx context.Context, subscriptionId string, serviceConfig *ServiceConfig, defaultResolver func() (*TargetResource, error)) (*TargetResource, error)
    Package(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, progress ProgressReporter) (*ServicePackageResult, error)
    Publish(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, targetResource *TargetResource, publishOptions *PublishOptions, progress ProgressReporter) (*ServicePublishResult, error)
    Deploy(ctx context.Context, serviceConfig *ServiceConfig, serviceContext *ServiceContext, targetResource *TargetResource, progress ProgressReporter) (*ServiceDeployResult, error)
}
```

### BaseServiceTargetProvider

```go
type BaseServiceTargetProvider struct{}
```

Provides **no-op default implementations** for all `ServiceTargetProvider`
methods. Extensions should embed this struct and override only the methods they
need.

**Usage:**

```go
type MyProvider struct {
    azdext.BaseServiceTargetProvider // embed defaults
    client *azdext.AzdClient
}

// Override only what you need
func (p *MyProvider) Package(ctx context.Context, sc *azdext.ServiceConfig,
    sctx *azdext.ServiceContext, progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
    progress.Report("Packaging...")
    // custom packaging logic
    return &azdext.ServicePackageResult{PackagePath: "/out/app.tar.gz"}, nil
}
```

---

## Extension Host

### NewExtensionHost

```go
func NewExtensionHost(client *AzdClient) *ExtensionHost
```

Creates an `ExtensionHost` that manages service targets, framework services,
and event handlers. The host starts a gRPC listener and blocks until azd shuts
down the connection.

### ExtensionHost Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `Client` | `() *AzdClient` | Returns the underlying gRPC client. |
| `WithServiceTarget` | `(host string, factory ServiceTargetFactory) *ExtensionHost` | Register a custom deployment target. |
| `WithFrameworkService` | `(language string, factory FrameworkServiceFactory) *ExtensionHost` | Register a custom language/framework build service. |
| `WithProjectEventHandler` | `(eventName string, handler ProjectEventHandler) *ExtensionHost` | Register a project-level lifecycle event handler. |
| `WithServiceEventHandler` | `(eventName string, handler ServiceEventHandler, options *ServiceEventOptions) *ExtensionHost` | Register a service-level lifecycle event handler (with optional filtering). |
| `Run` | `(ctx context.Context) error` | Start the host and block until shutdown. |

---

## Client & Utilities

### AzdClient

```go
func NewAzdClient(opts ...AzdClientOption) (*AzdClient, error)
```

gRPC client connecting to the azd framework. Auto-discovers the socket via
`AZD_RPC_SERVER_ENDPOINT`. Provides typed accessors for all framework services:

| Accessor | Returns |
|----------|---------|
| `Project()` | `ProjectServiceClient` |
| `Environment()` | `EnvironmentServiceClient` |
| `UserConfig()` | `UserConfigServiceClient` |
| `Prompt()` | `PromptServiceClient` |
| `Deployment()` | `DeploymentServiceClient` |
| `Events()` | `EventServiceClient` |
| `Compose()` | `ComposeServiceClient` |
| `Workflow()` | `WorkflowServiceClient` |
| `ServiceTarget()` | `ServiceTargetServiceClient` |
| `FrameworkService()` | `FrameworkServiceClient` |
| `Container()` | `ContainerServiceClient` |
| `Extension()` | `ExtensionServiceClient` |
| `Account()` | `AccountServiceClient` |
| `Ai()` | `AiModelServiceClient` |

Always call `defer client.Close()` after creation.

### ConfigHelper

```go
func NewConfigHelper(client *AzdClient) (*ConfigHelper, error)
```

Provides read/write access to azd user and environment configuration:

| Method | Description |
|--------|-------------|
| `GetUserString(ctx, path)` | Read a string from user config. |
| `GetUserJSON(ctx, path, out)` | Unmarshal user config into a struct. |
| `SetUserJSON(ctx, path, value)` | Write a value to user config. |
| `UnsetUser(ctx, path)` | Remove a user config key. |
| `GetEnvString(ctx, path)` | Read a string from env config. |
| `GetEnvJSON(ctx, path, out)` | Unmarshal env config into a struct. |
| `SetEnvJSON(ctx, path, value)` | Write a value to env config. |
| `UnsetEnv(ctx, path)` | Remove an env config key. |

Utility functions:

- `MergeJSON(base, override)` — Shallow-merge two JSON maps.
- `DeepMergeJSON(base, override)` — Deep recursive merge.
- `ValidateConfig(path, data, validators...)` — Validate config data.
- `RequiredKeys(keys...)` — Returns a `ConfigValidator` that checks for required keys.

### TokenProvider

```go
func NewTokenProvider(ctx context.Context, client *AzdClient, opts *TokenProviderOptions) (*TokenProvider, error)
```

Obtains Azure access tokens for authenticated API calls. Implements
`azcore.TokenCredential` semantics.

| Method | Description |
|--------|-------------|
| `GetToken(ctx, options)` | Returns an `azcore.AccessToken`. |
| `TenantID()` | Returns the resolved tenant ID. |

### Logger

```go
func NewLogger(component string, opts ...LoggerOptions) *Logger
```

Structured logging with component tagging, backed by `slog`:

| Method | Description |
|--------|-------------|
| `Debug(msg, args...)` | Log at DEBUG level. |
| `Info(msg, args...)` | Log at INFO level. |
| `Warn(msg, args...)` | Log at WARN level. |
| `Error(msg, args...)` | Log at ERROR level. |
| `With(args...)` | Create a child logger with additional fields. |
| `WithComponent(name)` | Create a child logger for a sub-component. |
| `WithOperation(name)` | Create a child logger tagged with an operation name. |
| `Slogger()` | Return the underlying `*slog.Logger`. |

Call `azdext.SetupLogging(LoggerOptions{Debug: true})` during initialization to
configure the global log level.

### Output

```go
func NewOutput(opts OutputOptions) *Output
```

Format-aware output (text or JSON):

| Method | Description |
|--------|-------------|
| `IsJSON()` | True if output format is JSON. |
| `Success(fmt, args...)` | Print a success message (green). |
| `Warning(fmt, args...)` | Print a warning (yellow). |
| `Error(fmt, args...)` | Print an error (red). |
| `Info(fmt, args...)` | Print informational text. |
| `Message(fmt, args...)` | Print plain text. |
| `JSON(data)` | Marshal and print JSON. |
| `Table(headers, rows)` | Print a formatted table. |

### Runtime Utilities

These helpers are intended to remove common extension boilerplate for shell execution, tool checks, TTY detection, and safe file writes.

#### Shell Helpers

| API | Description |
|-----|-------------|
| `DetectShell()` | Detects the current shell using `SHELL`, `PSModulePath`, `ComSpec`, then platform defaults. |
| `ShellCommand(ctx, script)` | Builds an `exec.Cmd` using detected shell conventions (`cmd /C`, `pwsh -Command`, `<shell> -c`). |
| `ShellCommandWith(ctx, info, script)` | Same as `ShellCommand` but uses explicit `ShellInfo` for deterministic behavior/testing. |
| `IsInteractiveTerminal(f)` / `IsStdinTerminal()` / `IsStdoutTerminal()` | Terminal detection helpers. |

#### Tool Discovery Helpers

| API | Description |
|-----|-------------|
| `LookupTool(name)` | Looks up tools on `PATH` and also checks the current project directory for local wrappers (for example `./mvnw`). |
| `LookupTools(names...)` | Batch lookup for multiple tools. |
| `RequireTools(names...)` | Returns a typed error when required tools are missing. |
| `PrependPATH` / `AppendPATH` / `PATHContains` | Cross-platform `PATH` mutation and detection helpers. |

#### Interactive/TUI Helpers

| API | Description |
|-----|-------------|
| `DetectInteractive()` | Detects TTY mode (`full` / `limited` / `none`), `AZD_NO_PROMPT`, CI, and known agent environments. |
| `InteractiveInfo.CanPrompt()` | Safe prompt gate (`stdin/stdout tty`, not no-prompt, not CI, not agent). |
| `InteractiveInfo.CanColorize()` | Color output gate honoring `FORCE_COLOR` and `NO_COLOR`. |

#### Atomic File Helpers

| API | Description |
|-----|-------------|
| `WriteFileAtomic(path, data, perm)` | Writes via temp-file + atomic rename, with Windows rename retry behavior. |
| `CopyFileAtomic(src, dst, perm)` | Atomic copy via `WriteFileAtomic`. |
| `BackupFile(path, suffix)` | Creates an atomic backup file (`.bak` by default). |
| `EnsureDir(dir, perm)` | Convenience wrapper around `os.MkdirAll` with extension-prefixed errors. |

---

## Error Handling

### LocalError

```go
type LocalError struct {
    Message    string
    Code       string
    Category   LocalErrorCategory
    Suggestion string
}
```

Represents an error originating within the extension. The `Suggestion` field
provides actionable guidance displayed to the user.

### ServiceError

```go
type ServiceError struct {
    Message     string
    ErrorCode   string
    StatusCode  int
    ServiceName string
    Suggestion  string
}
```

Represents an error from an Azure service call.

### LocalErrorCategory

```go
type LocalErrorCategory string

const (
    LocalErrorCategoryValidation    LocalErrorCategory = "validation"
    LocalErrorCategoryAuth          LocalErrorCategory = "auth"
    LocalErrorCategoryDependency    LocalErrorCategory = "dependency"
    LocalErrorCategoryCompatibility LocalErrorCategory = "compatibility"
    LocalErrorCategoryUser          LocalErrorCategory = "user"
    LocalErrorCategoryInternal      LocalErrorCategory = "internal"
    LocalErrorCategoryLocal         LocalErrorCategory = "local"
)
```

Error categories enable structured telemetry classification and targeted error
guidance. Use `WrapError(err)` to convert a `LocalError` or `ServiceError` to
the gRPC `ExtensionError` proto for reporting.

---

## See Also

- [Extension Framework](./extension-framework.md) — Getting started, managing extensions, developing extensions.
- [Extension Migration Guide](./extension-migration-guide.md) — Migrate from pre-#6856 patterns to new SDK helpers.
- [Extension End-to-End Walkthrough](./extension-e2e-walkthrough.md) — Build a complete extension from scratch.
- [Extension Framework Services](./extension-framework-services.md) — gRPC service reference for custom language frameworks.
- [Extension Style Guide](./extensions-style-guide.md) — Design guidelines and best practices.
