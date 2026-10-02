# Design Spec: `azd ai agent connection` — RemoteA2A Support, Auth Type Expansion & Catalog Integration

**Author:** Naman Tyagi  
**Date:** 2026-05-21 (updated after review feedback)  
**PR Context:** PR #8174 (`feat: add azd ai agent connection commands + credential resolution in run`)  
**Stakeholder:** Linda (Asset Catalog team)  

---

## 1. Background

PR #8174 shipped the `azd ai agent connection` command suite:

```
azd ai agent connection
├── list      --kind, --output, -p
├── show      --show-credentials, --output, -p
├── create    --kind, --target, --auth-type, --key, --custom-key, --metadata, --force, -p
├── update    --target, --key, --custom-key, -p
└── delete    --force, -p
```

Linda raised follow-up requests:

1. **RemoteA2A / WorkIQ support** — Can we support new tool types like WorkIQ that use `RemoteA2A` connections?
2. **Missing auth types** — `OAuth2`, `ProjectManagedIdentity`, and `UserEntraToken` are not supported in `azd connection create`.
3. **Catalog API integration** — Can we use the Asset Catalog APIs (and connector registry) to auto-fill connection creation flags?

---

## 2. Requirement 1: RemoteA2A Connection Support

### 2.1 What is RemoteA2A?

`RemoteA2A` is an ARM-registered `ConnectionCategory` used for **A2A (Agent-to-Agent) protocol** connections. These connections point to remote A2A-compatible agent endpoints. WorkIQ is one such tool that uses this connection type.

**Server-side flow (Agents service):**
```
User creates connection (category=RemoteA2A, target=<a2a-endpoint>)
  → Agent definition references WorkIQPreviewTool(projectConnectionId)
  → Agents service calls GetWorkspaceConnectionWithSecrets()
  → Transforms to remote_protocol args: { protocol: "a2a", project_connection_id }
  → Tool server handles A2A protocol dispatch + token acquisition
```

> **Note:** The transform produces a `remote_protocol` tool argument (not `remoteTool`).
> Both `WorkIQPreviewTool` and `A2APreviewTool` use the same transform pattern with
> `protocol: "a2a"` and `project_connection_id`.

### 2.2 Current State

`RemoteA2A` connections support the **same auth types as `RemoteTool`**: no auth, custom keys,
OAuth2, UserEntraToken, AgenticIdentity, etc. From E2E test fixtures in the Agents service
repo (vienna: `src/azureml-api/src/Agents/scripts/yacflow/tests/fixtures/`), real `RemoteA2A`
connections exist with:

| Auth Type | Example Connection | Target |
|-----------|-------------------|--------|
| `CustomKeys` | `testa2ahelloworld-apikey` | `https://a2a-samples-helloworld-apikey.calmforest-80564e74.eastus2.azurecontainerapps.io` |
| `None` | `testa2a-invalid-agent-card` | `https://a2a-samples-invalidagentcard.calmforest-80564e74.eastus2.azurecontainerapps.io` |
| `OAuth2` | GitHub connector | `https://a2a-samples-helloworld-github.calmforest-80564e74.eastus2.azurecontainerapps.io` |

---

## 3. Requirement 2: Auth Type Expansion

### 3.1 Current CLI Auth Support

The CLI currently supports **3 of 12+** auth types available in the ARM SDK:

```go
switch authType {
case "api-key":     → APIKeyAuthConnectionProperties
case "custom-keys": → CustomKeysConnectionProperties
case "none", "":    → NoneAuthTypeConnectionProperties
default:            → error: "Unsupported auth type"
}
```

### 3.2 Allowed Auth Types for RemoteTool / RemoteA2A

ARM explicitly validates auth types per connection kind. For `RemoteTool` and `RemoteA2A`
connections, the **server-accepted** auth types are (from ARM 400 error response):

```
None, CustomKeys, ProjectManagedIdentity, OAuth2, DeveloperConnection,
UserEntraToken, AgentUserImpersonation, AgenticIdentityToken, AgenticUser,
UserTokenAndProjectManagedIdentity
```

> **Note:** `AAD` and `ManagedIdentity` (from the ARM Go SDK) are **NOT** in this list.
> They are valid for other connection kinds (e.g., `AzureOpenAI`, `ContainerRegistry`)
> but are **rejected by ARM** for `RemoteTool` and `RemoteA2A` connections.

