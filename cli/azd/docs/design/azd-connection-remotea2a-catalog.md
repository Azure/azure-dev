# Design Spec: `azd ai agent connection` — RemoteA2A Support & Catalog Integration

**Author:** Naman Tyagi  
**Date:** 2026-05-21  
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

Linda raised two follow-up requests:

1. **RemoteA2A / WorkIQ support** — Can we support new tool types like WorkIQ that use `RemoteA2A` connections?
2. **Catalog API integration** — Can we use the Asset Catalog APIs to auto-fill connection creation flags?

---

## 2. Requirement 1: RemoteA2A Connection Support

### 2.1 What is RemoteA2A?

`RemoteA2A` is an ARM-registered `ConnectionCategory` used for **A2A (Agent-to-Agent) protocol** connections. These connections point to remote A2A-compatible agent endpoints. WorkIQ is one such tool that uses this connection type.

**Server-side flow (Agents service):**
```
User creates connection (category=RemoteA2A, target=<a2a-endpoint>)
  → Agent definition references WorkIQPreviewTool(projectConnectionId)
  → Agents service calls GetWorkspaceConnectionWithSecrets()
  → Transforms to remote tool args: { protocol: "a2a", serverLabel, projectConnectionId }
  → Tool server handles A2A protocol dispatch + token acquisition
```

### 2.2 Current State

From E2E test fixtures (`yacflow/tests/fixtures/`), real `RemoteA2A` connections exist with three auth types:

| Auth Type | Example Connection | Target |
|-----------|-------------------|--------|
| `CustomKeys` | `testa2ahelloworld-apikey` | `https://a2a-samples-helloworld-apikey.calmforest-80564e74.eastus2.azurecontainerapps.io` |
| `None` | `testa2a-invalid-agent-card` | `https://a2a-samples-invalidagentcard.calmforest-80564e74.eastus2.azurecontainerapps.io` |
| `OAuth2` | GitHub connector | `https://a2a-samples-helloworld-github.calmforest-80564e74.eastus2.azurecontainerapps.io` |

### 2.3 Feasibility Assessment

| Requirement | Feasible? | Detail |
|------------|-----------|--------|
| `--kind RemoteA2A` | ✅ Yes | ARM already accepts `RemoteA2A` as a valid `ConnectionCategory`. CLI's `normalizeKind()` passes unknown kinds through as-is. |
| `--auth-type custom-keys` | ✅ Yes | Already supported — maps to `CustomKeysConnectionProperties` |
| `--auth-type none` | ✅ Yes | Already supported — maps to `NoneAuthTypeConnectionProperties` |
| `--auth-type oauth2` | ❌ Not yet | ARM Go SDK v2.0.0 does not expose an `OAuth2AuthConnectionProperties` struct |
| Credential injection in `azd ai agent run` | N/A | WorkIQ/A2A is a hosted tool — token acquisition is server-side (Agents service uses `WorkIQA2A` app ID `fdcc1f02-fc51-4226-8753-f668596af7f7`). No local credential injection needed. |

### 2.4 Proposed Changes

#### Phase 1 — Immediate (small, no blockers)

**File: `internal/connections/cmd/connection.go`**

1. **Add `remote-a2a` alias to `normalizeKind()`** (~1 line):
   ```go
   // In the kind normalization map (line 773-782):
   "remote-a2a": "RemoteA2A",
   ```

2. **Add help text** for `--kind` flag to include `remote-a2a` in the list of known kinds.

3. **Add test case** in `connection_test.go:TestNormalizeKind`:
   ```go
   {"remote-a2a", "RemoteA2A"},
   ```

> **Why the alias is required (not just cosmetic):**  
> `connection list --kind remote-a2a` uses `normalizeKind()` to normalize the flag value, then does a **case-sensitive** string comparison against the ARM category. Without the alias, `"remote-a2a" != "RemoteA2A"` and RemoteA2A connections are silently filtered out of list results.
>
> Existing kinds shipped with the command:
> ```
> remote-tool → RemoteTool       (MCP/catalog tools like Tavily)
> cognitive-search → CognitiveSearch
> api-key → ApiKey
> app-insights → AppInsights
> grounding-with-bing-search → GroundingWithBingSearch
> ai-services → AIServices
> container-registry → ContainerRegistry
> custom-keys → CustomKeys
> ```
> `RemoteA2A` is the new category for A2A protocol tools (WorkIQ, generic A2A agents).

**With this alias, the full RemoteA2A UX becomes:**

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

# --- LIST ---

