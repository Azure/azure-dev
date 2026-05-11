# Design Spec: `azd ai connection` Direct Commands + Agent Run Secrets

<!-- cspell:ignore foundry toolbox toolboxes cognitiveservices azidentity keyvault -->

**Spec Source**: [PR #165 – azd ai Direct Commands spec](https://github.com/coreai-microsoft/foundrysdk_specs/pull/165)
**CLI / Engineering Owner**: Travis Angevine
**PM Owner**: John Miller
**Target Release**: Build (preview)

---

## 1. Overview

This document describes the design and code changes for two features:

1. **`azd ai connection`** — A new first-party extension (`azure.ai.connection`) providing direct commands for connection CRUD, metadata management, and credential key management against the Foundry platform.
2. **`azd ai agent run` secrets** — Enhancements to the existing `azure.ai.agents` extension to inject secrets into locally running agents.

### 1.1 Goals

From the spec's success criteria (lines 25–28):

1. A developer who has never used `azd` can create a connection in one command using `azd ai connection create` and inline `--help` text.
2. A coding agent can drive the same flow non-interactively with `--output json` and `--no-prompt`.

### 1.2 Non-Goals

- Toolboxes, Skills, Routines (owned by other teams)
- `azd ai project set/unset`, `azd ai show` (project context — separate work item)
- `azd ai agent optimize` (tracked in optimization spec)
- Config-driven orchestration / `azd up` for connections (targets Ignite)
- Changing auth type after creation (delete-and-recreate per spec line 321)

---

## 2. Architecture

### 2.1 Extension Placement

A **new first-party extension** at `cli/azd/extensions/azure.ai.connection/`:

```
extension.yaml:
  id: azure.ai.connection
  namespace: ai.connection
  → mounts at: azd ai connection
```

This follows the existing multi-extension pattern under the `ai.*` namespace:

| Extension | Namespace | Surface |
|-----------|-----------|---------|
| `azure.ai.agents` | `ai.agent` | `azd ai agent {init, run, invoke, ...}` |
| `azure.ai.models` | `ai.models` | `azd ai models ...` |
| `azure.ai.finetune` | `ai.finetuning` | `azd ai finetuning ...` |
| **`azure.ai.connection`** | **`ai.connection`** | **`azd ai connection ...`** |

The extension framework auto-creates the shared `azd ai` group command as a routing node (see `cli/azd/cmd/extensions.go:45-87`).

### 2.2 API Surfaces — Validated

The connection APIs were validated live against the `hosted-agents-bugbash` project (account: `e2e-tests-ncus-account`, region: northcentralus). Results:

| Operation | ARM (`management.azure.com`) | Data Plane (`services.ai.azure.com`) |
|-----------|------|-----------|
| **List** | ✅ Works | ✅ Works |
| **Get** (metadata only) | ✅ Works (never returns secrets) | ✅ Works |
| **Get with credentials** | ❌ Never returns secrets | ✅ Only way to get secrets (`POST .../getConnectionWithCredentials`) |
| **Create** (PUT) | ✅ Tested & confirmed | ❌ 405 Method Not Allowed |
| **Update** (PUT only, PATCH returns 400) | ✅ GET-then-PUT required | ❌ 405 |
| **Delete** | ✅ Idempotent (no-op if missing) | ❌ 405 |

**Architecture: Hybrid (Option C)** — ARM SDK for CRUD + data-plane for credentials.

```
┌─────────────────────┐     ARM PUT/GET/DELETE       ┌───────────────────────────────────┐
│  azd ai connection  │ ──────────────────────────→  │  ARM Control Plane                │
│  (CLI extension)    │  (management.azure.com)      │  Microsoft.CognitiveServices      │
│                     │                              │  /.../connections/{name}           │
│                     │     Data-plane POST           │                                   │
│                     │ ──────────────────────────→  │  Foundry Project Data Plane        │
└─────────────────────┘  (services.ai.azure.com)     │  /connections/{name}/              │
                                                     │    getConnectionWithCredentials    │
                                                     └───────────────────────────────────┘
```

**Key validation findings**:
- **ARM API** (`armcognitiveservices.NewProjectConnectionsClient`) — already in `go.mod` of `azure.ai.agents`. Supports full CRUD. Uses `management.azure.com` token scope.
- **Data-plane API** (`FoundryProjectsClient.GetConnectionWithCredentials`) — existing code in `azure.ai.agents`. Uses `https://ai.azure.com/.default` token scope.
- **PATCH not supported** — ARM returns 400 ("Missing discriminator property [AuthType]"). Update must GET-then-PUT the full object.
- **ARM PUT is idempotent** — `create --force` works naturally (PUT replaces existing).
- **Auth type payloads** — `ApiKey`, `CustomKeys`, and `None` were all tested end-to-end (create via ARM → read credentials back via data-plane → delete via ARM).

**Tested ARM request bodies**:

ApiKey:
```json
{
  "properties": {
    "category": "ApiKey",
    "target": "https://httpbin.org/get",
    "authType": "ApiKey",
    "credentials": { "key": "test-key-12345" },
    "metadata": { "ApiType": "Azure" }
  }
}
```

CustomKeys:
```json
{
  "properties": {
    "category": "RemoteTool",
    "target": "https://mcp.tavily.com/mcp",
    "authType": "CustomKeys",
    "credentials": { "keys": { "x-api-key": "test-tavily-key-12345" } },
    "metadata": { "type": "custom_MCP" }
  }
}
```

### 2.3 Agent Run Secrets (existing extension)

Pure local operation — no API calls. Secrets are injected as environment variables into the `exec.Command` process that `azd ai agent run` already spawns (see `run.go:148-175`).

---

## 3. Command Surface

### 3.1 Connection Commands

```
azd ai connection create <name>
    --kind <kind>                    # remote-tool, cognitive-search, api-key, etc.
    --target <url-or-arm-id>         # Target URL or ARM resource ID
    --auth-type <type>               # api-key, custom-keys, none
    --key <api-key>                  # API key value (for auth-type=api-key)
    --custom-key <k=v>              # Custom key pair (repeatable)
    --metadata <k=v>                # Metadata pair (repeatable)
    --from-file <path.yaml>         # AgentSchema YAML (mutually exclusive with above flags)
    --force                          # Replace existing resource (ARM PUT upsert)
    -p, --project-endpoint <url>    # Override project endpoint
    --output json|table              # Output format
    --no-prompt                      # Non-interactive mode
    --debug                          # Diagnostic logging

azd ai connection update <name>
    --target <url-or-arm-id>         # New target (partial update)
    --key <api-key>                  # New API key (partial update)
    # NOTE: no --from-file, no --auth-type, no --kind (spec lines 57, 133, 321)

azd ai connection delete <name>
    --force                          # Skip confirmation prompt

azd ai connection show <name>
    --show-credentials               # Opt-in to fetch credential values via data plane

azd ai connection list
    --kind <kind>                    # Filter by connection kind

azd ai connection metadata set <connection-name> <key=value>
azd ai connection metadata remove <connection-name> <key>
azd ai connection metadata list <connection-name>

azd ai connection key set <connection-name> <key=value>
azd ai connection key remove <connection-name> <key>
azd ai connection key list <connection-name>
```

#### 3.1.1 `--from-file` Mutual Exclusivity

Spec requirement (line 56): *"The two input modes are mutually exclusive: `--from-file` is the sole source of truth when present, and the CLI errors out (rather than silently merging) if any per-flag input is also supplied."*

```go
// from_file.go
func validateFromFileExclusivity(cmd *cobra.Command, fromFile string) error {
    if fromFile == "" {
        return nil
    }
    conflicting := []string{"kind", "target", "auth-type", "key", "custom-key", "metadata"}
    for _, flag := range conflicting {
        if cmd.Flags().Changed(flag) {
            return exterrors.Validation(
                CodeConflictingArguments,
                fmt.Sprintf("--from-file and --%s cannot be used together", flag),
                "Use --from-file alone, or use per-flag input without --from-file.",
            )
        }
    }
    return nil
}
```

#### 3.1.2 `--force` Dual Semantics

Spec (line 134): `--force` means different things on different commands:
- `create --force` → upsert (ARM PUT replaces existing)
- `delete --force` → skip confirmation prompt

This is **not** a single shared flag — each command defines its own `--force` with command-specific help text.

#### 3.1.3 Enums

**Connection kinds** (spec Open Question #1, line 345 — v1 candidates):
```go
var validConnectionKinds = []string{
    "remote-tool", "cognitive-search", "api-key", "app-insights",
    "grounding-with-bing-search", "ai-services", "container-registry", "custom-keys",
}
```

**Auth types** (spec Open Question #2, line 346 — v1 committed):
```go
var validAuthTypes = []string{"api-key", "custom-keys", "none"}
```

Casing convention (spec Terminology, line 124): lowercase single-word, kebab-case multi-word.

Implementation: validate against known set, **warn** (not error) for unknown values to allow forward compatibility until the enum is finalized.

### 3.2 Agent Run Secret Flags

Added to the existing `azd ai agent run` command:

```
azd ai agent run [name]
    --secret KEY=VALUE                          # Literal secret (repeatable)
    --secret-from-env KEY                       # Read from host env (repeatable)
    --secret-from-keyvault KEY=<vault-url>      # Fetch from Key Vault (repeatable)
    # ... existing flags (--port, --start-command) unchanged
```

All three are **repeatable** — can be specified multiple times. Resolved secrets are injected as environment variables into the spawned agent process.

### 3.3 Credential Reference Strings in `list` and `show` Output

When `list` or `show` displays a connection, the output includes ready-to-paste **AgentSchema credential reference strings** for each credential key. These use the `${{connections.<name>.credentials.<key>}}` interpolation syntax that `agent.yaml` consumes.

**Example — `azd ai connection show my-test-conn --show-credentials --output json`**:
```json
{
  "name": "my-test-conn",
  "kind": "RemoteTool",
  "target": "https://mcp.tavily.com/mcp",
  "authType": "CustomKeys",
  "credentials": {
    "x-api-key": "tvly-abc123..."
  },
  "credentialReferences": {
    "x-api-key": "${{connections.my-test-conn.credentials.x-api-key}}"
  }
}
```

**Example — `azd ai connection list --output table`**:
```
Name              Kind         Auth Type    Target                        Credential References
----              ----         ---------    ------                        ---------------------
my-test-conn      RemoteTool   CustomKeys   https://mcp.tavily.com/mcp    ${{connections.my-test-conn.credentials.x-api-key}}
prod-search       ApiKey       ApiKey       https://my-search.search...   ${{connections.prod-search.credentials.key}}
learn-mcp         RemoteTool   None         https://learn.microsoft...    (none)
```

The developer can copy the reference string directly into their `agent.yaml`:
```yaml
environment_variables:
  - name: TAVILY_API_KEY
    value: "${{connections.my-test-conn.credentials.x-api-key}}"
```

**Implementation**: The `credentialReferences` field is generated from the connection name + credential keys returned by the data-plane `getConnectionWithCredentials` API. For connections with `authType: ApiKey`, the key name is `key`. For `CustomKeys`, the key names come from the custom keys map (e.g., `x-api-key`). For `authType: None` or `AAD`, no credential references are generated.

```go
// buildCredentialReferences generates ${{connections.<name>.credentials.<key>}}
// strings for each credential key on a connection.
func buildCredentialReferences(connName string, creds *ConnectionCredentials) map[string]string {
    if creds == nil {
        return nil
    }
    refs := map[string]string{}
    if creds.Key != "" {
        refs["key"] = fmt.Sprintf("${{connections.%s.credentials.key}}", connName)
    }
    for k := range creds.CustomKeys {
        refs[k] = fmt.Sprintf("${{connections.%s.credentials.%s}}", connName, k)
    }
    if len(refs) == 0 {
        return nil
    }
    return refs
}
```

### 3.4 `azd ai agent run` — Credential Reference Resolution

When `azd ai agent run` starts a local agent process, it resolves `${{connections.<name>.credentials.<key>}}` references found in the agent manifest's `environment_variables` section. For each reference:

1. Parse the connection name and credential key from the reference string
2. Call the data-plane `getConnectionWithCredentials` API to fetch the actual secret value
3. Inject the resolved value as an environment variable into the spawned agent process

**Example**: Given this `agent.yaml`:
```yaml
environment_variables:
  - name: TAVILY_API_KEY
    value: "${{connections.my-test-conn.credentials.x-api-key}}"
```

At `azd ai agent run` time, the extension:
1. Reads the manifest, finds `${{connections.my-test-conn.credentials.x-api-key}}`
2. Calls `POST .../connections/my-test-conn/getConnectionWithCredentials` (data-plane POST, not GET)
3. Extracts `x-api-key` from the response credentials
4. Sets `TAVILY_API_KEY=tvly-abc123...` in the spawned process environment

**Implementation location**: In `run.go`, after the existing `appendFoundryEnvVars()` call (line 160), add a new `resolveConnectionReferences()` step that scans env vars for `${{connections...}}` patterns and resolves them. Each env var value supports at most one connection reference (the entire value is the reference string).

```go
// resolveConnectionReferences scans environment variable values for
// ${{connections.<name>.credentials.<key>}} patterns and resolves them
// by fetching credentials from the Foundry data plane via POST.
func resolveConnectionReferences(
    ctx context.Context,
    env []string,
    endpoint string,
    cred azcore.TokenCredential,
) ([]string, error) {
    re := regexp.MustCompile(`\$\{\{connections\.([^.]+)\.credentials\.([^}]+)\}\}`)

    // Cache fetched connections to avoid redundant API calls
    connCache := map[string]*ConnectionCredentials{}
    dpClient := NewDataClient(endpoint, cred)

    var result []string
    for _, entry := range env {
        key, value, _ := strings.Cut(entry, "=")
        matches := re.FindStringSubmatch(value)
        if matches == nil {
            result = append(result, entry)
            continue
        }

        connName := matches[1]
        credKey := matches[2]

        // Fetch connection credentials (cached)
        creds, ok := connCache[connName]
        if !ok {
            conn, err := dpClient.GetConnectionWithCredentials(ctx, connName)
            if err != nil {
                return nil, fmt.Errorf("failed to resolve %s: %w", value, err)
            }
            creds = conn.Credentials
            connCache[connName] = creds
        }

        // Look up the specific credential key
        credValue := ""
        if credKey == "key" && creds.Key != "" {
            credValue = creds.Key
        } else if v, exists := creds.CustomKeys[credKey]; exists {
            credValue = v
        } else {
            return nil, fmt.Errorf(
                "credential key %q not found on connection %q", credKey, connName)
        }

        result = append(result, fmt.Sprintf("%s=%s", key, credValue))
        log.Printf("Resolved connection credential: %s (connection: %s, key: %s)", key, connName, credKey)
    }

    return result, nil
}
```

---

## 4. Endpoint Resolution & ARM Resource ID Discovery

### 4.1 Project Endpoint Resolution Order

Per the spec (AZD Environment Scoping, lines 289–294), the resolution cascade is:

```
1. -p / --project-endpoint flag              ← explicit per-command override
2. Inside an azd project: active azd env     ← AZURE_AI_PROJECT_ENDPOINT from azd env
3. Global config                             ← extensions.ai-agents.context.endpoint (set by azd ai project set)
4. FOUNDRY_PROJECT_ENDPOINT env var          ← shell / CI environment variable
5. Structured error                          ← "No Foundry project endpoint resolved. Run azd ai project set..."
```

The `-p`/`--project-endpoint` flag is registered as a **persistent flag** on the extension root command so every subcommand inherits it.

Steps 2 and 3 use the `azdext` gRPC client to communicate with the azd host:
- **Step 2**: `azdClient.Environment().GetValue(ctx, "AZURE_AI_PROJECT_ENDPOINT")` — reads from the active azd env. Only works when running inside an azd project directory.
- **Step 3**: `azdClient.UserConfig().GetString(ctx, "extensions.ai-agents.context.endpoint")` — reads from global config (`~/.azd/config.json`). Works anywhere, set by `azd ai project set`.

If the azdext client cannot connect (e.g., extension invoked standalone without azd host), steps 2 and 3 are skipped silently and resolution falls through to step 4.

### 4.2 Endpoint → ARM Resource ID Bridge

ARM CRUD calls need subscription, resource group, account name, and project name. The extension derives these from the project endpoint URL:

**Step A — Parse account + project from the URL** (always available):
```
https://{account}.services.ai.azure.com/api/projects/{project}
  → account = "e2e-tests-ncus-account"
  → project = "hosted-agents-bugbash"
```

**Step B — Discover subscription + resource group via a bootstrap data-plane GET** (validated in POC):

Every data-plane GET response includes the full ARM resource ID in the `id` field:
```json
GET {endpoint}/connections?api-version=2025-11-15-preview
→ {
    "value": [{
      "id": "/subscriptions/921496dc-.../resourceGroups/agents-e2e-tests-ncus/providers/Microsoft.CognitiveServices/accounts/e2e-tests-ncus-account/projects/hosted-agents-bugbash/connections/fabric-api"
    }]
  }
```

Parse the ARM path → extract `subscriptionId` and `resourceGroup` → use for ARM SDK calls.

**Edge case — empty project (zero connections)**: Unlikely in practice (new Foundry projects always have default connections), but if it occurs, the extension can fall back to prompting for subscription/rg or using a project metadata API.

### 4.3 Implementation

```go
// endpoint.go

// resolveProjectEndpoint implements the 5-level resolution cascade from the spec.
func resolveProjectEndpoint(ctx context.Context, cmd *cobra.Command) (string, error) {
    // 1. -p / --project-endpoint flag
    if ep, _ := cmd.Flags().GetString("project-endpoint"); ep != "" {
        return ep, nil
    }

    // 2 & 3. Try azd host (env value + global config) — best-effort
    azdClient, err := azdext.NewAzdClient()
    if err == nil {
        defer azdClient.Close()

        // 2. Active azd env → AZURE_AI_PROJECT_ENDPOINT
        if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
            if valResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
                EnvName: envResp.Environment.Name,
                Key:     "AZURE_AI_PROJECT_ENDPOINT",
            }); err == nil && valResp.Value != "" {
                return valResp.Value, nil
            }
        }

        // 3. Global config → extensions.ai-agents.context.endpoint
        if cfgResp, err := azdClient.UserConfig().GetString(ctx, &azdext.GetStringRequest{
            Path: "extensions.ai-agents.context.endpoint",
        }); err == nil && cfgResp.Value != "" {
            return cfgResp.Value, nil
        }
    }

    // 4. FOUNDRY_PROJECT_ENDPOINT environment variable
    if ep := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); ep != "" {
        return ep, nil
    }

    // 5. Structured error
    return "", exterrors.Dependency(
        CodeMissingProjectEndpoint,
        "No Foundry project endpoint resolved.",
        "Run 'azd ai project set' to set one, or pass '--project-endpoint'.",
    )
}

// parseEndpointComponents extracts account and project from the endpoint URL.
func parseEndpointComponents(endpoint string) (account, project string, err error) {
    u, err := url.Parse(endpoint)
    if err != nil {
        return "", "", fmt.Errorf("invalid endpoint URL: %w", err)
    }
    account, _, _ = strings.Cut(u.Hostname(), ".")
    parts := strings.Split(strings.Trim(u.Path, "/"), "/")
    for i, p := range parts {
        if p == "projects" && i+1 < len(parts) {
            project = parts[i+1]
            break
        }
    }
    if account == "" || project == "" {
        return "", "", fmt.Errorf("could not parse account/project from %q", endpoint)
    }
    return account, project, nil
}

// discoverARMContext makes a data-plane list call to discover subscription and
// resource group from the ARM resource IDs embedded in connection responses.
func discoverARMContext(ctx context.Context, dpClient *DataClient) (*armContext, error) {
    conns, err := dpClient.ListConnections(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to list connections for ARM discovery: %w", err)
    }
    if len(conns) == 0 {
        return nil, fmt.Errorf("no connections found; cannot discover ARM context")
    }
    return parseARMResourceID(conns[0].ID)
}

type armContext struct {
    SubscriptionID string
    ResourceGroup  string
    AccountName    string
    ProjectName    string
}

func parseARMResourceID(resourceID string) (*armContext, error) {
    parts := strings.Split(resourceID, "/")
    result := &armContext{}
    for i, part := range parts {
        switch {
        case part == "subscriptions" && i+1 < len(parts):
            result.SubscriptionID = parts[i+1]
        case part == "resourceGroups" && i+1 < len(parts):
            result.ResourceGroup = parts[i+1]
        case part == "accounts" && i+1 < len(parts):
            result.AccountName = parts[i+1]
        case part == "projects" && i+1 < len(parts):
            result.ProjectName = parts[i+1]
        }
    }
    if result.SubscriptionID == "" || result.ResourceGroup == "" {
        return nil, fmt.Errorf("could not extract ARM context from: %s", resourceID)
    }
    return result, nil
}
```

### 4.4 Full Connection Context Resolution

Putting it all together — the shared context that every command uses:

```go
// connectionContext holds the resolved clients and project info.
type connectionContext struct {
    armClient *armcognitiveservices.ProjectConnectionsClient
    dpClient  *DataClient
    rg        string
    account   string
    project   string
}

func resolveConnectionContext(ctx context.Context, cmd *cobra.Command) (*connectionContext, error) {
    endpoint, err := resolveProjectEndpoint(ctx, cmd)
    if err != nil {
        return nil, err
    }

    account, project, err := parseEndpointComponents(endpoint)
    if err != nil {
        return nil, err
    }

    cred, err := newCredential()
    if err != nil {
        return nil, err
    }

    // Data-plane client (for list, get-with-credentials, and ARM discovery)
    dpClient := NewDataClient(endpoint, cred)

    // Discover subscription + resource group from data-plane response
    armCtx, err := discoverARMContext(ctx, dpClient)
    if err != nil {
        return nil, err
    }

    // ARM SDK client for CRUD
    armClient, err := armcognitiveservices.NewProjectConnectionsClient(
        armCtx.SubscriptionID, cred, nil,
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create ARM client: %w", err)
    }

    return &connectionContext{
        armClient: armClient,
        dpClient:  dpClient,
        rg:        armCtx.ResourceGroup,
        account:   account,
        project:   project,
    }, nil
}
```

---

## 5. Code Changes — New Extension (`azure.ai.connection`)

### 5.1 File Layout

```
cli/azd/extensions/azure.ai.connection/
├── main.go
├── go.mod
├── go.sum
├── extension.yaml
├── version.txt
├── internal/
│   ├── cmd/
│   │   ├── root.go
│   │   ├── endpoint.go
│   │   ├── endpoint_test.go
│   │   ├── from_file.go
│   │   ├── from_file_test.go
│   │   ├── connection.go
│   │   ├── connection_test.go
│   │   ├── connection_metadata.go
│   │   ├── connection_metadata_test.go
│   │   ├── connection_key.go
│   │   └── connection_key_test.go
│   ├── pkg/
│   │   └── connections/
│   │       ├── arm_client.go
│   │       ├── arm_client_test.go
│   │       ├── data_client.go
│   │       ├── data_client_test.go
│   │       └── models.go
│   ├── exterrors/
│   │   ├── errors.go
│   │   └── codes.go
│   └── version/
│       └── version.go
```

### 5.2 `extension.yaml`

```yaml
# yaml-language-server: $schema=../extension.schema.json
id: azure.ai.connection
namespace: ai.connection
displayName: Foundry connections (Preview)
description: Manage Foundry project connections from your terminal. (Preview)
usage: azd ai connection <command> [options]
version: 0.1.0-preview
requiredAzdVersion: ">1.23.13"
language: go
capabilities:
  - custom-commands
  - metadata
examples:
  - name: create
    description: Create a new connection.
    usage: azd ai connection create my-conn --kind api-key --target https://example.com --auth-type api-key --key $KEY
  - name: list
    description: List all connections.
    usage: azd ai connection list
```

Note: unlike `azure.ai.agents`, this extension only needs `custom-commands` capability — it does not participate in lifecycle events, service targeting, or MCP.

### 5.3 `main.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
    "azureaiconnection/internal/cmd"
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
    azdext.Run(cmd.NewRootCommand())
}
```

### 5.4 `go.mod`

```
module azureaiconnection

go 1.26

require (
    github.com/azure/azure-dev/cli/azd v1.24.3
    github.com/Azure/azure-sdk-for-go/sdk/azcore v1.18.0
    github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.9.0
    github.com/spf13/cobra v1.9.1
    gopkg.in/yaml.v3 v3.0.1
)
```

### 5.5 `internal/cmd/root.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    "github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
    rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
        Name:  "connection",
        Use:   "connection <command> [options]",
        Short: "Manage Foundry project connections. (Preview)",
    })
    rootCmd.SilenceUsage = true
    rootCmd.SilenceErrors = true
    rootCmd.CompletionOptions.DisableDefaultCmd = true

    // Register -p / --project-endpoint as a persistent flag
    rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
        "Foundry project endpoint URL (overrides FOUNDRY_PROJECT_ENDPOINT)")

    rootCmd.AddCommand(azdext.NewListenCommand(nil))
    rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.connection", func() *cobra.Command {
        return rootCmd
    }))

    rootCmd.AddCommand(newConnectionCreateCommand(extCtx))
    rootCmd.AddCommand(newConnectionUpdateCommand(extCtx))
    rootCmd.AddCommand(newConnectionDeleteCommand(extCtx))
    rootCmd.AddCommand(newConnectionShowCommand(extCtx))
    rootCmd.AddCommand(newConnectionListCommand(extCtx))
    rootCmd.AddCommand(newConnectionMetadataCommand(extCtx))
    rootCmd.AddCommand(newConnectionKeyCommand(extCtx))

    return rootCmd
}
```

### 5.6 `internal/cmd/connection.go` — CRUD Commands

Each command follows the pattern established in `azure.ai.agents/internal/cmd/show.go`:

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
    "azureaiconnection/internal/exterrors"
    "azureaiconnection/internal/pkg/connections"
    "encoding/json"
    "fmt"
    "text/tabwriter"
    "os"

    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/azure/azure-dev/cli/azd/pkg/azdext"
    "github.com/spf13/cobra"
)

// --- CREATE ---

type createFlags struct {
    kind     string
    target   string
    authType string
    key      string
    customKeys []string  // repeatable --custom-key "k=v"
    metadata   []string  // repeatable --metadata "k=v"
    fromFile string
    force    bool
}

func newConnectionCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    flags := &createFlags{}

    cmd := &cobra.Command{
        Use:   "create <name>",
        Short: "Create a new Foundry project connection.",
        Long: `Create a new Foundry project connection.