### 3.3 POC Test Results

We built and tested a POC branch with new auth type support against a live workspace
(`hosted-agents-bugbash` in `northcentralus`). Results:

**Round 1 — ARM SDK typed structs:**

| Test | Kind | Auth Type | Method | Result |
|------|------|-----------|--------|--------|
| RemoteA2A + None | `remote-a2a` | `none` | ARM SDK | ✅ Created, listed, shown, deleted |
| RemoteA2A + CustomKeys | `remote-a2a` | `custom-keys` | ARM SDK | ✅ Created, credentials visible |
| OAuth2 | `remote-tool` | `oauth2` | ARM SDK | ✅ Created with `--client-id`/`--client-secret` |
| AAD | `remote-tool` | `aad` | ARM SDK | ❌ ARM rejects for RemoteTool/RemoteA2A |
| ManagedIdentity | `remote-tool` | `managed-identity` | ARM SDK | ❌ ARM rejects (maps to `RegistryIdentity`) |
| `list --kind remote-a2a` | — | — | ARM SDK | ✅ Correctly filters RemoteA2A connections only |

**Round 2 — Raw REST (`az rest --method PUT`):**

| Test | Kind | Auth Type | Method | Result |
|------|------|-----------|--------|--------|
| AgenticIdentityToken | `RemoteTool` | `AgenticIdentityToken` | Raw REST | ✅ Created, shown, deleted |
| AgenticIdentityToken | `RemoteA2A` | `AgenticIdentityToken` | Raw REST | ✅ Works on both kinds |
| UserEntraToken | `RemoteTool` | `UserEntraToken` | Raw REST | ✅ Created, `audience` field stored correctly |
| ProjectManagedIdentity | `RemoteTool` | `ProjectManagedIdentity` | Raw REST | ✅ Created, no credentials needed |
| AgenticIdentity | `RemoteTool` | `AgenticIdentity` | Raw REST | ❌ ARM rejects — correct name is `AgenticIdentityToken` |

> **Important finding:** The `agent.yaml` schema uses `AgenticIdentity` as the auth type name,
> but ARM expects `AgenticIdentityToken`. The CLI should accept both and normalize to
> `AgenticIdentityToken` when calling ARM.

### 3.4 Full Auth Type Inventory

Based on POC testing and ARM validation, here is the corrected auth type inventory
for `RemoteTool` / `RemoteA2A` connections:

| Auth Type | ARM-Accepted? | In ARM Go SDK v2.0.0? | In CLI? | POC Tested? | Feasibility |
|-----------|:---:|:---:|:---:|:---:|-------------|
| `None` | ✅ | ✅ `NoneAuthTypeConnectionProperties` | ✅ Shipped | ✅ Pass | — |
| `CustomKeys` | ✅ | ✅ `CustomKeysConnectionProperties` | ✅ Shipped | ✅ Pass | — |
| `ApiKey` | ✅ | ✅ `APIKeyAuthConnectionProperties` | ✅ Shipped | — | — |
| **`OAuth2`** | ✅ | ✅ `OAuth2AuthTypeConnectionProperties` | ✅ **POC done** | ✅ Pass | **Ship it** |
| **`AgenticIdentityToken`** | ✅ | ❌ No struct | ❌ | ✅ Pass (raw REST) | ⚠️ Raw REST bypass |
| **`ProjectManagedIdentity`** | ✅ | ❌ No struct | ❌ | ✅ Pass (raw REST) | ⚠️ Raw REST bypass |
| **`UserEntraToken`** | ✅ | ❌ No struct | ❌ | ✅ Pass (raw REST, audience stored) | ⚠️ Raw REST bypass + `--audience` flag |
| `DeveloperConnection` | ✅ | ❌ No struct | ❌ | — | ⚠️ Raw REST bypass |
| `AgentUserImpersonation` | ✅ | ❌ No struct | ❌ | — | ⚠️ Raw REST bypass |
| `AgenticUser` | ✅ | ❌ No struct | ❌ | — | ⚠️ Raw REST bypass |
| `UserTokenAndProjectManagedIdentity` | ✅ | ❌ No struct | ❌ | — | ⚠️ Raw REST bypass |
| `AAD` | ❌ Rejected | ✅ Has struct | ❌ | ❌ Fail | **Not applicable** for RemoteTool/RemoteA2A |
| `ManagedIdentity` | ❌ Rejected | ✅ Has struct | ❌ | ❌ Fail | **Not applicable** for RemoteTool/RemoteA2A |
| `AgenticIdentity` | ❌ Rejected | ❌ No struct | ❌ | ❌ Fail | ARM expects `AgenticIdentityToken` instead |

