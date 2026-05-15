<!-- cspell:ignore foundry toolbox toolboxes toolsets exterrors retarget touchpoints -->

# Design Spec: `azd ai agent toolbox` Direct Commands

## 1. Summary

This spec covers the toolbox CRUD surface for the agents extension:

- `azd ai agent toolbox create | update | delete | show | list` — manage versioned toolboxes against a Foundry project.
- `azd ai agent toolbox connection add | remove | list` — manage the connection-backed tools attached to a toolbox (MCP servers, Azure AI Search; tool shape inferred from the connection's ARM category — see § 5.6).

A Foundry **toolbox** is a versioned, named collection of connection-backed tools that an agent references at run time. Each version carries a `tools[]` array of MCP tools (for `RemoteTool` connections) and Azure AI Search tools (for `CognitiveSearch` connections), each pointing at an existing project connection.

## 2. Scope and Non-Goals

In scope:

- The eight verbs listed in § 1.
- Cross-cutting flags (`--output table|json`, `--no-prompt`, `--debug`, `--project-endpoint`) on every new command.
- Extending the existing `FoundryToolboxClient` (`cli/azd/extensions/azure.ai.agents/internal/pkg/azure/foundry_toolsets_client.go`) with the additional methods the CLI surface needs but the agent runtime does not (§ 5). The client's existing methods (`CreateToolboxVersion`, `GetToolbox`, `DeleteToolbox`) and its pipeline are reused as-is.

Out of scope:

- A new top-level extension. Toolbox commands live inside the existing `azure.ai.agents` extension.
- Authoring built-in tools (`code_interpreter`, `web_search`, `file_search`) into toolboxes. Built-ins are wired on the agent, not via toolboxes (issue #8143). The CLI carries through any built-in entries already present on a fetched version (§ 5.6) but provides no verb to add or remove them.
- Config-driven orchestration / `azd up` for toolboxes.
- Bicep / ARM template authoring for toolboxes.
- Cross-project toolbox copy / clone.

## 3. Extension Placement

The `toolbox` subtree is added under the existing `azure.ai.agents` extension. No new module and no change to `registry.json`. The agents extension registers its root as `agent`, so toolbox commands surface as `azd ai agent toolbox …`.

### 3.1 Modular Layout

All `internal/...` paths in §§ 3, 5, 8, 9, and 11 are inside `cli/azd/extensions/azure.ai.agents/`.

1. All toolbox command files live under `internal/cmd/toolbox*.go`. No toolbox logic is added to existing command files.
2. All toolbox data-plane access goes through the existing `FoundryToolboxClient` in `internal/pkg/azure/foundry_toolsets_client.go`. The new CLI verbs append the missing methods (`ListToolboxes`, `GetToolboxVersion`, `ListToolboxVersions`, `DeleteToolboxVersion`, `SetDefaultVersion`, plus the local-state helper `RegisterPending`) to this same client. The pipeline (auth scope, `Foundry-Features` header) is reused as-is.
3. Imports are one-way: `cmd/toolbox*.go` → `internal/pkg/azure` (`FoundryToolboxClient`) → `internal/exterrors`. Shared helpers carry a `// SHARED: <reason>` comment.
4. Toolbox commands do not call into other `internal/cmd/*.go` files (besides shared helpers). This avoids creating reverse dependencies that would block a future lift-out.

### 3.2 Shared Code Touchpoints

| Shared piece | Location | Reason |
| --- | --- | --- |
| Endpoint resolver (`resolveAgentEndpoint`) | `cli/azd/extensions/azure.ai.agents/internal/cmd/agent_context.go` | Used by every agent command and by toolbox commands; resolves via the 5-level cascade defined by the project-context spec. |
| Confirmation prompt (`confirmDestructive` helper, wrapping `azdClient.Prompt().Confirm`) | `cli/azd/extensions/azure.ai.agents/internal/cmd/` | Used by `toolbox delete` and per-version delete. |
| `azdext.ExtensionContext` (`OutputFormat`, `NoPrompt`, `Prompt()`) | azd host gRPC | Standard extension surface. |
| Credential factory (`azidentity.NewAzureDeveloperCLICredential`) | stdlib wrapper | Same credential used by agent run. |
| Foundry data-plane client (`FoundryToolboxClient`) | `cli/azd/extensions/azure.ai.agents/internal/pkg/azure/foundry_toolsets_client.go` | Implements the data-plane methods listed in § 3.1 with pipeline scope `https://ai.azure.com/.default` and header `Foundry-Features: Toolboxes=V1Preview`. |
| `exterrors` package | `internal/exterrors/` | Shared error model and gRPC classification. |

## 4. API Surfaces

Data plane: scope `https://ai.azure.com/.default`, header `Foundry-Features: Toolboxes=V1Preview`, `api-version=v1`.

### 4.1 Endpoints

| HTTP + Path | Status | Notes |
| --- | --- | --- |
| `GET /toolboxes` | 200 | OpenAI-style list of `{object:"toolbox", id, name, default_version}`. |
| `GET /toolboxes/{name}` | 200 | Metadata only (`id, name, default_version`); no version body. |
| `GET /toolboxes/{name}/versions` | 200 | List `toolbox.version` summaries; used to enumerate versions before per-version delete. |
| `GET /toolboxes/{name}/versions/{version}` | 200 | Full `toolbox.version`: `{id, name, version, created_at, description?, metadata, tools[]}`. |
| `POST /toolboxes/{name}/versions` | 200 | Single mutation endpoint for tools. Auto-creates the parent toolbox if absent. Publishes a new immutable version with the complete tool list — no partial updates. Returned version is automatically `default_version` only on first creation; subsequent versions require an explicit PATCH. |
| `PATCH /toolboxes/{name}` | 200 | Only `default_version` is patchable (`{"default_version":"<n>"}`); other fields → 400. Used to flip the active version after publishing a new one. |
| `DELETE /toolboxes/{name}` | 204 / 404 | Cascades to all versions. Not idempotent — 404 on missing; CLI swallows 404. |
| `DELETE /toolboxes/{name}/versions/{version}` | 204 / 400 | Refuses to delete `default_version` when other versions exist (returns 400 with `bad_request`). If the default is the only remaining version, the service deletes it and cascades — the parent toolbox is removed. |
| `{endpoint}/toolboxes/{name}/versions/{version}/mcp?api-version=v1` | n/a | MCP server exposed by the service for runtime tool consumption, scoped to a specific version. The CLI does not call this URL; it surfaces the computed path on `show` (§ 5.4). |

### 4.2 Request-Body Validation (POST `/toolboxes/{name}/versions`)

- `tools` is required and non-empty (empty array → 400).
- `tools[]` is the whole tool set on every POST. To add or remove a tool, fetch the current default version's `tools[]`, mutate the in-memory array, and POST the complete result.
- `tool.name` must match `^[A-Za-z0-9_-]+$`.
- Supported `type` values: `mcp`, `azure_ai_search`, `file_search`, `code_interpreter`, `web_search`. Built-ins and connection-backed tools coexist freely.
- For `mcp`, `azure_ai_search`, and similar connection-backed entries, `project_connection_id` must be the full ARM resource ID (e.g. `/subscriptions/.../accounts/{account}/projects/{project}/connections/{name}`), not the short name. The CLI resolves names to ARM IDs before POST.

## 5. Command Behavior

### 5.1 `azd ai agent toolbox create <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--description` | string | "" | Optional description recorded with the toolbox. |
| `--project-endpoint` | string | resolver | See § 6. No `-p` short form (taken by `agent run` / `agent invoke`). |
| `--output` | enum | `table` | `table` \| `json`. |
| `--no-prompt` | bool | `false` | Cross-cutting. |
| `--debug` | bool | `false` | Cross-cutting. |

Behavior:

1. Resolve the project endpoint (§ 6).
2. `GET /toolboxes/{name}` to determine if the toolbox already exists.
3. `create` does not issue a POST. The service requires a non-empty `tools[]` on the first POST (§ 4.2), so `create` records a local **pending-toolbox** entry under `extensions.ai-agents.pending-toolboxes.<endpointHash>.items.<name>` (see § 7) containing `{description, createdAt}`. The first subsequent `connection add` reads this record, POSTs v1 with the first tool entry, and clears the record (§ 5.6).
4. Output:
   - New name: `Registered toolbox <name> (pending tools). Run 'azd ai agent toolbox connection add <name> <connection>' to publish v1.`
   - Existing name: `Toolbox <name> already exists. Run 'connection add' to publish a new version, or 'update --default-version <n>' to retarget.`

`--force` is not exposed on `create`.

### 5.2 `azd ai agent toolbox update <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--default-version` | string, required | — | Version string to mark as default. |
| `--output` / `--no-prompt` / `--debug` / `--project-endpoint` | — | — | Cross-cutting. |

Behavior:

1. `PATCH /toolboxes/{name}` with `{"default_version":"<n>"}`.
2. Description and metadata edits are not supported (service only accepts `default_version` on PATCH). To change description or tools, publish a new version via `connection add` / `connection remove`.
3. Missing `--default-version` → `exterrors.Validation(CodeMissingUpdateField, "No fields to update. Specify --default-version.")`.

### 5.3 `azd ai agent toolbox delete <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--version` | string | "" | Delete a single version instead of the whole toolbox. |
| `--force` | bool | `false` | Skip confirmation prompt. |
| `--output` / `--no-prompt` / `--debug` / `--project-endpoint` | — | — | Cross-cutting. |

Behavior:

| Scenario | Action |
| --- | --- |
| `--version` absent, toolbox exists on the service | `DELETE /toolboxes/{name}`. 404 is swallowed. Also clears any local pending-toolbox record for the same name and endpoint. |
| `--version` absent, **pending-only** (no GET match on the service) | Non-network: clears the local pending-toolbox record and reports `Cleared pending toolbox <name>.` |
| `--version <n>`, `<n>` is not `default_version` | `DELETE /toolboxes/{name}/versions/{n}`. |
| `--version <n>`, `<n>` is `default_version`, other versions exist | `exterrors.Validation(CodeDefaultVersionDelete, ...)` with suggestion *"Retarget the default with `azd ai agent toolbox update --default-version <other>` first."* The CLI determines this by listing versions before issuing DELETE; the server would otherwise return 400 with the same message. |
| `--version <n>`, `<n>` is `default_version`, **only remaining version** | The service deletes the version and cascades to remove the parent toolbox. To avoid surprise destruction, the CLI **rejects** this case unless `--force` is set, returning `exterrors.Validation(CodeOnlyVersionDelete, "Version <n> is the only remaining version; deleting it removes the toolbox.")` with suggestion *"Run `azd ai agent toolbox delete <name>` to delete the toolbox, or pass `--force` to confirm."* With `--force`, the CLI proceeds and reports `Deleted toolbox <name> (last version removed).` |
| `--no-prompt` without `--force` | `exterrors.Validation(CodeMissingForceFlag, ...)`. |

Confirmation is shown by default; `--force` skips it.

### 5.4 `azd ai agent toolbox show <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--version` | string | "" | Specific version. Default: server's `default_version`. |
| `--output` / `--no-prompt` / `--debug` / `--project-endpoint` | — | — | Cross-cutting. |

Behavior:

1. `GET /toolboxes/{name}` for `default_version`.
2. If the GET returns 404, check the pending-toolbox config store (§ 7) for an entry matching the resolved endpoint and `<name>`:
   - **Hit** → emit a *pending-toolbox view* (§ 5.4.1). `--version` is rejected with a `CodeMissingUpdateField`-style validation error (no versions exist yet).
   - **Miss** → propagate the 404 as `ErrToolboxNotFound`.
3. Otherwise (toolbox exists on the service): `GET /toolboxes/{name}/versions/{version}` (or `default_version` when `--version` is absent) for the body.
4. Compute the toolbox's MCP consumption URL as `{projectEndpoint}/toolboxes/{name}/versions/{shown_version}/mcp?api-version=v1`, where `shown_version` is the `--version` arg or the server's `default_version`.
5. Output:
   - Table: `Name`, `Default version`, `Shown version`, `Description`, `Endpoint`, `Tools` (count + list with `(builtin)` / `(connection:<id>)` annotation).
   - JSON: `{ "toolbox": <ToolboxObject>, "version": <ToolboxVersionObject>, "endpoint": "<mcp-url>" }`.

#### 5.4.1 Pending-toolbox view

When `show` resolves to a pending record (no service-side toolbox yet), output reflects the local state only:

- Table: `Name`, `State: pending`, `Description`, `Created` (RFC3339 timestamp from the pending record), and a follow-up line: *"Run `azd ai agent toolbox connection add <name> <connection>` to publish v1."*
- JSON: `{ "toolbox": { "name": "...", "pending": true, "description": "...", "createdAt": "..." }, "version": null, "endpoint": null }`. Consumers see `pending: true` as the trigger to use the follow-up command rather than treat the response as a live toolbox.

#### 5.4.2 Runtime consumption

The `endpoint` field is the contract for wiring a toolbox into agent code via the active azd environment:

```bash
azd env set TOOLBOX_RESEARCH_ENDPOINT $(azd ai agent toolbox show research --output json | jq -r '.endpoint')
```

`azd env set` persists the value into the active azd env so subsequent `azd` runs (including `azd up`) pick it up automatically.

### 5.5 `azd ai agent toolbox list`

Flags: `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

Behavior:

1. `GET /toolboxes` (paginated; CLI walks all pages).
2. Merges in pending-toolbox records for the resolved endpoint and marks them `(pending)`.
3. Output:
   - Table columns: `NAME  DEFAULT-VERSION  STATE  TOOLS  CREATED` (`STATE` is empty for live toolboxes, `pending` for local records; `TOOLS` is a count, blank for pending; `CREATED` is the pending record's `createdAt`, blank for live toolboxes — surfaces stale records the user forgot to follow up on).
   - JSON: `{ "toolboxes": [ ..., { "name": "...", "pending": true, "description": "..." } ] }`.

### 5.6 `azd ai agent toolbox connection add | remove | list`

The `connection` subgroup is implemented on top of the toolbox versions API (§ 4.1): every tool mutation is a full-`tools[]` POST to `/toolboxes/{name}/versions`, followed by a `PATCH default_version` to make the new version active.

#### Tool-entry shape by connection category

`connection add` infers the right tool entry shape from the project connection's ARM `category`:

| Connection ARM category | Tool `type` | Tool-entry fields built from the connection | Notes |
| --- | --- | --- | --- |
| `RemoteTool` (MCP servers) | `mcp` | `name`, `server_label`, `server_url`, `project_connection_id` (full ARM ID) | `server_label` is the connection's short name; `server_url` is the connection's `target`. |
| `CognitiveSearch` | `azure_ai_search` | `name`, `azure_ai_search.indexes[].project_connection_id` (full ARM ID) | Requires `--index <name>` (index name is not on the connection). |

Other connection categories (`ApiKey`, `CustomKeys`, `AppInsights`, etc.) → `CodeUnsupportedConnectionCategory` with the message *"Connection `<name>` has category `<category>`; v1 supports `RemoteTool` and `CognitiveSearch` only."*

Built-in tools (`code_interpreter`, `web_search`, `file_search`) are not authored by `connection add` or any other CLI verb (§ 2 Non-Goals). If a toolbox already contains built-in entries (added by another client), the CLI carries them through unchanged during the fetch-merge-POST flow.

#### Flags

| Flag | Type | Default | Used by | Notes |
| --- | --- | --- | --- | --- |
| `--index` | string | "" | `add` | Required when the resolved connection's category is `CognitiveSearch`. Rejected for other categories. |
| `--project-endpoint` / `--output` / `--no-prompt` / `--debug` | — | — | all | Cross-cutting. |

#### Shared write flow (`add` and `remove`)

1. Resolve the project connection's ARM resource ID, `category`, `target`, and any other shape-specific fields via `GET https://management.azure.com{armPath}/connections/{name}?api-version=2025-04-01-preview`.
2. Fetch the current default version body via `GET /toolboxes/{name}/versions/{default_version}`.
3. Mutate the in-memory `tools[]` array (append for `add`; filter by `project_connection_id` for `remove`).
4. `POST /toolboxes/{name}/versions` with the complete mutated `tools[]`, carrying forward `description` and `metadata` from the previous version. Built-in tools are carried through unchanged.
5. `PATCH /toolboxes/{name}` with `{"default_version":"<newVersion>"}`.

| Verb | Positional args | Behavior |
| --- | --- | --- |
| `add` | `<toolbox> <connection-name>` | If the toolbox has a pending record (§ 5.1): POST v1 with `tools=[<resolved entry>]` and the recorded description, then clear the record. (First version is automatically `default_version`, so no PATCH.) Otherwise run the shared write flow, appending the category-appropriate tool entry. Duplicate `project_connection_id` in the current default version → `CodeDuplicateConnection`. |
| `remove` | `<toolbox> <connection-name>` | Run the shared write flow, filtering out by `project_connection_id == <armId>`. Connection not present in the toolbox's current default version → `CodeConnectionNotInToolbox` with suggestion `Run 'connection list'`. If the resulting `tools[]` would have zero entries (counting any built-ins carried through) → `CodeLastToolRemoval` with the suggestion *"Delete the toolbox with `azd ai agent toolbox delete <name>` instead."* — service is not called. |
| `list` | `<toolbox>` | `GET /toolboxes/{name}/versions/{default_version}` and emit entries with `project_connection_id` set (including the nested form for `azure_ai_search`). Table columns: `NAME  CONNECTION  TYPE` (`CONNECTION` shows the connection's short name parsed from the ARM ID's trailing segment). |

If the ARM lookup in step 1 returns 404, the CLI returns `CodeConnectionNotFound` with the suggestion *"Run `azd ai connection list` to see available connections."*

#### Concurrency

The fetch-mutate-POST-PATCH flow has a gap between GET and POST. Two concurrent `connection add` calls against the same toolbox can both fetch the same default version, both POST a new version with their own appended tool, and the last `PATCH default_version` wins. The losing call's tool ends up on an orphan version that is not the default and is invisible to consumers.

The service does not expose primitives to prevent this race — no `If-Match` on POST, no compare-and-swap, no atomic add-tool endpoint. Any client-side mitigation (post-write re-GET, retry) is itself subject to a TOCTOU window between the verification and the PATCH, so v1 does not attempt one. The race is documented as a known limitation; a follow-up will revisit if the service grows conditional-write support (e.g. `If-Match` on POST against the parent's `default_version`). Users running concurrent `connection add` / `connection remove` against the same toolbox today should serialize their calls.

## 6. Endpoint Resolution

The toolbox commands consume the 5-level cascade defined by the project-context spec ([PR #8152](https://github.com/Azure/azure-dev/pull/8152) — `azure-ai-project-commands.md` § 4):

1. `--project-endpoint` flag on the invoked command.
2. Active azd env value (`AZURE_AI_PROJECT_ENDPOINT`) when inside an azd project.
3. Global config: `extensions.ai-agents.context.endpoint`.
4. Environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured `exterrors.Dependency(CodeMissingProjectEndpoint, …)` with an actionable suggestion.

`--project-endpoint` is registered as a persistent flag on the toolbox parent so every subcommand inherits it.

## 7. Config Store

Per-endpoint pending-toolbox records live under:

```jsonc
{
  "extensions": {
    "ai-agents": {
      "pending-toolboxes": {
        "a1b2c3d4e5f6a7b8": {
          "endpoint": "https://my-project.services.ai.azure.com/api/projects/my-project",
          "items": {
            "<toolbox-name>": {
              "description": "Research-time toolset",
              "createdAt": "2026-05-12T10:23:00Z"
            }
          }
        }
      }
    }
  }
}
```

- Outer key is `hex(sha256(endpoint))[:16]` — a short, opaque cache-key fragment. The hash is for key brevity, not secrecy.
- The full endpoint is stored as a plain-text sibling of `items` so the bucket is self-describing.
- Records are cleared by `connection add` (after the first successful POST) or by `delete <name>` (works on a pending-only toolbox without a service call — see § 5.3).
- `toolbox list` merges in records for the resolved endpoint only and surfaces each record's `createdAt` (§ 5.5) so the user can see and prune stale entries. v1 does **not** auto-expire records; cleanup is user-driven via `delete <name>`. A TTL-based or `--cleanup-stale` sweep can be added in a follow-up if accumulation is observed.

## 8. Test Plan

Unit tests (table-driven, no network; inject a `toolboxClient` interface that is a subset of `*azure.FoundryToolboxClient`):

- **`create`** — new name records a pending entry and prints the registered one-liner; existing name does not POST and prints the existing one-liner; description round-trips through the pending record.
- **`update`** — `--default-version` happy path; missing flag → `CodeMissingUpdateField`.
- **`show`** —
  - Live toolbox `default_version` path; explicit `--version`; table and JSON snapshots; `endpoint` field in JSON output is exactly `{projectEndpoint}/toolboxes/<name>/versions/<shown_version>/mcp?api-version=v1` and changes when `--version` is supplied.
  - 404 on live GET with a matching pending record → emits the pending-toolbox view (§ 5.4.1); JSON output has `pending: true`, `version: null`, `endpoint: null`.
  - 404 on live GET with no pending record → `ErrToolboxNotFound`.
  - `--version` on a pending-only toolbox → validation error (no versions exist yet).
- **`list`** — pagination across two pages; pending records merged and tagged `(pending)`; pending records for a different endpoint are not surfaced.
- **`delete`** —
  - Toolbox path: 204 happy path; 404 swallowed.
  - Per-version path (non-default): 204 happy path.
  - Per-version path (default, other versions exist): CLI rejects pre-flight with `CodeDefaultVersionDelete` and the retarget suggestion.
  - Per-version path (default, only remaining version): CLI rejects without `--force` (`CodeOnlyVersionDelete`); with `--force` proceeds and reports the cascaded toolbox removal.
  - `--no-prompt` without `--force` → `CodeMissingForceFlag`.
- **`connection add`** —
  - `RemoteTool` category: ARM lookup resolves `target` → `server_url`; tool entry is `{type:"mcp", name, server_label, server_url, project_connection_id}`.
  - `CognitiveSearch` category: requires `--index <name>`; tool entry is `{type:"azure_ai_search", name, azure_ai_search:{indexes:[{project_connection_id, index_name}]}}`. Missing `--index` → `CodeMissingIndex`.
  - Unsupported category (`ApiKey`, `CustomKeys`, `AppInsights`, …) → `CodeUnsupportedConnectionCategory` with the category in the message; no toolbox-side calls made.
  - `--index` on a `RemoteTool` connection → `CodeUnsupportedIndexFlag` (flag rejected for non-search categories).
  - Pending-record promotion: POSTs v1 with the resolved tool entry and the recorded description; clears the pending record. No PATCH.
  - Existing-toolbox path: fetch default version → append entry → POST new version → PATCH `default_version`.
  - Duplicate `project_connection_id` in the current default version → `CodeDuplicateConnection` before any POST.
  - Connection name not on the project (ARM 404) → `CodeConnectionNotFound` with the "Run `azd ai connection list`" suggestion; no toolbox-side calls made.
- **`connection remove`** —
  - Happy path: POST new version with the entry filtered out (works for both `mcp` and nested `azure_ai_search` entries); PATCH `default_version`.
  - Missing connection → `CodeConnectionNotInToolbox`.
  - Removing a tool that would leave `tools[]` with zero entries (including any built-ins carried through) → `CodeLastToolRemoval` with the "delete the toolbox instead" suggestion; service is not called.
- **`connection list`** — emits entries with `project_connection_id` set (top-level on `mcp`, nested under `azure_ai_search.indexes[]`); table `TYPE` column shows each entry's tool `type`; respects `--output`.

E2E:

- Smoke test that runs `create → connection add → list → show → connection remove → delete` against the built extension and asserts exit codes plus stdout/stderr shape.

Snapshots: `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'` from `cli/azd`.

## 9. Impact on Existing Commands

None at the command level. The toolbox surface is purely additive:

- `internal/cmd/root.go` registers the new toolbox parent alongside `session`, `files`, and `connection`. No existing command's flags, behavior, or output shape is changed.
- `internal/exterrors/codes.go` gains new constants (§ 11); existing codes are not touched.
- `internal/pkg/azure/foundry_toolsets_client.go` gains the additional methods (`ListToolboxes`, `GetToolboxVersion`, `ListToolboxVersions`, `DeleteToolboxVersion`, `SetDefaultVersion`, `RegisterPending`). The existing methods and the pipeline are unchanged; the existing call site in `internal/cmd/listen.go` is unaffected.

## 10. Telemetry

One event per command, reusing the extension's existing telemetry surface. All include `endpointHostHash` (sha256 of host) and `resolvedSource` (enum string from the cascade).

| Event | Additional properties |
| --- | --- |
| `azd.ai.toolbox.create` | `hasDescription` (bool) |
| `azd.ai.toolbox.update` | — |
| `azd.ai.toolbox.delete` | `scope` (`toolbox` \| `version`), `forced` (bool) |
| `azd.ai.toolbox.show` | `versionMode` (`default` \| `explicit`) |
| `azd.ai.toolbox.list` | `count` (int), `pendingCount` (int) |
| `azd.ai.toolbox.connection.add` | `promotedFromPending` (bool), `armResolveOk` (bool) |
| `azd.ai.toolbox.connection.remove` | — |
| `azd.ai.toolbox.connection.list` | `count` (int) |

No PII. `endpointHostHash` is sha256 of the project endpoint hostname; toolbox names are sent as-is (user-chosen labels with no credential value).

## 11. Errors

New codes added to `internal/exterrors/codes.go`. Each code maps to a single failure mode so telemetry can distinguish root causes:

| Code | Used by |
| --- | --- |
| `CodeMissingUpdateField` | `update` invoked without `--default-version`. |
| `CodeDefaultVersionDelete` | `delete --version <n>` where `<n>` is `default_version` and other versions exist. |
| `CodeOnlyVersionDelete` | `delete --version <n>` where `<n>` is the only remaining version, invoked without `--force`. |
| `CodeUnsupportedConnectionCategory` | `connection add` against a connection whose ARM `category` is not `RemoteTool` or `CognitiveSearch`. |
| `CodeMissingIndex` | `connection add` against a `CognitiveSearch` connection without `--index`. |
| `CodeUnsupportedIndexFlag` | `--index` supplied for a non-`CognitiveSearch` connection on `connection add`. |
| `CodeDuplicateConnection` | `connection add` where the resolved `project_connection_id` is already present in the current default version. |
| `CodeConnectionNotFound` | `connection add`'s ARM control-plane lookup returns 404 for the named connection. |
| `CodeConnectionNotInToolbox` | `connection remove` for a connection not present in the current default version. |
| `CodeLastToolRemoval` | `connection remove` whose resulting `tools[]` would have zero entries (including any carried-through built-ins). |
| `CodeMissingForceFlag` | `delete` with `--no-prompt` and without `--force`. |

New `Op*` constants for `exterrors.ServiceFromAzure` (added to `internal/exterrors/codes.go` alongside the existing `OpCreateToolboxVersion` and `OpGetToolbox`, which are reused):

```
OpRegisterPendingToolbox
OpDeleteToolbox
OpDeleteToolboxVersion
OpSetDefaultVersion
OpListToolboxes
OpGetToolboxVersion
OpListToolboxVersions
OpResolveProjectConnection
```

## 12. Security Considerations

- No credential material flows through toolbox commands. Connection credentials are owned by the connection extension and never echoed.
- Endpoint URLs and toolbox names are persisted in plain text to `~/.azd/config.json` for pending records (§ 7). Endpoints are not credentials. File permissions are managed by azd core; no change.
- The data-plane client uses the existing Foundry pipeline factory, inheriting its TLS / proxy configuration.

## 13. Decisions

1. **`create` does not accept `--connection <name>`.** `create` only records a local pending entry; the first network write happens on `connection add` (§ 5.1, § 5.6).
2. **`toolbox list` reports tool count for the default version only.** Avoids one extra `GET /versions` per toolbox; matches `show`'s default behavior (§ 5.5).
3. **`connection add` does not accept `--as <alias>`.** The tool entry's `name` is the connection's short name (§ 5.6).

## 14. Reference: Command Summary

```bash
azd ai agent toolbox create  <name>            [--description <text>]                                  [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox update  <name>             --default-version <n>                                  [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox delete  <name>            [--version <n>] [--force]                                [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox show    <name>            [--version <n>]                                          [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox list                                                                                [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]

azd ai agent toolbox connection add    <toolbox> <connection> [--index <name>]                          [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox connection remove <toolbox> <connection>                                            [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox connection list   <toolbox>                                                         [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: `--project-endpoint` flag → azd env (`AZURE_AI_PROJECT_ENDPOINT`) → `~/.azd/config.json` (`extensions.ai-agents.context.endpoint`) → `FOUNDRY_PROJECT_ENDPOINT` → structured error.