Specify connection properties via flags, or provide a YAML definition
with --from-file. The two modes are mutually exclusive.

Use --force to replace an existing connection with the same name.`,
        Example: `  # Create with flags
  azd ai connection create my-tavily \
    --kind remote-tool \
    --target https://mcp.tavily.com/mcp \
    --auth-type custom-keys \
    --custom-key "x-api-key=tvly-abc123"

  # Create from YAML
  azd ai connection create my-search --from-file ./my-search.yaml

  # Upsert (replace if exists)
  azd ai connection create my-conn --kind api-key --target https://x.com --force`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            name := args[0]

            // Validate --from-file exclusivity
            if err := validateFromFileExclusivity(cmd, flags.fromFile); err != nil {
                return err
            }

            endpoint, err := resolveProjectEndpoint(ctx, cmd)
            if err != nil {
                return err
            }

            cred, err := newCredential()
            if err != nil {
                return err
            }

            client := connections.NewARMClient(endpoint, cred)

            var req *connections.CreateRequest
            if flags.fromFile != "" {
                req, err = parseConnectionFromFile(flags.fromFile)
                if err != nil {
                    return err
                }
                req.Name = name
            } else {
                req = &connections.CreateRequest{
                    Name:       name,
                    Kind:       flags.kind,
                    Target:     flags.target,
                    AuthType:   flags.authType,
                    Key:        flags.key,
                    CustomKeys: parseKeyValuePairs(flags.customKeys),
                    Metadata:   parseKeyValuePairs(flags.metadata),
                }
            }

            ctx := azdext.WithAccessToken(cmd.Context())
            conn, err := client.Create(ctx, req, flags.force)
            if err != nil {
                return exterrors.ServiceFromAzure(err, OpCreateConnection)
            }

            return printOutput(conn, extCtx.OutputFormat)
        },
    }

    cmd.Flags().StringVar(&flags.kind, "kind", "", "Connection kind (e.g., remote-tool, cognitive-search)")
    cmd.Flags().StringVar(&flags.target, "target", "", "Target URL or ARM resource ID")
    cmd.Flags().StringVar(&flags.authType, "auth-type", "", "Auth type (api-key, custom-keys, none)")
    cmd.Flags().StringVar(&flags.key, "key", "", "API key value")
    cmd.Flags().StringArrayVar(&flags.customKeys, "custom-key", nil, "Custom key pair k=v (repeatable)")
    cmd.Flags().StringArrayVar(&flags.metadata, "metadata", nil, "Metadata pair k=v (repeatable)")
    cmd.Flags().StringVar(&flags.fromFile, "from-file", "", "YAML definition file (mutually exclusive with other flags)")
    cmd.Flags().BoolVar(&flags.force, "force", false, "Replace existing connection (upsert via ARM PUT)")

    azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
        Name:          "output",
        AllowedValues: []string{"json", "table"},
        Default:       "json",
    })

    return cmd
}