### 3.5 Linda's Reported Gaps — Validated

Linda tested the released CLI and confirmed these auth types are not supported:

| Auth Type | Status | What's Needed |
|-----------|--------|---------------|
| **OAuth2** | ✅ **POC validated — works** | Wire `OAuth2AuthTypeConnectionProperties` + `--client-id`/`--client-secret` flags (~30 lines) |
| **ProjectManagedIdentity** | ✅ **POC validated via raw REST** | Raw REST bypass. No credentials needed (identity-based). ~50 lines |
| **UserEntraToken** | ✅ **POC validated via raw REST** | Raw REST bypass + `--audience` flag. ~50 lines |
| **AgenticIdentityToken** | ✅ **POC validated via raw REST** | Raw REST bypass. No credentials needed. ~50 lines. Note: `agent.yaml` schema uses `AgenticIdentity` but ARM expects `AgenticIdentityToken` — CLI should normalize. |

### 3.6 Workaround for SDK-Missing Auth Types

For `UserEntraToken`, `ProjectManagedIdentity`, and `AgenticIdentityToken`, the typed ARM SDK
doesn't have structs. **POC validated that raw REST works for all three.**

**Approach: Raw REST bypass** (POC validated ✅)
- When `--auth-type` is `user-entra-token`, `project-managed-identity`, or `agentic-identity`,
  bypass the typed SDK
- Build the JSON body manually and PUT via `runtime.NewRequest` (same pattern as the
  existing data-plane client in `cli/azd/extensions/azure.ai.agents/internal/pkg/azure/foundry_projects_client.go`)
- Effort: ~50 lines per auth type (shared helper for the raw REST call)
- The CLI should normalize `agentic-identity` → `AgenticIdentityToken` (not `AgenticIdentity`)

### 3.7 Connector Registry = Asset Catalog

Linda clarified that the "connector registry" is the **same Asset Catalog API**
(`api.catalog.azureml.ms/asset-gallery/v1.0`). Connectors are OAuth2-managed MCP servers
in the Foundry Tools Catalog. When a user adds a connector:

1. **Browse** — find the connector in the Tools Catalog
2. **Connect** — authenticate (OAuth2 consent flow, API key, or none)
3. **Select actions** — choose which connector actions to expose as MCP tools
4. **Add tool** — Foundry creates a managed MCP server in the project's Connector Namespace

The connection created has a `connectorName` field linking it to the managed MCP server.
The `--from-catalog` picker should expose connectors alongside MCP tools, using the
`connectorName` property to distinguish managed connectors from self-hosted MCP servers.

### 3.7 Proposed Auth Type Changes

#### Phase 1 — Immediate (POC validated, no blockers)

Wire OAuth2 — the only high-priority auth type that has an SDK struct AND is accepted by ARM
for RemoteTool/RemoteA2A:

```go
case "oauth2":
    → OAuth2AuthTypeConnectionProperties  // needs --client-id, --client-secret
```

New CLI flags: `--client-id`, `--client-secret`.

> **Note:** `AAD` and `ManagedIdentity` were initially planned for Phase 1 but **POC testing
> confirmed ARM rejects them** for RemoteTool/RemoteA2A connections. They are valid for other
> connection kinds only.

#### Phase 2 — Short-term (POC validated, raw REST workaround)

Add identity-based auth types using raw REST (all POC validated ✅):

```go
case "user-entra-token":
    → raw REST PUT: { authType: "UserEntraToken", audience: <--audience>, ... }
case "project-managed-identity":
    → raw REST PUT: { authType: "ProjectManagedIdentity", ... }
case "agentic-identity":
    → raw REST PUT: { authType: "AgenticIdentityToken", ... }  // normalize name
```

New CLI flag: `--audience` (for UserEntraToken; also used by AgenticIdentityToken).

---

## 4. Requirement 3: RemoteA2A Kind Alias

### 4.1 Proposed Change