# 3. List only A2A connections
azd ai agent connection list --kind remote-a2a

# --- SHOW ---

# 4. Show details (output: Kind: RemoteA2A)
azd ai agent connection show my-workiq

# 5. Show with credentials
azd ai agent connection show my-workiq --show-credentials

# --- UPDATE ---

# 6. Update target endpoint
azd ai agent connection update my-workiq \
  --target https://new-endpoint.azurecontainerapps.io

# --- DELETE ---

# 7. Delete
azd ai agent connection delete my-workiq --force
```

> **Note:** The UX is identical to `--kind remote-tool` — only the category label differs.
> `list`/`show` output displays `Kind: RemoteA2A` instead of `Kind: RemoteTool`.
> Same code paths, same auth structs, same credential handling.

#### Phase 2 — Future (blocked on ARM SDK)

- **OAuth2 auth type**: Add `--auth-type oauth2` once the ARM Go SDK exposes the `OAuth2AuthConnectionProperties` struct. This would support GitHub-connector-style A2A connections.
- **`--auth-type aad`**: If connections need AAD/Entra ID auth (distinct from OAuth2), this requires a corresponding ARM SDK struct.

### 2.5 Tool Extensibility (Linda's "new tools" question)

**Q: If a new tool type is added (e.g., beyond WorkIQ), does the CLI need an update?**

**A: No, as long as it uses an existing ARM `ConnectionCategory` and auth type.**

| Scenario | Extension update? | Why |
|----------|-------------------|-----|
| New tool using `RemoteA2A` + `CustomKeys` | ❌ No | Kind and auth already supported |
| New tool using `RemoteTool` + `ApiKey` | ❌ No | Kind and auth already supported |
| New tool requiring a brand-new `ConnectionCategory` | ❌ No (CLI) | `--kind` passes through unknown values to ARM. ARM must register the category. |
| New tool requiring a new auth type (e.g., OAuth2) | ✅ Yes | Each auth type needs a specific ARM SDK struct + CLI switch case |

**Key insight:** `--kind` is a passthrough — the CLI forwards whatever value the user provides to ARM. If ARM accepts it, it works. The only hardcoded constraint is `--auth-type`, which maps to typed ARM SDK structs.

---

## 3. Requirement 2: Asset Catalog API Integration

### 3.1 What Linda Asked

> Can we use the catalog APIs to auto-fill `azd ai agent connection create` flags (target URL, auth-type, kind) instead of users typing everything manually?

### 3.2 Catalog API Summary

**API:** `POST https://api.catalog.azureml.ms/asset-gallery/v1.0/tools`  
**Auth:** Anonymous (no auth required)  
**Returns:** Tool name, endpoint URL, auth scheme, description, publisher

### 3.3 Tested Findings

| Field | Catalog API Response | Maps to CLI Flag |
|-------|---------------------|-----------------|
| Tool name | `name` | `<connection-name>` argument |
| Endpoint URL | `versionDetail.remotes.url` (NOT `customProperties.endpoint` — empty for 36/37 tools) | `--target` |
| Auth scheme | `customProperties.xMsSecuritySchemes` | `--auth-type` (needs mapping layer) |
| Kind | All tools are `kind=mcp` | `--kind remote-tool` (all MCP) |

**Current catalog stats (as of testing):**
- 37 tools listed, all `kind=mcp`
- 28/37 have endpoint URLs in `versionDetail.remotes.url`
- 23/37 have auth schemes — but vendor-specific strings (`stripeoauth`, `githuboauth`), not generic `oauth2`/`apiKey`

### 3.4 Proposed Integration: `--from-catalog` Flag

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
  1. GET https://api.catalog.azureml.ms/asset-gallery/v1.0/tools (anonymous)
  2. Present interactive picker (name, description, endpoint)
  3. On selection:
     a. Set --target from versionDetail.remotes.url
     b. Set --kind to RemoteTool (all catalog tools are MCP)
     c. Map xMsSecuritySchemes → --auth-type:
        - Contains "apikey" → --auth-type api-key → prompt for key
        - Contains "oauth"  → --auth-type custom-keys → prompt for keys
        - Empty/none        → --auth-type none
     d. Allow user to override connection name (default: tool name)
  4. Call existing create flow with resolved flags