// --- UPDATE ---

func newConnectionUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    var target, key string

    cmd := &cobra.Command{
        Use:   "update <name>",
        Short: "Update a connection's target or key.",
        Long: `Update a connection's target URL or API key.

Only the specified flags are changed; all other fields are preserved.
To manage metadata or custom keys, use the 'metadata' and 'key' subcommands.

Does not accept --from-file or --auth-type (delete and recreate to change auth type).`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            name := args[0]

            if !cmd.Flags().Changed("target") && !cmd.Flags().Changed("key") {
                return exterrors.Validation(
                    CodeMissingConnectionField,
                    "No fields to update. Specify --target and/or --key.",
                    "Run 'azd ai connection update --help' for usage.",
                )
            }

            endpoint, err := resolveProjectEndpoint(cmd)
            if err != nil {
                return err
            }

            cred, err := newCredential()
            if err != nil {
                return err
            }

            client := connections.NewARMClient(endpoint, cred)
            ctx := azdext.WithAccessToken(cmd.Context())

            conn, err := client.Update(ctx, name, &connections.UpdateRequest{
                Target: target,
                Key:    key,
            })
            if err != nil {
                return exterrors.ServiceFromAzure(err, OpUpdateConnection)
            }

            return printOutput(conn, extCtx.OutputFormat)
        },
    }

    cmd.Flags().StringVar(&target, "target", "", "New target URL or ARM resource ID")
    cmd.Flags().StringVar(&key, "key", "", "New API key value")

    azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
        Name:          "output",
        AllowedValues: []string{"json", "table"},
        Default:       "json",
    })

    return cmd
}