Add a `remote-a2a` alias to `normalizeKind()` in
`cli/azd/extensions/azure.ai.agents/internal/connections/cmd/connection.go`:

```go
"remote-a2a": "RemoteA2A",
```

Plus a test case and help text update.

### 4.2 Why the Alias is Required (Not Cosmetic)

`connection list --kind` uses `normalizeKind()` then does a **case-sensitive** string comparison
against the ARM `Category` field. Without the alias, `"remote-a2a" != "RemoteA2A"` and
RemoteA2A connections are silently filtered out. This affects **any kind without an alias** —
all existing kinds (`remote-tool`, `cognitive-search`, etc.) have aliases and work correctly.

Existing kinds shipped with the command:
```
remote-tool              → RemoteTool       (MCP/catalog tools)
cognitive-search         → CognitiveSearch
api-key                  → ApiKey
app-insights             → AppInsights
grounding-with-bing-search → GroundingWithBingSearch
ai-services              → AIServices
container-registry       → ContainerRegistry
custom-keys              → CustomKeys
```

### 4.3 Full RemoteA2A UX

```bash
# --- CREATE ---

# 1. API-key-based A2A agent (e.g., WorkIQ with API key)
azd ai agent connection create my-workiq \
  --kind remote-a2a \
  --target https://a2a-samples-helloworld-apikey.calmforest-80564e74.eastus2.azurecontainerapps.io \
  --auth-type custom-keys \
  --custom-key "api-key=xxx"

# 2. No-auth A2A agent (public endpoint)
azd ai agent connection create my-a2a-agent \
  --kind remote-a2a \
  --target https://a2a-samples-helloworld.calmforest-80564e74.eastus2.azurecontainerapps.io \
  --auth-type none

# 3. OAuth2 A2A agent (once wired)
azd ai agent connection create my-github-a2a \
  --kind remote-a2a \
  --target https://a2a-samples-helloworld-github.calmforest-80564e74.eastus2.azurecontainerapps.io \
  --auth-type oauth2 \
  --client-id <id> --client-secret <secret>

# --- LIST ---

# 4. List only A2A connections
azd ai agent connection list --kind remote-a2a

# --- SHOW ---

# 5. Show details (output: Kind: RemoteA2A)
azd ai agent connection show my-workiq

# 6. Show with credentials
azd ai agent connection show my-workiq --show-credentials

# --- UPDATE ---

# 7. Update target endpoint
azd ai agent connection update my-workiq \
  --target https://new-endpoint.azurecontainerapps.io

# --- DELETE ---

# 8. Delete
azd ai agent connection delete my-workiq --force
```

> **Note:** The UX is identical to `--kind remote-tool` — only the category label differs.
> `list`/`show` output displays `Kind: RemoteA2A` instead of `Kind: RemoteTool`.
> Same code paths, same auth structs, same credential handling.

### 4.4 Tool Extensibility

**Q: If a new tool type is added (e.g., beyond WorkIQ), does the CLI need an update?**

**A: No, as long as it uses an existing ARM `ConnectionCategory` and auth type.**

| Scenario | Extension update? | Why |
|----------|-------------------|-----|
| New tool using `RemoteA2A` + `CustomKeys` | ❌ No | Kind and auth already supported |
| New tool using `RemoteTool` + `ApiKey` | ❌ No | Kind and auth already supported |
| New tool requiring a new `ConnectionCategory` | ❌ No (CLI) | `--kind` passes through to ARM |
| New tool requiring a new auth type | ✅ Yes | Each auth type needs a CLI switch case |

---

## 5. Requirement 4: Catalog & Connector Registry Integration

### 5.1 What Linda Asked

> Can we use the catalog APIs to auto-fill `azd ai agent connection create` flags (target URL,
> auth-type, kind) instead of users typing everything manually?

Linda also noted there are **two registries** to support — both served by the same
Asset Catalog API (`api.catalog.azureml.ms`):
1. **MCP Tools** — self-hosted MCP servers (e.g., Tavily, GitHub MCP)
2. **Managed Connectors** — OAuth2-managed MCP servers provisioned in the Foundry
   Connector Namespace (e.g., GitHub connector, Slack, Jira)

### 5.2 Asset Catalog API Summary

**API:** `POST https://api.catalog.azureml.ms/asset-gallery/v1.0/tools` (listing uses POST with filter body)  
**Auth:** Anonymous (no auth required)  
**Returns:** Tool name, endpoint URL, auth scheme, description, publisher

