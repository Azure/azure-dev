<!-- cspell:ignore foundry toolbox toolboxes toolsets exterrors retarget touchpoints -->

# Design Spec: `azd ai toolbox` Direct Commands

## 1. Summary

This spec covers the toolbox CRUD surface, shipped as a standalone `azure.ai.toolboxes` extension:

- `azd ai toolbox create | update | delete | show | list` — manage versioned toolboxes against a Foundry project.
- `azd ai toolbox version list <toolbox>` — list every published version of a toolbox and mark the default.
- `azd ai toolbox connection add | remove | list` — manage the connection-backed tools attached to a toolbox (MCP servers, Azure AI Search; tool shape inferred from the connection's ARM category — see § 5.6).

A Foundry **toolbox** is a versioned, named collection of connection-backed tools that an agent references at run time. Each version carries a `tools[]` array of MCP tools (for `RemoteTool` connections) and Azure AI Search tools (for `CognitiveSearch` connections), each pointing at an existing project connection.

`create` and `connection add` accept their inputs through a JSON or YAML file via `--from-file`. There is no client-side "pending toolbox" state: a toolbox exists on the service from the moment `create` succeeds (publishing its initial version in the same call), and every subsequent mutation publishes a new version.

## 2. Scope and Non-Goals

In scope:

- The nine verbs listed in § 1.
- Cross-cutting flags (`--output table|json`, `--no-prompt`, `--debug`, `--project-endpoint`) on every new command.
- The Foundry toolbox/projects data-plane clients, vendored under the new extension at `internal/pkg/azure/foundry_toolsets_client.go` and `internal/foundry/connections/client.go` (the duplication contract from the agents extension is documented in those files).

Out of scope:

- A subcommand of `azd ai agent`. Toolbox commands ship as a sibling extension (`azure.ai.toolboxes`) with its own command tree under `azd ai toolbox …`.
- Authoring built-in tools (`code_interpreter`, `web_search`, `file_search`) into toolboxes. Built-ins are wired on the agent, not via toolboxes (issue #8143). The CLI carries through any built-in entries already present on a fetched version (§ 5.6) but provides no verb to add or remove them. The file shape in § 5.1 / § 5.6 intentionally has no `tools[]` escape hatch.
- Adding a connection to a non-default version (tracked separately in issue #8244; the documented workaround is to retarget the version as default first, then run `connection add`).
- Config-driven orchestration / `azd up` for toolboxes.
- Bicep / ARM template authoring for toolboxes.
- Cross-project toolbox copy / clone.

## 3. Extension Placement

The toolbox surface ships as a new top-level extension `azure.ai.toolboxes` (sibling to `azure.ai.agents`). Toolbox commands surface as `azd ai toolbox …`.

The `azure.ai.toolboxes` extension carries its own copies of the Foundry data-plane primitives it needs (Foundry credential factory, project-endpoint resolver/validator, single-connection lookup client). These are duplicated — not depended on — from `azure.ai.agents` so the two extensions ship and version independently. The duplication is documented in each affected file and is intended to be the seed for a future shared module.

### 3.1 Modular Layout

All `internal/...` paths in §§ 3, 5, 8, 9, and 11 are inside `cli/azd/extensions/azure.ai.toolboxes/`.

1. All toolbox command files live under `internal/cmd/toolbox*.go`. The command parent is registered by the extension's own `internal/cmd/root.go`.
2. All toolbox data-plane access goes through `FoundryToolboxClient` in `internal/pkg/azure/foundry_toolsets_client.go`. It exposes `CreateToolboxVersion`, `GetToolbox`, `DeleteToolbox`, `ListToolboxes`, `GetToolboxVersion`, `ListToolboxVersions`, `DeleteToolboxVersion`, `SetDefaultVersion`. Pipeline auth scope is `https://ai.azure.com/.default` and every request carries the `Foundry-Features: Toolboxes=V1Preview` header.
3. Connection resolution (`GET /connections/{name}`) goes through `internal/foundry/connections/client.go`. The toolbox commands only need a single-connection lookup; they do not enumerate connections.
4. Endpoint resolution and validation (the 5-level cascade) lives under `internal/foundry/projectctx/`. It reads `extensions.ai-agents.context.endpoint` from global config (the key owned by `azure.ai.agents`) read-only; it never writes that key.
5. Imports are one-way: `internal/cmd/` → `internal/foundry/`, `internal/pkg/azure`, `internal/exterrors`.

### 3.2 Boundary with `azure.ai.agents`

| Touchpoint | Direction | Notes |
| --- | --- | --- |
| `extensions.ai-agents.context.endpoint` in `~/.azd/config.json` | toolboxes reads, never writes | Owned by `azure.ai.agents`; toolboxes is one consumer of the cascade defined by the project-context spec. |
| `FoundryToolboxClient` | toolboxes has its own copy | The agents extension's copy is reverted to its original (pre-toolbox-CRUD) surface; the additional methods only live in the toolboxes extension. |
| Connection lookup client | toolboxes has its own minimal copy | Only the `Get(name)` primitive is needed; full enumeration stays in agents. |
| Credential factory | toolboxes has its own copy | Same `azidentity.NewAzureDeveloperCLICredential` wrapper used by agents. |
| `exterrors` | each extension owns its own copy | `azure.ai.toolboxes/internal/exterrors/` carries only the codes/ops the toolbox surface uses; unrelated codes are not duplicated. |

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

### 5.1 `azd ai toolbox create <name> --from-file <path>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--from-file` | string, **required** | — | Path to a JSON/YAML payload describing the initial version. Marked `MarkFlagRequired` on the cobra command. |
| `--project-endpoint` | string | resolver | See § 6. No `-p` short form (taken by `agent run` / `agent invoke`). |
| `--output` | enum | `table` | `table` \| `json`. |
| `--no-prompt` | bool | `false` | Cross-cutting. |
| `--debug` | bool | `false` | Cross-cutting. |

#### File shape

The same `connections[]` shape is reused by `connection add --from-file` (§ 5.6) so the two file payloads are intentionally similar. Only `create` accepts `description`.

```jsonc
{
  "description": "research toolbox",
  "connections": [
    { "name": "my-mcp" },
    { "name": "my-search", "index": "products" }
  ]
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `description` | optional | Stored on the initial version. |
| `connections[]` | required, non-empty | List of existing project connections to attach. |
| `connections[].name` | required | Project connection short name. Looked up via `connections.Client.Get(name)`. |
| `connections[].index` | required for `CognitiveSearch` connections, rejected otherwise | The search index name inside the AI Search service the connection points at. |

There is no `metadata` field and no raw `tools[]` escape hatch (§ 2 Non-Goals). Both the YAML form (`.yaml`/`.yml`) and JSON (`.json`) are accepted; the file extension selects the parser. The parser is strict: unknown top-level fields fail with `CodeInvalidParameter` so a typo (e.g. `conections` instead of `connections`) is caught locally rather than silently dropped.

#### Behavior

1. Resolve the project endpoint (§ 6).
2. `GET /toolboxes/{name}` — if the toolbox already exists, fail with `CodeInvalidToolboxName` and the suggestion *"run `azd ai toolbox update` or `connection add/remove` to change it."* (Behavior is **non-mutating** on the "already exists" branch — no POST is attempted.)
3. Parse `--from-file`. Reject:
   - Empty/missing `connections[]` → `CodeInvalidToolboxName` *"toolbox create requires at least one connection."*
   - Empty `connections[].name` → `CodeInvalidParameter`.
   - Two entries that resolve to the same `project_connection_id` → `CodeDuplicateConnection` (pre-flight; service is not called).
4. Resolve each connection via `connections.Client.Get(name)` and convert to a tool entry via the shape table in § 5.6.
5. `POST /toolboxes/{name}/versions` with `{description, tools: [<resolved entries>]}`. The new version is automatically `default_version` (service guarantee for first POST), so no follow-up PATCH is issued.
6. Output:
   - Table: `Created toolbox <name> at version <v>.` followed by `Endpoint: <mcp-url>`.
   - JSON: `{ "toolbox": "<name>", "version": "<v>", "endpoint": "<mcp-url>" }`.

`--force` is not exposed on `create`. There is no "pending toolbox" state: creation is a single atomic publication of v1.

### 5.2 `azd ai toolbox update <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--default-version` | string, required | — | Version string to mark as default. |
| `--output` / `--no-prompt` / `--debug` / `--project-endpoint` | — | — | Cross-cutting. |

Behavior:

1. `PATCH /toolboxes/{name}` with `{"default_version":"<n>"}`.
2. Description and tool edits are not supported (service only accepts `default_version` on PATCH). To change the tool list, publish a new version via `connection add` / `connection remove`. Description is set at create time only.
3. Missing `--default-version` → `exterrors.Validation(CodeMissingUpdateField, "No fields to update. Specify --default-version.")`.

### 5.3 `azd ai toolbox delete <name>`

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
| `--version` absent, toolbox exists on the service | `DELETE /toolboxes/{name}`. 404 is swallowed. |
| `--version` absent, toolbox 404 | `exterrors.Dependency(CodeToolboxNotFound, ...)` with suggestion *"Run `azd ai toolbox list` to see available toolboxes."* |
| `--version <n>`, `<n>` is not `default_version` | `DELETE /toolboxes/{name}/versions/{n}`. No prompt. |
| `--version <n>`, `<n>` is `default_version`, other versions exist | `exterrors.Validation(CodeDefaultVersionDelete, ...)` with suggestion *"Retarget the default with `azd ai toolbox update --default-version <other>` first."* The CLI determines this by listing versions before issuing DELETE; the server would otherwise return 400 with the same message. |
| `--version <n>`, `<n>` is `default_version`, **only remaining version** | The service deletes the version and cascades to remove the parent toolbox. To avoid surprise destruction, the CLI **rejects** this case unless `--force` is set, returning `exterrors.Validation(CodeOnlyVersionDelete, "Version <n> is the only remaining version; deleting it removes the toolbox.")` with suggestion *"Run `azd ai toolbox delete <name>` to delete the toolbox, or pass `--force` to confirm."* With `--force`, the CLI proceeds and reports `Deleted toolbox <name> (last version removed).` |
| `--no-prompt` without `--force`, parent-toolbox delete | `exterrors.Validation(CodeMissingForceFlag, ...)`. Per-version delete does not prompt and is not gated by `--force`/`--no-prompt`. |

Confirmation is shown by default on parent-toolbox delete; `--force` skips it. Per-version delete (`--version <n>`) does not prompt — it's a constrained mutation that already has its own pre-flight guards above.

### 5.4 `azd ai toolbox show <name>`

Flags:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `<name>` | positional, required | — | Toolbox name. |
| `--version` | string | "" | Specific version. Default: server's `default_version`. |
| `--output` / `--no-prompt` / `--debug` / `--project-endpoint` | — | — | Cross-cutting. |

Behavior:

1. `GET /toolboxes/{name}` for `default_version`. 404 → `exterrors.Dependency(CodeToolboxNotFound, ...)` with suggestion *"Run `azd ai toolbox list` to see available toolboxes."*
2. `GET /toolboxes/{name}/versions/{version}` (or `default_version` when `--version` is absent) for the body. 404 → `exterrors.Dependency(CodeToolboxNotFound, "version \"<v>\" of toolbox \"<name>\" not found", ...)`.
3. Compute the toolbox's MCP consumption URL as `{projectEndpoint}/toolboxes/{name}/versions/{shown_version}/mcp?api-version=v1`, where `shown_version` is the `--version` arg or the server's `default_version`.
4. Output:
   - Table: `Name`, `Default version`, `Shown version`, `Description`, `Endpoint`, `Tools` (count + list with `(builtin)` / `(connection:<id>)` annotation).
   - JSON: `{ "toolbox": <ToolboxObject>, "version": <ToolboxVersionObject>, "endpoint": "<mcp-url>" }`.

#### 5.4.1 Runtime consumption

The `endpoint` field is the contract for wiring a toolbox into agent code via the active azd environment:

```bash
azd env set TOOLBOX_RESEARCH_ENDPOINT $(azd ai toolbox show research --output json | jq -r '.endpoint')
```

`azd env set` persists the value into the active azd env so subsequent `azd` runs (including `azd up`) pick it up automatically.

### 5.5 `azd ai toolbox list`

Flags: `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

Behavior:

1. `GET /toolboxes` (paginated; CLI walks all pages).
2. Output:
   - Table columns: `NAME  DEFAULT-VERSION`. The table intentionally omits a per-toolbox tool count to avoid an extra `GET /versions` per row; use `toolbox show` to see tools for a single toolbox.
   - JSON: `{ "toolboxes": [ { "id": "...", "name": "...", "default_version": "..." }, ... ] }`.

### 5.6 `azd ai toolbox connection add | remove | list`

The `connection` subgroup is implemented on top of the toolbox versions API (§ 4.1): every tool mutation is a full-`tools[]` POST to `/toolboxes/{name}/versions`, followed by a `PATCH default_version` to make the new version active.

#### Tool-entry shape by connection category

`connection add` infers the right tool entry shape from the project connection's ARM `category`:

| Connection ARM category | Tool `type` | Tool-entry fields built from the connection | Notes |
| --- | --- | --- | --- |
| `RemoteTool` (MCP servers) | `mcp` | `name`, `server_label`, `server_url`, `project_connection_id` (full ARM ID) | `server_label` is the connection's short name; `server_url` is the connection's `target`. |
| `CognitiveSearch` | `azure_ai_search` | `name`, `azure_ai_search.indexes[].project_connection_id` (full ARM ID), `azure_ai_search.indexes[].index_name` | Requires `--index <name>` in single-mode, or `connections[].index` in file mode. The index name is not on the connection — it must be supplied per call. |

Other connection categories (`ApiKey`, `CustomKeys`, `AppInsights`, etc.) → `CodeUnsupportedConnectionCategory` with the message *"Connection `<name>` has category `<category>`; this command supports `RemoteTool` and `CognitiveSearch` only."* Service-team-confirmed additions (e.g. `RemoteA2A`, `GroundingWithCustomSearch`, `BrowserTool`) are tracked separately and will extend the table above when the toolbox `tools[].type` shape is finalized.

Built-in tools (`code_interpreter`, `web_search`, `file_search`) are not authored by `connection add` or any other CLI verb (§ 2 Non-Goals). If a toolbox already contains built-in entries (added by another client), the CLI carries them through unchanged during the fetch-merge-POST flow.

#### Flags

| Flag | Type | Default | Used by | Notes |
| --- | --- | --- | --- | --- |
| `--index` | string | "" | `add` (single mode) | Required when the resolved connection's category is `CognitiveSearch`. Rejected for other categories. Mutually exclusive with `--from-file`. |
| `--from-file` | string | "" | `add` (file mode) | Path to a JSON/YAML payload describing the connections to attach. All entries publish exactly one new toolbox version. Mutually exclusive with the `<connection>` positional. |
| `--force` | bool | `false` | `remove` | Skip the confirmation prompt that `remove` shows by default. `--no-prompt` without `--force` → `CodeMissingForceFlag`. |
| `--project-endpoint` / `--output` / `--no-prompt` / `--debug` | — | — | all | Cross-cutting. |

#### File shape (`add --from-file`)

```jsonc
{
  "connections": [
    { "name": "my-mcp" },
    { "name": "my-search", "index": "products" }
  ]
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `connections[]` | required, non-empty | List of project connections to attach. The existing version's `description` and `metadata` are carried forward unchanged. |
| `connections[].name` | required | Project connection short name. |
| `connections[].index` | required for `CognitiveSearch`, rejected otherwise | Search index name. |

There is no raw `tools[]` field, and `description` is not accepted in this file shape: a toolbox's description is set at create time and cannot be changed afterwards in v1 (the service `PATCH` surface only accepts `default_version`). Unknown fields in the file — including a stray `description` — are rejected by the parser with a clear error so a user typo can't silently propagate.

#### Shared write flow (`add` and `remove`)

1. Resolve each named project connection's ARM resource ID, `category`, `target`, and any other shape-specific fields via `GET https://management.azure.com{armPath}/connections/{name}?api-version=2025-04-01-preview`. Both modes use the same `connections.Client.Get(name)` primitive.
2. Fetch the current default version body via `GET /toolboxes/{name}/versions/{default_version}`.
3. Mutate the in-memory `tools[]` array:
   - `add` (single): append one resolved entry.
   - `add` (file): resolve every `connections[]` entry, append them all in order. Duplicate `project_connection_id` either against the current default version or within the batch → `CodeDuplicateConnection` (pre-flight; service is not called).
   - `remove`: filter out by `project_connection_id == <armId>`.
4. `POST /toolboxes/{name}/versions` with the complete mutated `tools[]`, carrying forward `description` and `metadata` from the previous version. Built-in tools are carried through unchanged.
5. `PATCH /toolboxes/{name}` with `{"default_version":"<newVersion>"}`. On PATCH failure after a successful POST, the error reports the just-created version number so the user can manually retarget with `toolbox update --default-version <v>`.

| Verb | Positional args | Notes |
| --- | --- | --- |
| `add` (single) | `<toolbox> <connection-name>` | One new default version per invocation. |
| `add` (file) | `<toolbox>` (positional `<connection-name>` not allowed when `--from-file` is set) | All entries publish **exactly one** new default version per invocation — adding N connections via the file produces v(N+1), not v(N+N). |
| `remove` | `<toolbox> <connection-name>` | Runs the shared write flow, filtering out by `project_connection_id == <armId>`. Connection not present in the toolbox's current default version → `CodeConnectionNotInToolbox` with suggestion `Run 'connection list'`. If the resulting `tools[]` would have zero entries (counting any built-ins carried through) → `CodeLastToolRemoval` with the suggestion *"Delete the toolbox with `azd ai toolbox delete <name>` instead."* — service is not called. Prompts for confirmation by default; `--force` skips it; `--no-prompt` without `--force` → `CodeMissingForceFlag`. |
| `list` | `<toolbox>` | `GET /toolboxes/{name}/versions/{default_version}` and emit entries with `project_connection_id` set (including the nested form for `azure_ai_search`). Table columns: `NAME  CONNECTION  TYPE` (`CONNECTION` shows the connection's short name parsed from the ARM ID's trailing segment). JSON entries include `connection_id` (snake_case). |

If the ARM lookup in step 1 returns 404, the CLI returns `CodeConnectionNotFound` with the suggestion *"Run `azd ai connection list` to see available connections."*

#### Concurrency

The fetch-mutate-POST-PATCH flow has a gap between GET and POST. Two concurrent `connection add` calls against the same toolbox can both fetch the same default version, both POST a new version with their own appended tool, and the last `PATCH default_version` wins. The losing call's tool ends up on an orphan version that is not the default and is invisible to consumers.

The service does not expose primitives to prevent this race — no `If-Match` on POST, no compare-and-swap, no atomic add-tool endpoint. Any client-side mitigation (post-write re-GET, retry) is itself subject to a TOCTOU window between the verification and the PATCH, so v1 does not attempt one. The race is documented as a known limitation; a follow-up will revisit if the service grows conditional-write support (e.g. `If-Match` on POST against the parent's `default_version`). Users running concurrent `connection add` / `connection remove` against the same toolbox today should serialize their calls.

### 5.7 `azd ai toolbox version list <toolbox>`

Flags: `<toolbox>` positional, `--output`, `--project-endpoint`, `--no-prompt`, `--debug`.

Behavior:

1. `GET /toolboxes/{name}` for the current `default_version`. 404 → `CodeToolboxNotFound`.
2. `GET /toolboxes/{name}/versions` (paginated). Sort descending: numeric versions compare numerically; non-numeric versions sort lexically descending.
3. Output:
   - Table columns: `VERSION  DEFAULT  CREATED  TOOLS  DESCRIPTION`. `DEFAULT` shows `*` on the row whose `version` matches the toolbox's `default_version`; `CREATED` is the version's `created_at` rendered RFC3339; `TOOLS` is the entry count.
   - JSON: `{ "toolbox": "<name>", "default_version": "<v>", "versions": [ { "id", "name", "version", "description", "created_at", "tools_count", "is_default" }, ... ] }`.

This verb exists so users can discover non-default versions before retargeting with `update --default-version` or deleting via `delete --version`. It is read-only and idempotent.

## 6. Endpoint Resolution

The toolbox commands consume the 5-level cascade defined by the project-context spec ([PR #8152](https://github.com/Azure/azure-dev/pull/8152) — `azure-ai-project-commands.md` § 4):

1. `--project-endpoint` flag on the invoked command.
2. Active azd env value (`AZURE_AI_PROJECT_ENDPOINT`) when inside an azd project.
3. Global config: `extensions.ai-agents.context.endpoint` (owned by `azure.ai.agents`; `azure.ai.toolboxes` reads it but never writes it — see § 3.2).
4. Environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured `exterrors.Dependency(CodeMissingProjectEndpoint, …)` with an actionable suggestion.

`--project-endpoint` is registered as a persistent flag on the toolbox root so every subcommand inherits it.

## 7. Client-Side State

The toolbox commands hold **no** client-side state. Everything the user observes comes from a single source of truth: the Foundry project. `create` publishes the initial version in the same call that creates the toolbox; `list` / `show` / `version list` read live state; `delete` removes live state. The endpoint cascade (§ 6) reads existing azd-managed config but writes nothing.

An earlier draft of this spec proposed a per-endpoint "pending toolbox" record under `~/.azd/config.json` (keyed by `hex(sha256(endpoint))[:16]`) to bridge the gap between `create` and the first `connection add`. That model was removed in favour of file-based `create --from-file` (§ 5.1), which lets the first POST ship a non-empty `tools[]` directly. The implementation has no `pending_toolboxes.go` file and the `extensions.ai-toolboxes.pending-toolboxes` namespace is unused.

## 8. Test Plan

Unit tests (table-driven, no network; inject a `toolboxClient` interface that is a subset of `*azure.FoundryToolboxClient`, and a `connectionResolver` interface that stands in for the project connections client):

- **`create`** —
  - File-input happy path: POSTs v1 with the resolved tool entries and the file's `description`. No PATCH (first version is automatically default).
  - File missing or unreadable → `CodeInvalidParameter` with the path and OS error.
  - Empty `connections[]` → `CodeInvalidToolboxName` *"toolbox create requires at least one connection."*
  - Duplicate `connections[].name` (resolving to the same `project_connection_id`) → `CodeDuplicateConnection`, pre-flight.
  - Toolbox already exists (`GET /toolboxes/{name}` returns 200) → `CodeInvalidToolboxName` *"toolbox \"<name>\" already exists"* and **no POST** is attempted.
  - YAML and JSON both parse; unknown file extension → `CodeInvalidParameter`.
- **`update`** — `--default-version` happy path; missing flag → `CodeMissingUpdateField`.
- **`show`** —
  - Live toolbox `default_version` path; explicit `--version`; table and JSON snapshots.
  - `endpoint` field in JSON output is exactly `{projectEndpoint}/toolboxes/<name>/versions/<shown_version>/mcp?api-version=v1` and changes when `--version` is supplied.
  - 404 on `GET /toolboxes/{name}` → `CodeToolboxNotFound`.
  - 404 on the specific-version GET → `CodeToolboxNotFound` with the version in the message.
- **`list`** — pagination across two pages; sorted by name; table omits tool count.
- **`version list`** —
  - Happy path: returns versions sorted descending; marks the default with `*` in the table; JSON `is_default` is true on exactly one row.
  - 404 on `GET /toolboxes/{name}` → `CodeToolboxNotFound`.
  - Service error on `ListToolboxVersions` propagates as `ServiceFromAzure(OpListToolboxVersions)`.
- **`delete`** —
  - Toolbox path: 204 happy path; 404 → `CodeToolboxNotFound`.
  - Per-version path (non-default): 204 happy path, no prompt.
  - Per-version path (default, other versions exist): CLI rejects pre-flight with `CodeDefaultVersionDelete` and the retarget suggestion.
  - Per-version path (default, only remaining version): CLI rejects without `--force` (`CodeOnlyVersionDelete`); with `--force` proceeds and reports the cascaded toolbox removal.
  - `--no-prompt` without `--force` on a parent-toolbox delete → `CodeMissingForceFlag`; per-version delete is not gated by `--force`/`--no-prompt`.
- **`connection add`** (single mode) —
  - `RemoteTool` category: ARM lookup resolves `target` → `server_url`; tool entry is `{type:"mcp", name, server_label, server_url, project_connection_id}`.
  - `CognitiveSearch` category: requires `--index <name>`; tool entry is `{type:"azure_ai_search", name, azure_ai_search:{indexes:[{project_connection_id, index_name}]}}`. Missing `--index` → `CodeMissingIndex`.
  - Unsupported category (`ApiKey`, `CustomKeys`, `AppInsights`, …) → `CodeUnsupportedConnectionCategory` with the category in the message; no toolbox-side calls made.
  - `--index` on a `RemoteTool` connection → `CodeUnsupportedIndexFlag`.
  - Existing-toolbox path: fetch default version → append entry → POST new version → PATCH `default_version`.
  - Duplicate `project_connection_id` in the current default version → `CodeDuplicateConnection` before any POST.
  - Connection name not on the project (ARM 404) → `CodeConnectionNotFound` with the "Run `azd ai connection list`" suggestion; no toolbox-side calls made.
  - `SetDefaultVersion` fails after a successful POST → `CodeSetDefaultVersionFailed` with the created version number in the message and a recovery suggestion that includes `toolbox update --default-version <v>`.
- **`connection add`** (file mode) —
  - All `connections[]` entries publish **exactly one** new version per invocation (single POST, single PATCH). Asserts `len(client.createVersionCalls) == 1` for an N-entry file.
  - `<connection>` positional supplied together with `--from-file` → `CodeInvalidPositionalArg`.
  - `--index` supplied together with `--from-file` → `CodeUnsupportedIndexFlag` (set per-connection via `connections[].index` instead).
  - Empty `connections[]` → `CodeInvalidToolboxName`.
  - Duplicate within the batch or against the current default version → `CodeDuplicateConnection`, pre-flight.
- **`connection remove`** —
  - Happy path: POST new version with the entry filtered out (works for both `mcp` and nested `azure_ai_search` entries); PATCH `default_version`.
  - Missing connection → `CodeConnectionNotInToolbox`.
  - Removing a tool that would leave `tools[]` with zero entries (including any built-ins carried through) → `CodeLastToolRemoval` with the "delete the toolbox instead" suggestion; service is not called.
  - `--no-prompt` without `--force` → `CodeMissingForceFlag` before the resolver runs.
- **`connection list`** — emits entries with `project_connection_id` set (top-level on `mcp`, nested under `azure_ai_search.indexes[]`); table `TYPE` column shows each entry's tool `type`; JSON keys use snake_case (`connection_id`); respects `--output`.

Snapshots: `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'` from `cli/azd`.

## 9. Impact on Existing Commands

The toolbox surface ships as a new extension `azure.ai.toolboxes`, so the impact on existing commands is limited to entries in the extension registry:

- `cli/azd/extensions/registry.json` gains a new entry for `azure.ai.toolboxes`. No existing entry is modified.
- `cli/azd/extensions/azure.ai.agents/` is reverted to its pre-toolbox-CRUD surface: `internal/pkg/azure/foundry_toolsets_client.go` and `internal/exterrors/codes.go` no longer carry the toolbox-only additions; the agents extension's `internal/cmd/listen.go` consumer of the original `FoundryToolboxClient` methods is unchanged.

No command outside the toolboxes extension changes its flags, behavior, or output shape.

## 10. Telemetry

One event per command, reusing the extension's existing telemetry surface. All include `endpointHostHash` (sha256 of host) and `resolvedSource` (enum string from the cascade).

| Event | Additional properties |
| --- | --- |
| `azd.ai.toolbox.create` | `hasDescription` (bool), `connectionCount` (int) |
| `azd.ai.toolbox.update` | — |
| `azd.ai.toolbox.delete` | `scope` (`toolbox` \| `version`), `forced` (bool) |
| `azd.ai.toolbox.show` | `versionMode` (`default` \| `explicit`) |
| `azd.ai.toolbox.list` | `count` (int) |
| `azd.ai.toolbox.version.list` | `count` (int) |
| `azd.ai.toolbox.connection.add` | `mode` (`single` \| `file`), `connectionCount` (int), `armResolveOk` (bool) |
| `azd.ai.toolbox.connection.remove` | `forced` (bool) |
| `azd.ai.toolbox.connection.list` | `count` (int) |

No PII. `endpointHostHash` is sha256 of the project endpoint hostname; toolbox names are sent as-is (user-chosen labels with no credential value).

## 11. Errors

New codes added to `internal/exterrors/codes.go`. Each code maps to a single failure mode so telemetry can distinguish root causes:

| Code | Used by |
| --- | --- |
| `CodeInvalidToolboxName` | `create` against an already-existing name; `create` / `connection add` (file) with an empty `connections[]`; positional name fails the local regex / length guard. |
| `CodeInvalidParameter` | `--from-file` path is unreadable, parses as invalid JSON/YAML, has an unsupported extension, or a `connections[].name` is empty. |
| `CodeInvalidPositionalArg` | `connection add` with both a `<connection>` positional and `--from-file`; or with neither. |
| `CodeMissingUpdateField` | `update` invoked without `--default-version`. |
| `CodeToolboxNotFound` | `GET /toolboxes/{name}` returns 404 on `show`, `delete`, `connection add` (existing-toolbox path), `connection remove`, `connection list`, `version list`. |
| `CodeDefaultVersionDelete` | `delete --version <n>` where `<n>` is `default_version` and other versions exist. |
| `CodeOnlyVersionDelete` | `delete --version <n>` where `<n>` is the only remaining version, invoked without `--force`. |
| `CodeUnsupportedConnectionCategory` | `connection add` against a connection whose ARM `category` is not `RemoteTool` or `CognitiveSearch`. |
| `CodeMissingIndex` | `connection add` against a `CognitiveSearch` connection without `--index` (single mode) or without `connections[].index` (file mode). |
| `CodeUnsupportedIndexFlag` | `--index` supplied for a non-`CognitiveSearch` connection on `connection add`; or `--index` supplied together with `--from-file`. |
| `CodeDuplicateConnection` | `connection add` or `create` where the resolved `project_connection_id` is already present in the current default version, or appears more than once within the input. |
| `CodeConnectionNotFound` | `connection add` / `connection remove` / `create`'s ARM control-plane lookup returns 404 for the named connection. |
| `CodeConnectionMissingTarget` | `connection add` against a `RemoteTool` connection whose `target` is empty (would produce a generic 400 server-side). |
| `CodeConnectionNotInToolbox` | `connection remove` for a connection not present in the current default version. |
| `CodeLastToolRemoval` | `connection remove` whose resulting `tools[]` would have zero entries (including any carried-through built-ins). |
| `CodeMissingForceFlag` | `delete` or `connection remove` with `--no-prompt` and without `--force`. |
| `CodeSetDefaultVersionFailed` | `connection add` / `connection remove` POSTed a new version successfully but the subsequent `PATCH default_version` failed. Error message and suggestion include the orphan version number for manual recovery via `toolbox update --default-version`. |

`Op*` constants for `exterrors.ServiceFromAzure`:

```
OpCreateToolboxVersion
OpGetToolbox
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
- The toolbox CLI persists no client-side state (§ 7). Endpoint URLs and toolbox names are not written to `~/.azd/config.json` by the toolboxes extension; the only config the extension reads is the shared `extensions.ai-agents.context.endpoint` slot owned by `azure.ai.agents` (read-only).
- The data-plane client uses the existing Foundry pipeline factory, inheriting its TLS / proxy configuration.

## 13. Decisions

1. **`create` is file-based and immediately publishes the initial version.** An earlier draft proposed a per-endpoint "pending toolbox" config record that bridged the gap between `create` and the first `connection add`. That model was rejected because it created two ways to express the same state ("registered locally" vs "live on the service"), needed manual cleanup, and surprised users whose toolbox was visible to `azd ai toolbox list` but not to anyone else. File input lets the first POST ship a non-empty `tools[]` directly. See § 5.1, § 7.
2. **No raw `tools[]` escape hatch in the file shape.** The file accepts only `description` (create) and `connections[]`; there is no top-level `tools[]` field. Built-ins are out of scope (§ 2 Non-Goals), so the field would only serve as a backdoor for unsupported categories — which the typed `connections` mapper should learn about explicitly when the service confirms their tool shapes. Tracking is via the unsupported-category list in § 5.6.
3. **`connection add --from-file` publishes exactly one new version per invocation.** The earlier "N tools → N versions" behavior surfaced in review feedback as the dominant pain point. Single-mode (`add <toolbox> <connection>`) still publishes one version per call; file mode batches N entries into a single POST + PATCH. See § 5.6.
4. **`connection add` does not target a non-default version.** Tracked separately in issue #8244. Workaround: retarget the desired version as default via `update --default-version`, then run `connection add`.
5. **`connection add` does not accept `--as <alias>`.** The tool entry's `name` is the connection's short name (§ 5.6).
6. **`toolbox list` does not report tool counts.** Avoids one extra `GET /versions` per toolbox (§ 5.5). Use `toolbox show` or `toolbox version list` to inspect tools/versions for a single toolbox.
7. **`connection remove` prompts by default.** Symmetric with `toolbox delete`. `--force` skips; `--no-prompt` without `--force` rejects with `CodeMissingForceFlag`.
8. **JSON output keys use snake_case.** Consistent within the toolbox surface (`default_version`, `connection_id`, `is_default`, `created_at`, `tools_count`).
9. **Description is set at create time and immutable afterwards in v1.** The service `PATCH` accepts only `default_version`, so the CLI cannot retarget description. The `connection add --from-file` schema therefore does not accept `description`; the parser rejects unknown fields so the misuse can't be silent. A future `toolbox update --description` flag will land when the service grows the PATCH surface.

## 14. Reference: Command Summary

```bash
azd ai toolbox create  <name>             --from-file <path>                                       [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox update  <name>             --default-version <n>                                    [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox delete  <name>            [--version <n>] [--force]                                 [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox show    <name>            [--version <n>]                                           [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox list                                                                                [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox version list <name>                                                                 [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]

azd ai toolbox connection add    <toolbox> <connection> [--index <name>]                           [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox connection add    <toolbox>              --from-file <path>                         [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox connection remove <toolbox> <connection> [--force]                                  [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai toolbox connection list   <toolbox>                                                         [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: `--project-endpoint` flag → azd env (`AZURE_AI_PROJECT_ENDPOINT`) → `~/.azd/config.json` (`extensions.ai-agents.context.endpoint`, read-only) → `FOUNDRY_PROJECT_ENDPOINT` → structured error.