// --- DELETE ---

func newConnectionDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    var force bool

    cmd := &cobra.Command{
        Use:   "delete <name>",
        Short: "Delete a connection.",
        Long:  "Delete a connection. Prompts for confirmation unless --force is specified.",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            name := args[0]
            ctx := azdext.WithAccessToken(cmd.Context())

            connCtx, err := resolveConnectionContext(ctx, cmd)
            if err != nil {
                return err
            }

            // GET to confirm it exists and show details
            conn, err := connCtx.armClient.Get(ctx, name)
            if err != nil {
                return exterrors.ServiceFromAzure(err, OpGetConnection)
            }

            // Show what will be deleted
            fmt.Printf("Connection: %s\n", name)
            fmt.Printf("Target:     %s\n", getTarget(conn))

            // Confirm unless --force
            if !force {
                if extCtx.NoPrompt {
                    return exterrors.Validation(
                        CodeMissingForceFlag,
                        fmt.Sprintf("Deleting %q requires confirmation.", name),
                        "Use --force to skip confirmation in non-interactive mode.",
                    )
                }

                azdClient, err := azdext.NewAzdClient()
                if err != nil {
                    return fmt.Errorf("failed to create azd client: %w", err)
                }
                defer azdClient.Close()

                confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
                    Options: &azdext.ConfirmOptions{
                        Message:      "Are you sure you want to delete this connection?",
                        DefaultValue: new(false),
                    },
                })
                if err != nil {
                    return err
                }
                if !*confirmResp.Value {
                    fmt.Println("Cancelled.")
                    return nil
                }
            }

            if err := connCtx.armClient.Delete(ctx, name); err != nil {
                return exterrors.ServiceFromAzure(err, OpDeleteConnection)
            }

            fmt.Printf("Connection %q deleted.\n", name)
            return nil
        },
    }

    cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")

    return cmd
}