### 5.3 Tested Findings

| Field | Catalog API Response | Maps to CLI Flag |
|-------|---------------------|-----------------|
| Tool name | `name` | `<connection-name>` argument |
| Endpoint URL | `versionDetail.remotes.url` (NOT `customProperties.endpoint` — empty for 36/37 tools) | `--target` |
| Auth scheme | `customProperties.xMsSecuritySchemes` | `--auth-type` (needs mapping layer) |
| Kind | All tools are `kind=mcp` | `--kind remote-tool` (all MCP) |

**Current catalog stats (as of testing):**
- 37 tools listed, all `kind=mcp` — these are remote MCP servers customers host
- 28/37 have endpoint URLs in `versionDetail.remotes.url`
- 23/37 have auth schemes — vendor-specific strings (`stripeoauth`, `githuboauth`), not
  generic `oauth2`/`apiKey`. Linda noted the UX team has implemented logic to map these and
  will share.

### 5.4 Proposed Integration: `--from-catalog` Flag

#### UX Design

```bash
# Interactive picker
azd ai agent connection create --from-catalog

# Displays:
#   Select a tool from the catalog:
#   > tavily-search     Tavily web search API          https://mcp.tavily.com
#     github-mcp        GitHub MCP server               https://api.github.com/mcp
#     stripe-mcp        Stripe payment processing       https://mcp.stripe.com
#     ...
#
# After selection, auto-fills --kind, --target, and prompts for auth:
#   Selected: tavily-search
#   Target: https://mcp.tavily.com (from catalog)
#   Auth type detected: api-key
#   Enter API key: ********
#
#   ✅ Created connection "tavily-search" (kind=RemoteTool)
```

#### Implementation

```
azd ai agent connection create --from-catalog
  1. Fetch tool list from catalog API (anonymous)
  2. Present interactive picker (name, description, endpoint)
  3. On selection:
     a. Set --target from versionDetail.remotes.url
     b. Set --kind to RemoteTool (all catalog tools are MCP)
     c. Map xMsSecuritySchemes → --auth-type (using UX team's mapping logic)
     d. Allow user to override connection name (default: tool name)
  4. Call existing create flow with resolved flags
```

#### Caveats & Risks

| Issue | Impact | Mitigation |
|-------|--------|------------|
| Endpoint URL missing for 9/37 tools | Can't auto-fill `--target` | Prompt user to enter manually; show warning |
| Auth schemes are vendor-specific strings | Can't reliably map to `--auth-type` | Use UX team's mapping logic (Linda to share) |
| Catalog only has MCP tools (no A2A) | `--from-catalog` won't help with WorkIQ/A2A | Expected — A2A connections are created manually |
| Catalog API is anonymous/public | Tool list may not match workspace capabilities | Expected — connection creation still validates against ARM |

#### Managed Connectors

The same catalog API serves managed connectors (OAuth2-based). When integrated into
`--from-catalog`, the picker should:
- Show connectors alongside MCP tools (distinguished by `connectorName` field)
- For OAuth2 connectors: open browser for consent flow, then create connection with
  `--auth-type oauth2` + `connectorName`
- For no-auth connectors: create directly with `--auth-type none`

> **CLI limitation:** The full connector flow (browse → OAuth consent → select actions →
> add tool) involves browser-based OAuth and action selection UX. The CLI can handle
> browse + create, but the OAuth consent and action selection may need to delegate to
> a browser or portal URL.

#### Future: Catalog adds A2A tools

If A2A tools are added to the Asset Catalog later, there's no architectural conflict:

| Scenario | Impact |
|----------|--------|
| Catalog adds A2A tools with `kind=a2a` | Need a mapping in picker: catalog `a2a` → CLI `RemoteA2A` |
| Catalog adds A2A tools with `kind=mcp` | Would work as `RemoteTool` but wrong category |
| Catalog adds `kind=remote_a2a` | Trivial mapping in the picker |

The `--from-catalog` picker would use a `catalogKindToARMCategory()` function — same
pattern as `normalizeKind()`.

---

## 6. Summary of Changes

### Phase 1 — Immediate (POC validated, no blockers)

