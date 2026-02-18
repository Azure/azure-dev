# Azure AI Models Extension - Development Guide

This guide provides best practices for developing and extending the `azure.ai.models` extension. It is designed to be used by developers and AI coding assistants (like GitHub Copilot) to ensure consistent, high-quality code.

---

## Project Structure

```
azure.ai.models/
├── main.go                    # Extension entrypoint
├── extension.yaml             # Extension manifest
├── version.txt                # Version stamp
├── internal/
│   ├── cmd/                   # Cobra commands
│   │   ├── root.go           # Root command + persistent flags
│   │   ├── init.go           # Initialize project
│   │   ├── custom.go         # Custom model command group
│   │   ├── custom_create.go  # Create/upload model
│   │   ├── custom_list.go    # List models
│   │   ├── custom_show.go    # Show model details
│   │   └── custom_delete.go  # Delete model
│   ├── client/               # HTTP client for Foundry APIs
│   │   └── foundry_client.go
│   ├── azcopy/               # AzCopy runner and installer
│   │   ├── runner.go
│   │   └── installer.go
│   └── utils/                # Shared utilities
│       └── output.go         # Table/JSON output formatting
└── pkg/
    └── models/               # Data models (structs)
```

---

## Code Patterns

### 1. Creating a New Command

```go
package cmd

import (
    "context"
    "errors"
    "fmt"

    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    "github.com/spf13/cobra"
)

type myCommandFlags struct {
    Name   string
    Output string
}

func newMyCommand(parentFlags *parentFlags) *cobra.Command {
    flags := &myCommandFlags{}

    cmd := &cobra.Command{
        Use:   "mycommand",
        Short: "Brief description",
        Long:  "Detailed description of what the command does.",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := azdext.WithAccessToken(cmd.Context())
            return runMyCommand(ctx, parentFlags, flags)
        },
    }

    // Define flags
    cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Name (required)")
    cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")
    
    _ = cmd.MarkFlagRequired("name")

    return cmd
}

func runMyCommand(ctx context.Context, parentFlags *parentFlags, flags *myCommandFlags) error {
    // 1. Create azd client
    azdClient, err := azdext.NewAzdClient()
    if err != nil {
        return fmt.Errorf("failed to create azd client: %w", err)
    }
    defer azdClient.Close()

    // 2. Wait for debugger (optional, for development)
    if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
        if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
            return nil
        }
        return fmt.Errorf("failed waiting for debugger: %w", err)
    }

    // 3. Create credential
    credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
        AdditionallyAllowedTenants: []string{"*"},
    })
    if err != nil {
        return fmt.Errorf("failed to create Azure credential: %w", err)
    }

    // 4. Create Foundry client
    foundryClient, err := client.NewFoundryClient(parentFlags.projectEndpoint, credential)
    if err != nil {
        return err
    }

    // 5. Execute business logic
    result, err := foundryClient.SomeOperation(ctx)
    if err != nil {
        return err
    }

    // 6. Output results - ALWAYS handle errors from PrintObject
    if err := utils.PrintObject(result, utils.OutputFormat(flags.Output)); err != nil {
        return err
    }

    return nil
}
```

### 2. HTTP Client Pattern

```go
package client

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "strings"
    "time"

    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type MyClient struct {
    baseURL    string
    credential azcore.TokenCredential
    httpClient *http.Client
}

func NewMyClient(endpoint string, credential azcore.TokenCredential) (*MyClient, error) {
    parsedURL, err := url.Parse(endpoint)
    if err != nil {
        return nil, fmt.Errorf("invalid endpoint URL: %w", err)
    }

    // SECURITY: Enforce HTTPS
    if !strings.EqualFold(parsedURL.Scheme, "https") {
        return nil, fmt.Errorf("invalid endpoint URL: scheme must be https")
    }

    // SECURITY: Reject embedded credentials
    if parsedURL.User != nil {
        return nil, fmt.Errorf("invalid endpoint URL: userinfo is not allowed")
    }

    // Validate URL structure strictly
    pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
    if len(pathParts) != 3 || pathParts[0] != "api" {
        return nil, fmt.Errorf("invalid endpoint URL format")
    }

    return &MyClient{
        baseURL:    endpoint,
        credential: credential,
        // IMPORTANT: Always set timeout
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }, nil
}

func (c *MyClient) DoRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
    reqURL := fmt.Sprintf("%s%s", c.baseURL, path)

    var reqBody io.Reader
    if body != nil {
        data, err := json.Marshal(body)
        if err != nil {
            return nil, fmt.Errorf("failed to marshal request: %w", err)
        }
        // Use bytes.NewReader, NOT strings.NewReader(string(data))
        reqBody = bytes.NewReader(data)
    }

    req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    // Add auth header
    if err := c.addAuth(ctx, req); err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")

    return c.httpClient.Do(req)
}

func (c *MyClient) addAuth(ctx context.Context, req *http.Request) error {
    token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
        Scopes: []string{"https://ai.azure.com/.default"},
    })
    if err != nil {
        return fmt.Errorf("failed to get token: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+token.Token)
    return nil
}
```

### 3. Nil-Safe Reflection Pattern

```go
func printTable(obj interface{}) error {
    v := reflect.ValueOf(obj)
    
    // Always check for nil before calling Elem()
    if v.Kind() == reflect.Ptr {
        if v.IsNil() {
            return fmt.Errorf("cannot print nil pointer")
        }
        v = v.Elem()
    }

    if v.Kind() == reflect.Slice {
        for i := 0; i < v.Len(); i++ {
            elem := v.Index(i)
            if elem.Kind() == reflect.Ptr {
                if elem.IsNil() {
                    continue // Skip nil elements
                }
                elem = elem.Elem()
            }
            // Process elem...
        }
    }

    return nil
}
```