// --- SHOW ---

func newConnectionShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    var showCredentials bool

    cmd := &cobra.Command{
        Use:   "show <name>",
        Short: "Show connection details.",
        Long: `Show connection details.

By default shows metadata only. Use --show-credentials to fetch credential
values from the data plane (requires appropriate permissions).`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            name := args[0]

            endpoint, err := resolveProjectEndpoint(cmd)
            if err != nil {
                return err
            }

            cred, err := newCredential()
            if err != nil {
                return err
            }

            ctx := azdext.WithAccessToken(cmd.Context())

            // Always fetch metadata via ARM
            armClient := connections.NewARMClient(endpoint, cred)
            conn, err := armClient.Get(ctx, name)
            if err != nil {
                return exterrors.ServiceFromAzure(err, OpGetConnection)
            }

            // Optionally fetch credentials via data plane
            if showCredentials {
                dataClient := connections.NewDataClient(endpoint, cred)
                creds, err := dataClient.GetCredentials(ctx, name)
                if err != nil {
                    return exterrors.ServiceFromAzure(err, OpGetConnectionCredentials)
                }
                conn.Credentials = creds
            }

            return printOutput(conn, extCtx.OutputFormat)
        },
    }

    cmd.Flags().BoolVar(&showCredentials, "show-credentials", false,
        "Fetch credential values from the data plane")

    azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
        Name:          "output",
        AllowedValues: []string{"json", "table"},
        Default:       "json",
    })

    return cmd
}

