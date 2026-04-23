# Extension Migration Guide

This guide helps extension authors migrate from pre-[#6856](https://github.com/Azure/azure-dev/pull/6856) patterns to the new `azdext` SDK helpers. Each section shows a **before** (legacy) and **after** (recommended) pattern with a brief explanation of what changed and why.

> **Applies to:** Extensions targeting azd ≥ 1.23.7 with `azdext` SDK helpers.

---

## Table of Contents

- [M1: Entry Point — NewContext → Run](#m1-entry-point--newcontext--run)
- [M2: Root Command — Manual Flags → NewExtensionRootCommand](#m2-root-command--manual-flags--newextensionrootcommand)
- [M3: Listen Command — Manual Host Setup → NewListenCommand](#m3-listen-command--manual-host-setup--newlistencommand)
- [M4: MCP Server — Manual Construction → MCPServerBuilder](#m4-mcp-server--manual-construction--mcpserverbuilder)
- [M5: MCP Tool Arguments — Raw Map Access → ToolArgs](#m5-mcp-tool-arguments--raw-map-access--toolargs)
- [M6: MCP Responses — Manual Result Construction → Result Helpers](#m6-mcp-responses--manual-result-construction--result-helpers)
- [M7: SSRF / Path Validation — Custom Checks → MCPSecurityPolicy](#m7-ssrf--path-validation--custom-checks--mcpsecuritypolicy)
- [M8: Service Target — Full Interface → BaseServiceTargetProvider](#m8-service-target--full-interface--baseservicetargetprovider)
- [M9: Metadata Command — Hand-Rolled → NewMetadataCommand](#m9-metadata-command--hand-rolled--newmetadatacommand)
- [M10: Version Command — Custom → NewVersionCommand](#m10-version-command--custom--newversioncommand)
- [Compatibility Notes](#compatibility-notes)
- [Step-by-Step Migration Checklist](#step-by-step-migration-checklist)

---

## M1: Entry Point — NewContext → Run

### Before (legacy)

```go
func main() {
    ctx, err := azdext.NewContext()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }

    rootCmd := cmd.NewRootCommand()
    rootCmd.SetContext(ctx)
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

**Problems:** Manual error display, no structured error reporting to azd, no
`FORCE_COLOR` handling, exit codes not standardized.

### After (recommended)

```go
func main() {
    azdext.Run(cmd.NewRootCommand())
}
```

**What changed:** `Run` handles context creation, `FORCE_COLOR`, trace
propagation, access-token injection, structured error reporting via gRPC
`ReportError`, and `os.Exit`. One line replaces ~15 lines of boilerplate.

> **Note:** `NewContext()` is deprecated but remains available for backward
> compatibility. New extensions should not use it.

---

## M2: Root Command — Manual Flags → NewExtensionRootCommand

### Before (legacy)

```go
var (
    debug       bool
    noPrompt    bool
    cwd         string
    environment string
    output      string
)

func NewRootCommand() *cobra.Command {
    rootCmd := &cobra.Command{
        Use:   "my-extension",
        Short: "My extension",
        PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
            // Manual trace context extraction
            traceparent := os.Getenv("TRACEPARENT")
            tracestate := os.Getenv("TRACESTATE")
            if traceparent != "" {
                ctx := propagation.TraceContext{}.Extract(cmd.Context(),
                    propagation.MapCarrier{
                        "traceparent": traceparent,
                        "tracestate":  tracestate,
                    })
                cmd.SetContext(ctx)
            }
            // Manual access token
            cmd.SetContext(azdext.WithAccessToken(cmd.Context()))
            return nil
        },
    }

    rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Enable debug logging")
    rootCmd.PersistentFlags().BoolVar(&noPrompt, "no-prompt", false, "Disable prompts")
    rootCmd.PersistentFlags().StringVar(&cwd, "cwd", "", "Working directory")
    rootCmd.PersistentFlags().StringVarP(&environment, "environment", "e", "", "Environment")
    rootCmd.PersistentFlags().StringVar(&output, "output", "", "Output format")

    return rootCmd
}
```

**Problems:** 30-50 lines of identical boilerplate in every extension. Flag names
and trace-context extraction can drift from what azd expects.

### After (recommended)

```go
func NewRootCommand() *cobra.Command {
    rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
        Name:    "my-extension",
        Version: "1.0.0",
        Short:   "My extension",
    })

    // Use extCtx.Debug, extCtx.Cwd, etc. in subcommands
    rootCmd.AddCommand(newServeCommand(extCtx))

    return rootCmd
}
```

**What changed:** `NewExtensionRootCommand` registers all standard flags, reads
`AZD_*` env vars, and sets up trace context + access token in
`PersistentPreRunE`. The `ExtensionContext` struct provides typed access to the
parsed values.

---

## M3: Listen Command — Manual Host Setup → NewListenCommand

### Before (legacy)

```go
func newListenCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "listen",
        Short: "Starts the extension and listens for events.",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := azdext.WithAccessToken(cmd.Context())
            azdClient, err := azdext.NewAzdClient()
            if err != nil {
                return fmt.Errorf("failed to create azd client: %w", err)
            }
            defer azdClient.Close()

            host := azdext.NewExtensionHost(azdClient).
                WithServiceTarget("myhost", func() azdext.ServiceTargetProvider {
                    return &MyProvider{client: azdClient}
                })

            return host.Run(ctx)
        },
    }
}
```

### After (recommended)

```go
rootCmd.AddCommand(azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
    host.WithServiceTarget("myhost", func() azdext.ServiceTargetProvider {
        return &MyProvider{client: host.Client()}
    })
}))
```

**What changed:** `NewListenCommand` handles client creation, context injection,
and `defer Close()` internally. The `configure` callback receives the fully
initialized host.

---

## M4: MCP Server — Manual Construction → MCPServerBuilder

### Before (legacy)

```go
mcpServer := server.NewMCPServer("my-mcp", "1.0.0")

// Manual rate limiter setup
limiter := rate.NewLimiter(rate.Limit(2.0), 10)

// Manual tool registration with raw handler
mcpServer.AddTool(
    mcp.NewTool("list_items",
        mcp.WithDescription("List items"),
        mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
    ),
    func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // Manual rate limiting check
        if !limiter.Allow() {
            return &mcp.CallToolResult{
                IsError: true,
                Content: []mcp.Content{{Type: "text", Text: ptr("rate limited")}},
            }, nil
        }
        // Manual arg extraction
        args := req.Params.Arguments
        query, ok := args["query"].(string)
        if !ok {
            return nil, fmt.Errorf("missing required argument: query")
        }
        // ... handler logic ...
        return nil, nil
    },
)
```

### After (recommended)

```go
mcpServer := azdext.NewMCPServerBuilder("my-mcp", "1.0.0").
    WithRateLimit(10, 2.0).
    WithSecurityPolicy(azdext.DefaultMCPSecurityPolicy()).
    AddTool("list_items", listItemsHandler, azdext.MCPToolOptions{
        Description: "List items",
    },
        mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
    ).
    Build()

func listItemsHandler(ctx context.Context, args azdext.ToolArgs) (*mcp.CallToolResult, error) {
    query, err := args.RequireString("query")
    if err != nil {
        return azdext.MCPErrorResult("missing argument: %v", err), nil
    }
    // ... handler logic ...
    return azdext.MCPJSONResult(results), nil
}
```

**What changed:** The builder handles rate-limiter wiring, security policy
attachment, and argument parsing. Tool handlers receive `ToolArgs` instead of
raw `CallToolRequest`.

---

## M5: MCP Tool Arguments — Raw Map Access → ToolArgs

### Before (legacy)

```go
func handler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := req.Params.Arguments
    name, ok := args["name"].(string)
    if !ok {
        return nil, fmt.Errorf("missing name")
    }
    // JSON numbers are float64, manual conversion needed
    countRaw, ok := args["count"]
    count := 10 // default
    if ok {
        if f, ok := countRaw.(float64); ok {
            count = int(f)
        }
    }
    verbose := false
    if v, ok := args["verbose"].(bool); ok {
        verbose = v
    }
    // ...
}
```

### After (recommended)

```go
func handler(ctx context.Context, args azdext.ToolArgs) (*mcp.CallToolResult, error) {
    name, err := args.RequireString("name")
    if err != nil {
        return azdext.MCPErrorResult("%v", err), nil
    }
    count := args.OptionalInt("count", 10)
    verbose := args.OptionalBool("verbose", false)
    // ...
}
```

**What changed:** `ToolArgs` handles JSON `float64` → `int` conversion, type
checking, and defaults in a single method call. `Require*` methods return
errors; `Optional*` methods return defaults.

---

## M6: MCP Responses — Manual Result Construction → Result Helpers

### Before (legacy)

```go
// Text result
textPtr := func(s string) *string { return &s }
result := &mcp.CallToolResult{
    Content: []mcp.Content{{Type: "text", Text: textPtr("Success: 5 items found")}},
}

// JSON result
jsonBytes, err := json.Marshal(data)
if err != nil {
    return nil, err
}
result := &mcp.CallToolResult{
    Content: []mcp.Content{{Type: "text", Text: textPtr(string(jsonBytes))}},
}

// Error result
result := &mcp.CallToolResult{
    IsError: true,
    Content: []mcp.Content{{Type: "text", Text: textPtr("failed: " + err.Error())}},
}
```

### After (recommended)

```go
return azdext.MCPTextResult("Success: %d items found", 5), nil
return azdext.MCPJSONResult(data), nil
return azdext.MCPErrorResult("failed: %v", err), nil
```

**What changed:** One-liners replace 3-5 lines each. `MCPJSONResult` handles
marshal errors internally (returns an error result if marshaling fails).

---

## M7: SSRF / Path Validation — Custom Checks → MCPSecurityPolicy

### Before (legacy)

```go
func validateURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return err
    }
    // Check for metadata endpoints
    if u.Hostname() == "169.254.169.254" {
        return fmt.Errorf("metadata endpoint blocked")
    }
    // Check for private IPs - incomplete, easy to miss ranges
    ip := net.ParseIP(u.Hostname())
    if ip != nil && ip.IsPrivate() {
        return fmt.Errorf("private network blocked")
    }
    // Missing: CGNAT, IPv6 transition, cloud metadata variants, etc.
    return nil
}
```

### After (recommended)

```go
policy := azdext.DefaultMCPSecurityPolicy()

// Or build a custom policy:
policy := azdext.NewMCPSecurityPolicy().
    BlockMetadataEndpoints().
    BlockPrivateNetworks().
    RequireHTTPS().
    ValidatePathsWithinBase(projectDir)

if err := policy.CheckURL(userURL); err != nil {
    return azdext.MCPErrorResult("blocked: %v", err), nil
}
if err := policy.CheckPath(userPath); err != nil {
    return azdext.MCPErrorResult("blocked: %v", err), nil
}
```

**What changed:** `MCPSecurityPolicy` covers cloud metadata (AWS, GCP, Azure
IMDS), RFC 1918, CGNAT (RFC 6598), IPv6 transition mechanisms (6to4, Teredo,
NAT64), symlink resolution, and sensitive header redaction — areas that manual
checks commonly miss.

---

## M8: Service Target — Full Interface → BaseServiceTargetProvider

### Before (legacy)

```go
type MyProvider struct {
    client *azdext.AzdClient
}

// Must implement ALL 6 methods even if unused
func (p *MyProvider) Initialize(ctx context.Context, sc *azdext.ServiceConfig) error {
    return nil
}
func (p *MyProvider) Endpoints(ctx context.Context, sc *azdext.ServiceConfig,
    tr *azdext.TargetResource) ([]string, error) {
    return nil, nil
}
func (p *MyProvider) GetTargetResource(ctx context.Context, subId string,
    sc *azdext.ServiceConfig, defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
    return nil, nil
}
func (p *MyProvider) Package(ctx context.Context, sc *azdext.ServiceConfig,
    sctx *azdext.ServiceContext, progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
    return nil, nil
}
func (p *MyProvider) Publish(ctx context.Context, sc *azdext.ServiceConfig,
    sctx *azdext.ServiceContext, tr *azdext.TargetResource,
    opts *azdext.PublishOptions, progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
    return nil, nil
}
func (p *MyProvider) Deploy(ctx context.Context, sc *azdext.ServiceConfig,
    sctx *azdext.ServiceContext, tr *azdext.TargetResource, progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
    // Only method actually needed
    // ... deploy logic ...
    return &azdext.ServiceDeployResult{}, nil
}
```

### After (recommended)

```go
type MyProvider struct {
    azdext.BaseServiceTargetProvider // no-op defaults for all methods
    client *azdext.AzdClient
}

// Override only what you need
func (p *MyProvider) Deploy(ctx context.Context, sc *azdext.ServiceConfig,
    sctx *azdext.ServiceContext, tr *azdext.TargetResource, progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
    // ... deploy logic ...
    return &azdext.ServiceDeployResult{}, nil
}
```

**What changed:** Embed `BaseServiceTargetProvider` and override only the
methods you need. Eliminates ~40 lines of no-op stubs per provider.

---

## M9: Metadata Command — Hand-Rolled → NewMetadataCommand

### Before (legacy)

```go
func newMetadataCommand() *cobra.Command {
    return &cobra.Command{
        Use:    "metadata",
        Short:  "Generate extension metadata",
        Hidden: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            rootCmd := newRootCommand()
            metadata := azdext.GenerateExtensionMetadata("1.0", "my.extension", rootCmd)
            jsonBytes, _ := json.MarshalIndent(metadata, "", "  ")
            fmt.Println(string(jsonBytes))
            return nil
        },
    }
}
```

### After (recommended)

```go
rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "my.extension", newRootCommand))
```

---

## M10: Version Command — Custom → NewVersionCommand

### Before (legacy)

```go
func newVersionCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "version",
        Short: "Print version",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Printf("my.extension version %s\n", Version)
            return nil
        },
    }
}
```

### After (recommended)

```go
rootCmd.AddCommand(azdext.NewVersionCommand("my.extension", "1.0.0", &extCtx.OutputFormat))
```

Supports `--output json` automatically when the output-format pointer is
provided.

---

## Compatibility Notes

1. **`azdext.NewContext()` is deprecated** — it remains functional but should
   not be used in new code. Use `azdext.Run` + `NewExtensionRootCommand` instead.

2. **All migrations are additive** — existing extensions continue to compile
   and run without changes. You can migrate incrementally.

3. **Minimum azd version** — The SDK helpers ship in azd ≥ 1.23.7. If your
   extension must support older azd versions, guard imports behind a build tag
   or version check.

4. **`mcp-go` dependency** — MCP helpers wrap `mark3labs/mcp-go`. Your
   extension's `go.mod` must include this dependency. Run `go mod tidy` after
   migration.

---

## Step-by-Step Migration Checklist

Use this checklist to migrate an existing extension:

- [ ] **Replace entry point:** Change `main()` to use `azdext.Run(rootCmd)`.
- [ ] **Replace root command:** Use `NewExtensionRootCommand` instead of manual
      flag registration and trace-context extraction.
- [ ] **Replace listen command:** Use `NewListenCommand(configure)` instead of
      manual host setup.
- [ ] **Replace MCP server construction:** Use `MCPServerBuilder` with
      `AddTool`, `WithRateLimit`, `WithSecurityPolicy`.
- [ ] **Replace argument parsing:** Switch tool handlers to `MCPToolHandler`
      signature and use `ToolArgs` methods.
- [ ] **Replace result construction:** Use `MCPTextResult`, `MCPJSONResult`,
      `MCPErrorResult`.
- [ ] **Add security policy:** Use `DefaultMCPSecurityPolicy()` or build a
      custom policy.
- [ ] **Embed BaseServiceTargetProvider:** Remove no-op method stubs; embed
      the base struct.
- [ ] **Replace metadata/version commands:** Use `NewMetadataCommand` and
      `NewVersionCommand`.
- [ ] **Run `go mod tidy`** to pick up new dependencies.
- [ ] **Test:** Build and run the extension against azd ≥ 1.23.7.

---

## See Also

- [Extension SDK Reference](./extension-sdk-reference.md) — Full API reference.
- [Extension End-to-End Walkthrough](./extension-e2e-walkthrough.md) — Build a complete extension from scratch.
- [Extension Framework](./extension-framework.md) — General framework documentation.