### 4. URL/Path Parsing Pattern

```go
// parseEndpoint extracts components from an endpoint URL.
// Be STRICT - reject malformed URLs instead of silently accepting them.
func parseEndpoint(endpoint string) (account, project string, err error) {
    parsedURL, err := url.Parse(endpoint)
    if err != nil {
        return "", "", fmt.Errorf("failed to parse URL: %w", err)
    }

    // Extract account from hostname
    hostname := parsedURL.Hostname()
    hostParts := strings.Split(hostname, ".")
    if len(hostParts) < 1 || hostParts[0] == "" {
        return "", "", fmt.Errorf("cannot extract account from hostname")
    }
    account = hostParts[0]

    // Extract project from path - require EXACT format
    pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
    // Use == not >= to reject extra segments
    if len(pathParts) != 3 || pathParts[0] != "api" || pathParts[1] != "projects" || pathParts[2] == "" {
        return "", "", fmt.Errorf("invalid path format: expected /api/projects/{project}")
    }
    project = pathParts[2]

    return account, project, nil
}
```

---

## Error Handling Rules

### DO: Wrap errors with context
```go
return fmt.Errorf("failed to create model: %w", err)
```

### DO: Return errors from utility functions
```go
if err := utils.PrintObject(result, format); err != nil {
    return err
}
```

### DON'T: Ignore errors
```go
// ❌ WRONG
utils.PrintObject(result, format)

// ❌ WRONG  
_, _ = azdClient.Environment().SetValue(ctx, req)
```

### DON'T: Return generic errors that lose context
```go
// ❌ WRONG
return fmt.Errorf("operation failed")

// ✅ CORRECT
return fmt.Errorf("operation failed: %w", err)
```

---

## Persistent Flags Pattern

When using persistent flags from a parent command, reference the global variable directly:

```go
// In root.go
var rootFlags struct {
    Debug    bool
    NoPrompt bool
}

func NewRootCommand() *cobra.Command {
    cmd := &cobra.Command{...}
    cmd.PersistentFlags().BoolVar(&rootFlags.NoPrompt, "no-prompt", false, "...")
    return cmd
}

// In child commands, reference rootFlags directly
func runInit(ctx context.Context, flags *initFlags) error {
    if rootFlags.NoPrompt {
        // Use no-prompt mode
    }
}
```

---

## AzCopy Integration

### Well-Known Paths
```go
func getWellKnownPaths() []string {
    home, _ := os.UserHomeDir()
    binary := "azcopy"
    if runtime.GOOS == "windows" {
        binary = "azcopy.exe"
    }

    paths := []string{
        filepath.Join(home, ".azd", "bin", binary),
        filepath.Join(home, ".azure", "bin", binary),
    }

    // Linux package manager paths
    if runtime.GOOS == "linux" {
        paths = append(paths, "/usr/bin/azcopy")
    }

    return paths
}
```

### Detect File vs Directory
```go
func getSourceArg(source string) string {
    // Don't modify URL sources
    if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
        return source
    }

    // Check if local path is a directory
    info, err := os.Stat(source)
    if err != nil {
        return source
    }

    if info.IsDir() {
        return filepath.Join(source, "*")
    }
    return source
}
```

---

## Build Configuration

### ldflags in PowerShell
```powershell
# DON'T use single quotes around -X values
$ldFlag = "-ldflags=-s -w -X azure.ai.models/internal/cmd.Version=$Version"

# DO add this for consistent argument passing
$PSNativeCommandArgumentPassing = 'Legacy'
```

### ldflags in Bash
```bash
# DON'T wrap -X values in quotes
go build -ldflags="-X pkg.Version=$VERSION -X pkg.Commit=$COMMIT"
```

---

## Testing Requirements

Every parsing/validation function needs table-driven tests:

```go
func TestParseEndpoint(t *testing.T) {
    tests := []struct {
        name        string
        endpoint    string
        wantAccount string
        wantProject string
        wantErr     bool
    }{
        {
            name:        "valid endpoint",
            endpoint:    "https://myaccount.services.ai.azure.com/api/projects/myproject",
            wantAccount: "myaccount",
            wantProject: "myproject",
            wantErr:     false,
        },
        {
            name:     "missing project",
            endpoint: "https://myaccount.services.ai.azure.com/api/projects/",
            wantErr:  true,
        },
        {
            name:     "extra path segments",
            endpoint: "https://myaccount.services.ai.azure.com/api/projects/proj/extra",
            wantErr:  true,
        },
        {
            name:     "http scheme rejected",
            endpoint: "http://myaccount.services.ai.azure.com/api/projects/proj",
            wantErr:  true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            account, project, err := parseEndpoint(tt.endpoint)
            if (err != nil) != tt.wantErr {
                t.Errorf("parseEndpoint() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if account != tt.wantAccount || project != tt.wantProject {
                t.Errorf("parseEndpoint() = (%v, %v), want (%v, %v)",
                    account, project, tt.wantAccount, tt.wantProject)
            }
        })
    }
}
```

---

## Checklist Before Committing

- [ ] All errors are wrapped with `%w` and returned
- [ ] HTTP clients have timeouts set
- [ ] HTTPS is enforced for endpoints handling auth tokens
- [ ] Nil checks exist before all `.Elem()` calls
- [ ] URL parsing is strict (exact segment counts)
- [ ] `bytes.NewReader()` used instead of `strings.NewReader(string(data))`
- [ ] Persistent flags referenced from global, not passed by value
- [ ] Recovery instructions reference commands that actually exist
- [ ] Unit tests cover all validation/parsing functions