// --- LIST ---

func newConnectionListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
    var kind string

    cmd := &cobra.Command{
        Use:   "list",
        Short: "List connections.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            endpoint, err := resolveProjectEndpoint(cmd)
            if err != nil {
                return err
            }

            cred, err := newCredential()
            if err != nil {
                return err
            }

            client := connections.NewARMClient(endpoint, cred)
            ctx := azdext.WithAccessToken(cmd.Context())

            conns, err := client.List(ctx, &connections.ListOptions{Kind: kind})
            if err != nil {
                return exterrors.ServiceFromAzure(err, OpListConnections)
            }

            return printOutput(conns, extCtx.OutputFormat)
        },
    }

    cmd.Flags().StringVar(&kind, "kind", "", "Filter by connection kind")

    azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
        Name:          "output",
        AllowedValues: []string{"json", "table"},
        Default:       "json",
    })

    return cmd
}

// --- Helpers ---

func newCredential() (*azidentity.AzureDeveloperCLICredential, error) {
    cred, err := azidentity.NewAzureDeveloperCLICredential(
        &azidentity.AzureDeveloperCLICredentialOptions{},
    )
    if err != nil {
        return nil, exterrors.Auth(
            CodeCredentialCreationFailed,
            fmt.Sprintf("Failed to create Azure credential: %s", err),
            "Run 'azd auth login' to authenticate.",
        )
    }
    return cred, nil
}

func printOutput(v any, format string) error {
    switch format {
    case "table":
        return printTable(v)
    default:
        data, err := json.MarshalIndent(v, "", "  ")
        if err != nil {
            return fmt.Errorf("failed to marshal output: %w", err)
        }
        fmt.Println(string(data))
        return nil
    }
}

func parseKeyValuePairs(pairs []string) (map[string]string, error) {
    result := make(map[string]string, len(pairs))
    for _, pair := range pairs {
        k, v, ok := strings.Cut(pair, "=")
        if !ok || k == "" {
            return nil, fmt.Errorf("invalid key=value pair: %q", pair)
        }
        result[k] = v
    }
    return result, nil
}
```

### 5.7 `internal/pkg/connections/models.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

// Connection represents a Foundry project connection.
type Connection struct {
    Name        string               `json:"name"`
    ID          string               `json:"id"`
    Kind        string               `json:"type"`
    Target      string               `json:"target"`
    AuthType    string               `json:"authType,omitempty"`
    IsDefault   bool                 `json:"isDefault"`
    Metadata    map[string]string    `json:"metadata,omitempty"`
    Credentials *ConnectionCredentials `json:"credentials,omitempty"`
}

// ConnectionCredentials holds credential values returned by the data-plane
// getConnectionWithCredentials endpoint. The shape varies by auth type:
//   - ApiKey:     Key is populated (e.g., "abc123")
//   - CustomKeys: CustomKeys map is populated (e.g., {"x-api-key": "tvly-..."})
//   - AAD/None:   Only Type is populated, no secret values
type ConnectionCredentials struct {
    Type       string            `json:"type"`
    Key        string            `json:"key,omitempty"`
    CustomKeys map[string]string `json:"keys,omitempty"`
}

// CreateRequest is the input for creating a connection.
type CreateRequest struct {
    Name       string            `json:"name"`
    Kind       string            `json:"kind"`
    Target     string            `json:"target"`
    AuthType   string            `json:"authType"`
    Key        string            `json:"key,omitempty"`
    CustomKeys map[string]string `json:"customKeys,omitempty"`
    Metadata   map[string]string `json:"metadata,omitempty"`
}

// UpdateRequest is the input for updating a connection.
// Only non-empty fields are merged.
type UpdateRequest struct {
    Target string `json:"target,omitempty"`
    Key    string `json:"key,omitempty"`
}

// ListOptions controls filtering for connection list.
type ListOptions struct {
    Kind string
}
```

### 5.8 `internal/pkg/connections/arm_client.go`

Uses the official ARM SDK (`armcognitiveservices.NewProjectConnectionsClient`) — already validated live. The SDK is already a dependency in the `azure.ai.agents` extension's `go.mod`.

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

import (
    "context"
    "fmt"

    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

// ARMClient wraps the official ARM SDK for connection CRUD.
type ARMClient struct {
    inner   *armcognitiveservices.ProjectConnectionsClient
    rg      string
    account string
    project string
}

// NewARMClient creates a new ARM client for connection operations.
func NewARMClient(
    subscriptionID, rg, account, project string,
    cred azcore.TokenCredential,
) (*ARMClient, error) {
    client, err := armcognitiveservices.NewProjectConnectionsClient(
        subscriptionID, cred, nil,
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create ARM connections client: %w", err)
    }
    return &ARMClient{inner: client, rg: rg, account: account, project: project}, nil
}

// Create creates (or replaces with --force) a connection via ARM PUT.
// ARM PUT is idempotent — it replaces an existing connection with the same name.
func (c *ARMClient) Create(ctx context.Context, name string, body armcognitiveservices.ConnectionModel) (*armcognitiveservices.ConnectionModel, error) {
    resp, err := c.inner.Create(ctx, c.rg, c.account, c.project, name, body, nil)
    if err != nil {
        return nil, err
    }
    return &resp.ConnectionModel, nil
}

// Update performs a GET-then-PUT merge. ARM does not support PATCH (returns 400).
func (c *ARMClient) Update(ctx context.Context, name string, mergeFn func(*armcognitiveservices.ConnectionModel)) (*armcognitiveservices.ConnectionModel, error) {
    // 1. GET current
    current, err := c.inner.Get(ctx, c.rg, c.account, c.project, name, nil)
    if err != nil {
        return nil, fmt.Errorf("connection %q not found: %w", name, err)
    }
    // 2. Apply changes
    mergeFn(&current.ConnectionModel)
    // 3. PUT back
    resp, err := c.inner.Create(ctx, c.rg, c.account, c.project, name, current.ConnectionModel, nil)
    if err != nil {
        return nil, err
    }
    return &resp.ConnectionModel, nil
}

// Delete deletes a connection via ARM DELETE. Idempotent (no-op if missing).
func (c *ARMClient) Delete(ctx context.Context, name string) error {
    _, err := c.inner.Delete(ctx, c.rg, c.account, c.project, name, nil)
    return err
}

// Get retrieves a connection's metadata via ARM GET (never returns credentials).
func (c *ARMClient) Get(ctx context.Context, name string) (*armcognitiveservices.ConnectionModel, error) {
    resp, err := c.inner.Get(ctx, c.rg, c.account, c.project, name, nil)
    if err != nil {
        return nil, err
    }
    return &resp.ConnectionModel, nil
}

// List lists all connections via ARM, with optional type filter (client-side).
func (c *ARMClient) List(ctx context.Context, filterKind string) ([]*armcognitiveservices.ConnectionModel, error) {
    pager := c.inner.NewListPager(c.rg, c.account, c.project, nil)
    var result []*armcognitiveservices.ConnectionModel
    for pager.More() {
        page, err := pager.NextPage(ctx)
        if err != nil {
            return nil, err
        }
        for _, conn := range page.Value {
            if filterKind == "" || matchesKind(conn, filterKind) {
                result = append(result, conn)
            }
        }
    }
    return result, nil
}
```

