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
OAuth2, UserEntraToken, AgenticIdentity, etc. From E2E test fixtures
(Agents service, `yacflow/tests/fixtures/`), real `RemoteA2A` connections exist with:

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

### 3.2 Full Auth Type Inventory

The ARM Go SDK (`armcognitiveservices` v2.0.0) exposes the following `ConnectionAuthType` constants
and corresponding property structs:

| Auth Type | ARM SDK Constant | SDK Struct | In CLI? | Feasibility |
|-----------|-----------------|------------|---------|-------------|
| `ApiKey` | `ConnectionAuthTypeAPIKey` | `APIKeyAuthConnectionProperties` | ✅ Shipped | — |
| `CustomKeys` | `ConnectionAuthTypeCustomKeys` | `CustomKeysConnectionProperties` | ✅ Shipped | — |
| `None` | `ConnectionAuthTypeNone` | `NoneAuthTypeConnectionProperties` | ✅ Shipped | — |
| **`OAuth2`** | `ConnectionAuthTypeOAuth2` | `OAuth2AuthTypeConnectionProperties` | ❌ Missing | ✅ **SDK struct exists — wire it** |
| **`AAD`** | `ConnectionAuthTypeAAD` | `AADAuthTypeConnectionProperties` | ❌ Missing | ✅ **SDK struct exists — wire it** |
| **`ManagedIdentity`** | `ConnectionAuthTypeManagedIdentity` | `ManagedIdentityAuthTypeConnectionProperties` | ❌ Missing | ✅ **SDK struct exists — wire it** |
| `AccessKey` | `ConnectionAuthTypeAccessKey` | `AccessKeyAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| `AccountKey` | `ConnectionAuthTypeAccountKey` | `AccountKeyAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| `SAS` | `ConnectionAuthTypeSAS` | `SASAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| `ServicePrincipal` | `ConnectionAuthTypeServicePrincipal` | `ServicePrincipalAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| `PAT` | `ConnectionAuthTypePAT` | `PATAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| `UsernamePassword` | `ConnectionAuthTypeUsernamePassword` | `UsernamePasswordAuthTypeConnectionProperties` | ❌ Missing | ✅ SDK struct exists |
| **`UserEntraToken`** | ❌ Not in SDK | ❌ No struct | ❌ Missing | ⚠️ **Requires workaround** |
| **`ProjectManagedIdentity`** | ❌ Not in SDK | ❌ No struct | ❌ Missing | ⚠️ **Requires workaround** |
| `AgenticIdentityToken` | ❌ Not in SDK | ❌ No struct | ❌ Missing | ⚠️ Requires workaround |

### 3.3 Linda's Reported Gaps (Confirmed)

Linda tested the released CLI and confirmed these auth types are not supported:

| Auth Type | Status | What's Needed |
|-----------|--------|---------------|
| **OAuth2** | SDK struct exists (`OAuth2AuthTypeConnectionProperties`) | Add switch case + credential prompts (~30 lines). Needs `--client-id` and `--client-secret` flags. |
| **ProjectManagedIdentity** | ❌ Not in ARM Go SDK v2.0.0 | Raw REST bypass or SDK update. The Agents service treats this as a distinct type from `ManagedIdentity`. No credentials needed (identity-based). |
| **UserEntraToken** | ❌ Not in ARM Go SDK v2.0.0 | Raw REST bypass or SDK update. Connections use an `audience` field (e.g., `"https://mcp.ai.azure.com"`) that existing SDK structs don't have. Needs `--audience` flag. |

### 3.4 Workaround for SDK-Missing Auth Types

For `UserEntraToken` and `ProjectManagedIdentity`, the typed ARM SDK doesn't have structs.
Two approaches:

**Option A: Raw REST bypass** (recommended)
- When `--auth-type` is `user-entra-token` or `project-managed-identity`, bypass the typed SDK
- Build the JSON body manually and POST via `runtime.NewRequest` (same pattern as the
  existing data-plane client in `foundry_projects_client.go`)
- Effort: ~50 lines per auth type

**Option B: Wait for ARM SDK update**
- The ARM Go SDK would need to add `UserEntraTokenAuthTypeConnectionProperties` and
  `ProjectManagedIdentityAuthTypeConnectionProperties` structs
- Timeline: unknown, depends on SDK team

### 3.5 Proposed Auth Type Changes

#### Phase 1 — Immediate (no blockers)

Wire the three highest-priority auth types that already have SDK structs:

```go
case "oauth2":
    → OAuth2AuthTypeConnectionProperties  // needs --client-id, --client-secret