```

#### Caveats & Risks

| Issue | Impact | Mitigation |
|-------|--------|------------|
| Endpoint URL missing for 9/37 tools | Can't auto-fill `--target` | Prompt user to enter manually; show warning |
| Auth schemes are vendor-specific strings | Can't reliably map to `--auth-type` | Best-effort mapping + manual override prompt |
| Catalog only has MCP tools (no A2A) | `--from-catalog` won't help with WorkIQ/A2A | Document that A2A connections are created manually |
| Catalog API is anonymous/public | Tool list may not match workspace capabilities | Informational only; connection creation still validates against ARM |

#### Future: Catalog adds A2A tools

If A2A tools are added to the Asset Catalog later, there's no architectural conflict:

| Scenario | Impact |
|----------|--------|
| Catalog adds A2A tools with `kind=a2a` | Need a mapping in picker: catalog `a2a` → CLI `RemoteA2A` |
| Catalog adds A2A tools with `kind=mcp` | Would work as `RemoteTool` but wrong category |
| Catalog adds `kind=remote_a2a` | Trivial mapping in the picker |

The `--from-catalog` picker would use a `catalogKindToARMCategory()` function — same pattern as `normalizeKind()`.

### 3.5 `list --kind` Filter Behavior

> **Important implementation detail:** `connection list --kind` uses `normalizeKind()` then does a
> **case-sensitive** comparison against the ARM `Category` field. Without the `remote-a2a` alias,
> `list --kind remote-a2a` silently returns zero results even when RemoteA2A connections exist.
> This is why the alias is **required** — not just for `create`, but for `list` filtering.
>
> `connection list` (no `--kind` flag) always returns all connections — RemoteA2A included.

#### Phase Plan

| Phase | Scope | Effort |
|-------|-------|--------|
| Phase 1 | `--from-catalog` with interactive picker, auto-fills target + kind | ~2-3 days |
| Phase 2 | Auth scheme mapping layer + credential prompting | ~1-2 days |
| Phase 3 | `--from-catalog <tool-name>` non-interactive mode for CI/CD | ~1 day |

---

## 4. Summary of Changes

### Immediate (Phase 1 — No blockers)

| Change | File | Effort |
|--------|------|--------|
| Add `remote-a2a` → `RemoteA2A` alias | `connection.go:normalizeKind()` | 1 line |
| Update `--kind` help text | `connection.go:newConnectionCreateCommand()` | 1 line |
| Add `normalizeKind` test case | `connection_test.go:TestNormalizeKind` | 1 line |

**Result:** Full `RemoteA2A` support for `CustomKeys` and `None` auth types. `list --kind remote-a2a` correctly filters RemoteA2A connections.

### Short-term (Phase 2)

| Change | File | Effort |
|--------|------|--------|
| `--from-catalog` interactive picker | New file: `catalog.go` + `connection.go` | 2-3 days |
| Catalog API client | New file: `internal/connections/pkg/catalog/client.go` | 1 day |

### Future (Blocked on ARM SDK)

| Change | Blocker | Effort when unblocked |
|--------|---------|----------------------|
| `--auth-type oauth2` | ARM Go SDK `OAuth2AuthConnectionProperties` struct | ~20 lines |
| `--auth-type aad` | ARM Go SDK `AADAuthConnectionProperties` struct | ~20 lines |

---

## 5. Testing Plan

### RemoteA2A

| Test | Method |
|------|--------|
| `create --kind remote-a2a --auth-type none` | Manual + E2E |
| `create --kind remote-a2a --auth-type custom-keys` | Manual + E2E |
| `list` shows RemoteA2A connections | Manual |
| `show` displays RemoteA2A connection details | Manual |
| `delete` removes RemoteA2A connection | Manual |
| `--kind RemoteA2A` (PascalCase, no alias) | Manual — verify passthrough |

### Catalog Integration

| Test | Method |
|------|--------|
| Catalog API reachable (anonymous) | Unit test with mock |
| Tool list parsing (name, endpoint, auth) | Unit test |
| Interactive picker renders correctly | Manual |
| Auto-fill creates valid connection | E2E |
| Missing endpoint gracefully handled | Unit test |

---

## 6. Open Questions

1. **OAuth2 auth priority** — How many A2A connections use OAuth2 vs CustomKeys/None? If most are CustomKeys, we can defer OAuth2 indefinitely.
2. **Catalog scope** — Should `--from-catalog` only show tools compatible with the user's workspace region/SKU, or show all 37?
3. **WorkIQ-specific UX** — Does Linda's team want a dedicated `--kind work-iq` alias, or is `--kind remote-a2a` sufficient since WorkIQ is just an A2A tool?
4. **A2A in catalog** — Will A2A tools eventually appear in the Asset Catalog? If so, `--from-catalog` could cover both MCP and A2A tools.