### 5.9 `internal/pkg/connections/data_client.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

import (
    "context"
    "fmt"

    "azureaiconnection/internal/version"

    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
    "github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// DataClient provides credential operations via the Foundry data plane.
type DataClient struct {
    endpoint string
    pipeline runtime.Pipeline
}

// NewDataClient creates a new data-plane client for credential operations.
func NewDataClient(endpoint string, cred azcore.TokenCredential) *DataClient {
    userAgent := fmt.Sprintf("azd-ext-azure-ai-connection/%s", version.Version)

    clientOptions := &policy.ClientOptions{
        PerCallPolicies: []policy.Policy{
            runtime.NewBearerTokenPolicy(
                cred,
                []string{"https://ai.azure.com/.default"},
                nil,
            ),
            azsdk.NewMsCorrelationPolicy(),
            azsdk.NewUserAgentPolicy(userAgent),
        },
    }

    pipeline := runtime.NewPipeline(
        "azure-ai-connection-data",
        "v1.0.0",
        runtime.PipelineOptions{},
        clientOptions,
    )

    return &DataClient{endpoint: endpoint, pipeline: pipeline}
}

// GetCredentials fetches credential values for a named connection.
// Spec Open Question #3: may be a single endpoint or fan-out per auth type.
func (c *DataClient) GetCredentials(ctx context.Context, name string) (*Credentials, error) {
    // TBD: data-plane endpoint to GET credential values
    panic("TODO: implement against confirmed data-plane endpoint")
}

// SetKey sets a custom key value on a connection.
func (c *DataClient) SetKey(ctx context.Context, connName, key, value string) error {
    panic("TODO: implement against confirmed data-plane endpoint")
}

// RemoveKey removes a custom key from a connection.
func (c *DataClient) RemoveKey(ctx context.Context, connName, key string) error {
    panic("TODO: implement against confirmed data-plane endpoint")
}

// ListKeys lists custom keys on a connection.
func (c *DataClient) ListKeys(ctx context.Context, connName string) (map[string]string, error) {
    panic("TODO: implement against confirmed data-plane endpoint")
}
```

### 5.10 `internal/exterrors/codes.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for connection validation.
const (
    CodeConflictingArguments   = "conflicting_arguments"
    CodeMissingConnectionField = "missing_connection_field"
    CodeInvalidConnectionKind  = "invalid_connection_kind"
    CodeInvalidAuthType        = "invalid_auth_type"
    CodeInvalidFromFile        = "invalid_from_file"
    CodeMissingForceFlag       = "missing_force_flag"
)

// Error codes for endpoint resolution.
const (
    CodeMissingProjectEndpoint = "missing_project_endpoint"
)

// Error codes for auth.
const (
    CodeCredentialCreationFailed = "credential_creation_failed"
)

// Operation names for ServiceFromAzure errors.
const (
    OpCreateConnection         = "create_connection"
    OpUpdateConnection         = "update_connection"
    OpDeleteConnection         = "delete_connection"
    OpGetConnection            = "get_connection"
    OpGetConnectionCredentials = "get_connection_credentials"
    OpListConnections          = "list_connections"
    OpSetConnectionMetadata    = "set_connection_metadata"
    OpRemoveConnectionMetadata = "remove_connection_metadata"
    OpSetConnectionKey         = "set_connection_key"
    OpRemoveConnectionKey      = "remove_connection_key"
)
```

---

## 6. Code Changes — Agent Run Secrets (`azure.ai.agents`)

### 6.1 Modified: `internal/cmd/run.go`

Add three new flags to the existing `runFlags` struct and inject them into the spawned process environment:

```go
// Additions to runFlags struct
type runFlags struct {
    port              int
    name              string
    startCommand      string
    secrets           []string  // NEW: --secret KEY=VALUE (repeatable)
    secretsFromEnv    []string  // NEW: --secret-from-env KEY (repeatable)
    secretsFromKV     []string  // NEW: --secret-from-keyvault KEY=<vault-url> (repeatable)
}

// Additions to newRunCommand flag registration
cmd.Flags().StringArrayVar(&flags.secrets, "secret", nil,
    "Inject secret as KEY=VALUE into agent env (repeatable)")
cmd.Flags().StringArrayVar(&flags.secretsFromEnv, "secret-from-env", nil,
    "Read KEY from host env and inject into agent env (repeatable)")
cmd.Flags().StringArrayVar(&flags.secretsFromKV, "secret-from-keyvault", nil,
    "Fetch KEY=<vault-url>/secrets/<name> from Key Vault and inject (repeatable)")
```

In `runRun()`, after `env = appendFoundryEnvVars(...)` (line 160), add secret resolution:

```go
    // Resolve and inject secrets into the agent process environment
    secretEnv, err := resolveSecrets(ctx, flags.secrets, flags.secretsFromEnv, flags.secretsFromKV)
    if err != nil {
        return fmt.Errorf("failed to resolve secrets: %w", err)
    }
    env = append(env, secretEnv...)
```

### 6.2 New: `internal/cmd/secrets.go`

```go
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
    "context"
    "fmt"
    "os"
    "strings"

    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// resolveSecrets resolves all secret sources into KEY=VALUE environment variable strings.
func resolveSecrets(
    ctx context.Context,
    literals []string,      // --secret KEY=VALUE
    fromEnv []string,       // --secret-from-env KEY
    fromKV []string,        // --secret-from-keyvault KEY=<vault-url>/secrets/<name>
) ([]string, error) {
    var result []string

    // 1. Literal secrets
    for _, s := range literals {
        if !strings.Contains(s, "=") {
            return nil, fmt.Errorf("invalid --secret format %q: expected KEY=VALUE", s)
        }
        result = append(result, s)
    }

    // 2. Secrets from host environment
    for _, key := range fromEnv {
        value, ok := os.LookupEnv(key)
        if !ok {
            return nil, fmt.Errorf("--secret-from-env: environment variable %q not set", key)
        }
        result = append(result, fmt.Sprintf("%s=%s", key, value))
    }

    // 3. Secrets from Key Vault
    if len(fromKV) > 0 {
        cred, err := azidentity.NewAzureDeveloperCLICredential(
            &azidentity.AzureDeveloperCLICredentialOptions{},
        )
        if err != nil {
            return nil, fmt.Errorf("failed to create credential for Key Vault: %w", err)
        }

        for _, spec := range fromKV {
            key, vaultRef, ok := strings.Cut(spec, "=")
            if !ok {
                return nil, fmt.Errorf(
                    "invalid --secret-from-keyvault format %q: expected KEY=<vault-url>/secrets/<name>",
                    spec,
                )
            }

            value, err := fetchKeyVaultSecret(ctx, cred, vaultRef)
            if err != nil {
                return nil, fmt.Errorf("--secret-from-keyvault %s: %w", key, err)
            }

            result = append(result, fmt.Sprintf("%s=%s", key, value))
        }
    }

    return result, nil
}

// fetchKeyVaultSecret fetches a secret value from Azure Key Vault.
// vaultRef is a full URL like "https://myvault.vault.azure.net/secrets/mysecret".
func fetchKeyVaultSecret(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, vaultRef string) (string, error) {
    // Parse vault URL and secret name from the reference
    // Expected format: https://<vault>.vault.azure.net/secrets/<name>
    vaultURL, secretName, err := parseVaultReference(vaultRef)
    if err != nil {
        return "", err
    }

    client, err := azsecrets.NewClient(vaultURL, cred, nil)
    if err != nil {
        return "", fmt.Errorf("failed to create Key Vault client: %w", err)
    }

    resp, err := client.GetSecret(ctx, secretName, "", nil)
    if err != nil {
        return "", fmt.Errorf("failed to get secret %q: %w", secretName, err)
    }

    if resp.Value == nil {
        return "", fmt.Errorf("secret %q has no value", secretName)
    }

    return *resp.Value, nil
}

// parseVaultReference splits "https://myvault.vault.azure.net/secrets/mysecret"
// into vault URL and secret name.
func parseVaultReference(ref string) (vaultURL, secretName string, err error) {
    // Find "/secrets/" in the URL
    idx := strings.Index(ref, "/secrets/")
    if idx == -1 {
        return "", "", fmt.Errorf(
            "invalid vault reference %q: expected format https://<vault>.vault.azure.net/secrets/<name>",
            ref,
        )
    }

    vaultURL = ref[:idx]
    secretName = ref[idx+len("/secrets/"):]

    if secretName == "" {
        return "", "", fmt.Errorf("invalid vault reference %q: secret name is empty", ref)
    }

    return vaultURL, secretName, nil
}
```

---

## 7. Error Handling

Following the `azure.ai.agents` extension pattern (see `AGENTS.md`):

- Lower-level helpers return `fmt.Errorf("context: %w", err)`
- Command-level code classifies with `exterrors.*` factories
- Azure SDK errors use `exterrors.ServiceFromAzure(err, operation)`
- Once structured, errors are returned unchanged (no re-wrapping)

### Error scenarios and their handling:

| Scenario | Error type | Code | Suggestion |
|----------|-----------|------|------------|
| No endpoint resolved | `Dependency` | `missing_project_endpoint` | "Pass --project-endpoint or set FOUNDRY_PROJECT_ENDPOINT." |
| `--from-file` + flags | `Validation` | `conflicting_arguments` | "Use --from-file alone, or use per-flag input." |
| No fields on update | `Validation` | `missing_connection_field` | "Specify --target and/or --key." |
| Delete without --force in --no-prompt | `Validation` | `missing_force_flag` | "Use --force to skip confirmation." |
| Auth failure | `Auth` | `credential_creation_failed` | "Run 'azd auth login' to authenticate." |
| ARM API error | `ServiceFromAzure` | Azure error code | Auto-extracted from `azcore.ResponseError` |

---

## 8. Output Formatting

Following the existing pattern from `azure.ai.agents/internal/cmd/show.go:117-121`:

```go
azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
    Name:          "output",
    AllowedValues: []string{"json", "table"},
    Default:       "json",
})
```

- **JSON** (`--output json`): `json.MarshalIndent(v, "", "  ")` to stdout
- **Table** (`--output table`): `text/tabwriter` to stdout

Commands that produce no structured output (e.g., `delete`) skip the `--output` flag and print a confirmation message directly.

---

## 9. Registry Entry

After the extension is built and published, add to `cli/azd/extensions/registry.json`:

```json
{
  "azure.ai.connection": {
    "displayName": "Foundry connections (Preview)",
    "namespace": "ai.connection",
    "description": "Manage Foundry project connections from your terminal.",
    "versions": {
      "0.1.0-preview": {
        "requiredAzdVersion": ">1.23.13",
        ...
      }
    }
  }
}
```

The actual entry is generated by `azd x publish` against published release artifacts.

---

## 10. Open Items

| # | Item | Spec ref | Blocking? |
|---|------|----------|-----------|
| 1 | ARM endpoint path + api-version for connection CRUD | API Surface row 2 | Yes — client stubs until confirmed |
| 2 | Data-plane endpoint for credential GET | API Surface row 3, Open Q #3 | Yes — `show --show-credentials` stubs |
| 3 | Final `--kind` enum | Open Q #1 | No — warn on unknown |
| 4 | Final auth-type enum | Open Q #2 | No — committed set known |
| 5 | `--from-file` schema version pinning | Open Q #6 | No — accept any initially |
| 6 | Telemetry for coding agents | Open Q #7 | No — follow existing pattern |
| 7 | Key Vault SDK dependency for `--secret-from-keyvault` | Dependencies line 338 | May need 2-PR approach |
| 8 | Resolution order contradiction (Terminology vs AZD Env Scoping) | Lines 127 vs 289 | Flag to spec authors |