case "aad":
    → AADAuthTypeConnectionProperties     // no credentials, identity-based
case "managed-identity":
    → ManagedIdentityAuthTypeConnectionProperties  // no credentials
```

New CLI flags needed: `--client-id`, `--client-secret` (for OAuth2 only).

#### Phase 2 — Short-term (raw REST workaround)

Add `user-entra-token` and `project-managed-identity` using raw REST:

```go
case "user-entra-token":
    → raw REST body: { authType: "UserEntraToken", audience: <--audience>, ... }
case "project-managed-identity":
    → raw REST body: { authType: "ProjectManagedIdentity", ... }
```

New CLI flag needed: `--audience` (for UserEntraToken only).

---

## 4. Requirement 3: RemoteA2A Kind Alias

### 4.1 Proposed Change

Add a `remote-a2a` alias to `normalizeKind()` in the extension's `connection.go`:

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

Linda also noted there are **two registries** to support:
1. **Asset Catalog** (`api.catalog.azureml.ms`) — MCP tools, models, publishers
2. **Connector Registry** — details TBD (Linda to share)

### 5.2 Asset Catalog API Summary

**API:** `POST https://api.catalog.azureml.ms/asset-gallery/v1.0/tools`  
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

#### Future: Connector Registry

Linda mentioned a second registry (**connector registry**) that should also be supported.
Details TBD — waiting for Linda to share the API surface. The `--from-catalog` architecture
(picker → flag resolution → existing create flow) can be extended to support additional
registries via a `--from-connector-registry` flag or a unified `--from-registry` flag
with a source selector.

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

### Phase 1 — Immediate (no blockers)

| Change | Effort |
|--------|--------|
| Add `remote-a2a` → `RemoteA2A` kind alias + test + help text | 3 lines |
| Wire `--auth-type oauth2` → `OAuth2AuthTypeConnectionProperties` + `--client-id`/`--client-secret` flags | ~30 lines |
| Wire `--auth-type aad` → `AADAuthTypeConnectionProperties` (no credentials needed) | ~15 lines |
| Wire `--auth-type managed-identity` → `ManagedIdentityAuthTypeConnectionProperties` (no credentials needed) | ~15 lines |

**Result:** RemoteA2A kind support + 3 new auth types. Covers most of Linda's reported gaps.

### Phase 2 — Short-term (raw REST workaround)

| Change | Effort |
|--------|--------|
| `--auth-type user-entra-token` via raw REST + `--audience` flag | ~50 lines |
| `--auth-type project-managed-identity` via raw REST | ~50 lines |
| `--from-catalog` interactive picker | ~2-3 days |
| Catalog API client | ~1 day |

### Phase 3 — Future

| Change | Dependency |
|--------|-----------|
| Connector registry integration | Linda to share API surface |
| `--from-catalog <tool-name>` non-interactive mode for CI/CD | After Phase 2 picker ships |
| Remaining auth types (`SAS`, `PAT`, `ServicePrincipal`, etc.) | On demand |

---

## 7. Testing Plan

### RemoteA2A

| Test | Method |
|------|--------|
| `create --kind remote-a2a --auth-type none` | Manual + E2E |
| `create --kind remote-a2a --auth-type custom-keys` | Manual + E2E |
| `create --kind remote-a2a --auth-type oauth2` | Manual + E2E |
| `list --kind remote-a2a` shows RemoteA2A connections | Manual |
| `show` displays RemoteA2A connection details | Manual |
| `delete` removes RemoteA2A connection | Manual |
| `--kind RemoteA2A` (PascalCase, no alias) | Manual — verify passthrough |

### Auth Types

| Test | Method |
|------|--------|
| `create --auth-type oauth2 --client-id X --client-secret Y` | Manual + E2E |
| `create --auth-type aad` (no credentials) | Manual + E2E |
| `create --auth-type managed-identity` (no credentials) | Manual + E2E |
| `create --auth-type user-entra-token --audience X` (Phase 2) | Manual + E2E |
| `create --auth-type project-managed-identity` (Phase 2) | Manual + E2E |

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

1. **Connector registry** — Linda to share the API surface for the second registry.
2. **Auth mapping logic** — Linda to share the UX team's `xMsSecuritySchemes` → auth type mapping.
3. **WorkIQ-specific UX** — Does Linda's team want a dedicated `--kind work-iq` alias, or is `--kind remote-a2a` sufficient?
4. **A2A in catalog** — Will A2A tools eventually appear in the Asset Catalog?
5. **OAuth2 credential flow** — Does OAuth2 need `--client-id`/`--client-secret` flags, or is there a different credential shape?