| Change | Effort | POC Status |
|--------|--------|------------|
| Add `remote-a2a` → `RemoteA2A` kind alias + test + help text | 3 lines | ✅ Tested |
| Wire `--auth-type oauth2` → `OAuth2AuthTypeConnectionProperties` + `--client-id`/`--client-secret` flags | ~30 lines | ✅ Tested |

**Result:** RemoteA2A kind support + OAuth2 auth. Covers the most common Linda-reported gaps.

### Phase 2 — Short-term (POC validated, raw REST workaround)

| Change | Effort |
|--------|--------|
| `--auth-type user-entra-token` via raw REST + `--audience` flag | ~50 lines |
| `--auth-type project-managed-identity` via raw REST | ~50 lines |
| `--auth-type agentic-identity` via raw REST (normalized to `AgenticIdentityToken`) | ~50 lines |
| Shared raw REST helper for ARM PUT | ~30 lines (reused by all three) |
| `--from-catalog` interactive picker (covers MCP tools + managed connectors) | ~2-3 days |
| Catalog API client | ~1 day |

### Phase 3 — Future

| Change | Dependency |
|--------|-----------|
| Connector registry integration | Linda to share API surface |
| `--from-catalog <tool-name>` non-interactive mode for CI/CD | After Phase 2 picker ships |
| Remaining auth types (`SAS`, `PAT`, `ServicePrincipal`, etc.) | On demand |

---

## 7. Testing Plan

### RemoteA2A (POC validated ✅)

| Test | Method | POC Result |
|------|--------|------------|
| `create --kind remote-a2a --auth-type none` | Manual + E2E | ✅ Pass |
| `create --kind remote-a2a --auth-type custom-keys` | Manual + E2E | ✅ Pass |
| `create --kind remote-a2a --auth-type oauth2` | Manual + E2E | ✅ Pass |
| `list --kind remote-a2a` shows RemoteA2A connections | Manual | ✅ Pass |
| `show` displays RemoteA2A connection details | Manual | ✅ Pass |
| `delete` removes RemoteA2A connection | Manual | ✅ Pass |
| `--kind RemoteA2A` (PascalCase, no alias) | Manual — verify passthrough | ✅ Pass |

### Auth Types

| Test | Method | POC Result |
|------|--------|------------|
| `create --auth-type oauth2 --client-id X --client-secret Y` | ARM SDK | ✅ Pass — credentials stored as `clientid`/`clientsecret` |
| `create --auth-type agentic-identity` | Raw REST | ✅ Pass — normalized to `AgenticIdentityToken` |
| `create --auth-type user-entra-token --audience X` | Raw REST | ✅ Pass — audience stored correctly |
| `create --auth-type project-managed-identity` | Raw REST | ✅ Pass — no credentials needed |
| `create --auth-type aad` | ARM SDK | ❌ ARM rejects for RemoteTool/RemoteA2A — **not applicable** |
| `create --auth-type managed-identity` | ARM SDK | ❌ ARM rejects for RemoteTool/RemoteA2A — **not applicable** |

### Catalog Integration

| Test | Method |
|------|--------|
| Catalog API reachable (anonymous) | Unit test with mock |
| Tool list parsing (name, endpoint, auth) | Unit test |
| Interactive picker renders correctly | Manual |
| Auto-fill creates valid connection | E2E |
| Missing endpoint gracefully handled | Unit test |

---

## 8. Open Questions

1. **Auth mapping logic** — @lindazqli to share the UX team's `xMsSecuritySchemes` → auth type mapping.
2. **WorkIQ-specific UX** — Does Linda's team want a dedicated `--kind work-iq` alias, or is `--kind remote-a2a` sufficient?
3. **A2A in catalog** — Will A2A tools eventually appear in the Asset Catalog?
4. **OAuth2 credential shape** — POC used `--client-id`/`--client-secret`. Are other OAuth2 fields needed (e.g., `--auth-url`, `--tenant-id`)?
5. **UserEntraToken audience values** — What are the common `--audience` values? (e.g., `https://mcp.ai.azure.com`)
6. **AgenticIdentity vs AgenticIdentityToken** — The `agent.yaml` schema uses `AgenticIdentity` but ARM expects `AgenticIdentityToken`. Should we align the schema, or just normalize in the CLI?
7. **Connector picker UX** — For managed connectors, the flow is browse → connect (OAuth consent) → select actions → add. How much of this can happen in a CLI context vs portal?
