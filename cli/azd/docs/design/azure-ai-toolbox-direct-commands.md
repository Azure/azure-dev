<!-- cspell:ignore foundry toolbox toolboxes exterrors -->

# Design Spec: `azd ai agent toolbox` Direct Commands

## 1. Summary

This spec covers the toolbox CRUD surface for the agents extension:

- `azd ai agent toolbox create | update | delete | show | list` — manage versioned toolboxes against a Foundry project.
- `azd ai agent toolbox connection add | remove | list` — manage the connection-backed tools attached to a toolbox (MCP servers, Azure AI Search; tool shape inferred from the connection's ARM category — see § 5.6).
- `azd ai agent toolbox tag set | remove | list` — Azure resource tags (Compatibility stub in v1; see § 4.4).

A Foundry **toolbox** is a versioned, named collection of connection-backed tools that an agent references at run time. Each version carries a `tools[]` array of MCP tools (for `RemoteTool` connections) and Azure AI Search tools (for `CognitiveSearch` connections), each pointing at an existing project connection.

## 2. Scope and Non-Goals

In scope:

- The eleven verbs listed in § 1.
- Cross-cutting flags (`--output table|json`, `--no-prompt`, `--debug`, `--project-endpoint`) on every new command.
- A self-contained data-plane client at `internal/pkg/toolbox/`.

Out of scope:

- A new top-level extension. Toolbox commands live inside the existing `azure.ai.agents` extension.
- Authoring built-in tools (`code_interpreter`, `web_search`, `file_search`) into toolboxes. Built-ins are wired on the agent, not via toolboxes (issue #8143). The CLI carries through any built-in entries already present on a fetched version (§ 5.6) but provides no verb to add or remove them.
- Config-driven orchestration / `azd up` for toolboxes.
- Bicep / ARM template authoring for toolboxes.
- Cross-project toolbox copy / clone.

## 3. Extension Placement

The `toolbox` subtree is added under the existing `azure.ai.agents` extension. No new module and no change to `registry.json`. The agents extension registers its root as `agent`, so toolbox commands surface as `azd ai agent toolbox …`.

### 3.1 Modular Layout

1. All toolbox command files live under `internal/cmd/toolbox*.go`. No toolbox logic is added to existing command files.
2. All toolbox client code lives under `internal/pkg/toolbox/` (client, models, errors). It does **not** import from sibling agent packages such as `internal/pkg/agent_yaml/` or `internal/pkg/agent_runtime/`.
3. Imports are one-way: `cmd/toolbox*.go` → `internal/pkg/toolbox/` and a small set of shared helpers, each annotated with `// SHARED: <reason>`.

### 3.2 Shared Code Touchpoints

| Shared piece | Location | Reason |
| --- | --- | --- |
| Endpoint resolver | `internal/cmd/endpoint.go` | 5-level cascade defined by the project-context spec. |
| Confirmation prompt helper (`confirmDestructive`) | `internal/cmd/confirm.go` | Reused by `toolbox delete` and per-version delete. |
| `azdext.ExtensionContext` (`OutputFormat`, `NoPrompt`, `Prompt()`) | azd host gRPC | Standard extension surface. |
| Credential factory (`azidentity.NewAzureDeveloperCLICredential`) | stdlib wrapper | Same credential used by agent run. |
| Foundry data-plane pipeline factory (scope `https://ai.azure.com/.default`, `Foundry-Features` header) | `internal/pkg/azure/foundry_client.go` | Shared with agent run and the connection sub-surface. |
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
| `{endpoint}/toolboxes/{name}/mcp?api-version=v1` | n/a | MCP server exposed by the service for runtime tool consumption. The CLI does not call this URL; it surfaces the computed path on `show` (§ 5.4). |

### 4.2 Request-Body Validation (POST `/toolboxes/{name}/versions`)

- `tools` is required and non-empty (empty array → 400).
- `tools[]` is the whole tool set on every POST. To add or remove a tool, fetch the current default version's `tools[]`, mutate the in-memory array, and POST the complete result.
- `tool.name` must match `^[A-Za-z0-9_-]+$`.
- Supported `type` values: `mcp`, `azure_ai_search`, `file_search`, `code_interpreter`, `web_search`. Built-ins and connection-backed tools coexist freely.
- For `mcp`, `azure_ai_search`, and similar connection-backed entries, `project_connection_id` must be the full ARM resource ID (e.g. `/subscriptions/.../accounts/{account}/projects/{project}/connections/{name}`), not the short name. The CLI resolves names to ARM IDs before POST.

### 4.3 Missing Surfaces

No data-plane `/tags` endpoint, and toolboxes are not exposed as ARM resources (no `subscriptions/…` IDs in any toolbox response). ARM `TagsClient` is unavailable. The `tag` subgroup ships as a Compatibility stub (§ 4.4, § 5.7).

### 4.4 Tags

All three tag verbs return `exterrors.Compatibility(CodeToolboxTagsUnavailable, ...)`. The CLI surface contract (positional args, flags, output shape) is final and does not change when the verbs are activated.

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
3. Missing `--default-version` → `exterrors.Validation(CodeInvalidToolbox, "No fields to update. Specify --default-version.")`.

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
| `--version` absent | `DELETE /toolboxes/{name}`. 404 is swallowed. Also deletes any local pending-toolbox record. |
| `--version <n>`, `<n>` is not `default_version` | `DELETE /toolboxes/{name}/versions/{n}`. |
| `--version <n>`, `<n>` is `default_version`, other versions exist | `exterrors.Validation(CodeInvalidToolbox, ...)` with suggestion *"Retarget the default with `azd ai agent toolbox update --default-version <other>` first."* The CLI determines this by listing versions before issuing DELETE; the server would otherwise return 400 with the same message. |
| `--version <n>`, `<n>` is `default_version`, **only remaining version** | The service deletes the version and cascades to remove the parent toolbox. To avoid surprise destruction, the CLI **rejects** this case unless `--force` is set, returning `exterrors.Validation(CodeInvalidToolbox, "Version <n> is the only remaining version; deleting it removes the toolbox.")` with suggestion *"Run `azd ai agent toolbox delete <name>` to delete the toolbox, or pass `--force` to confirm."* With `--force`, the CLI proceeds and reports `Deleted toolbox <name> (last version removed).` |
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
2. `GET /toolboxes/{name}/versions/{version}` (or `default_version` when `--version` is absent) for the body.
3. Compute the toolbox's MCP consumption URL as `{projectEndpoint}/toolboxes/{name}/mcp?api-version=v1`.
4. Output:
   - Table: `Name`, `Default version`, `Shown version`, `Description`, `Endpoint`, `Tools` (count + list with `(builtin)` / `(connection:<id>)` annotation).
   - JSON: `{ "toolbox": <ToolboxObject>, "version": <ToolboxVersionObject>, "endpoint": "<mcp-url>" }`.

#### 5.4.1 Runtime consumption

The `endpoint` field is the contract for wiring a toolbox into agent code via env vars:

```bash
export TOOLBOX_RESEARCH_ENDPOINT=$(azd ai agent toolbox show research --output json | jq -r '.endpoint')
```

### 5.5 `azd ai agent toolbox list`

Flags: `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

Behavior:

1. `GET /toolboxes` (paginated; CLI walks all pages).
2. Merges in pending-toolbox records for the resolved endpoint and marks them `(pending)`.
3. Output:
   - Table columns: `NAME  DEFAULT-VERSION  STATE  TOOLS` (`STATE` is empty for live toolboxes, `pending` for local records; `TOOLS` is a count).
   - JSON: `{ "toolboxes": [ ..., { "name": "...", "pending": true, "description": "..." } ] }`.

### 5.6 `azd ai agent toolbox connection add | remove | list`

The `connection` subgroup is implemented on top of the toolbox versions API (§ 4.1): every tool mutation is a full-`tools[]` POST to `/toolboxes/{name}/versions`, followed by a `PATCH default_version` to make the new version active.

#### Tool-entry shape by connection category

`connection add` infers the right tool entry shape from the project connection's ARM `category`:

| Connection ARM category | Tool `type` | Tool-entry fields built from the connection | Notes |
| --- | --- | --- | --- |
| `RemoteTool` (MCP servers) | `mcp` | `name`, `server_label`, `server_url`, `project_connection_id` (full ARM ID) | `server_label` is the connection's short name; `server_url` is the connection's `target`. |
| `CognitiveSearch` | `azure_ai_search` | `name`, `azure_ai_search.indexes[].project_connection_id` (full ARM ID) | Requires `--index <name>` (index name is not on the connection). |

Other connection categories (`ApiKey`, `CustomKeys`, `AppInsights`, etc.) → `CodeInvalidToolbox` with the message *"Connection `<name>` has category `<category>`; v1 supports `RemoteTool` and `CognitiveSearch` only."*

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
| `add` | `<toolbox> <connection-name>` | If the toolbox has a pending record (§ 5.1): POST v1 with `tools=[<resolved entry>]` and the recorded description, then clear the record. (First version is automatically `default_version`, so no PATCH.) Otherwise run the shared write flow, appending the category-appropriate tool entry. Duplicate `project_connection_id` in the current default version → `CodeInvalidToolbox`. |
| `remove` | `<toolbox> <connection-name>` | Run the shared write flow, filtering out by `project_connection_id == <armId>`. Missing → `CodeInvalidToolbox` with suggestion `Run 'connection list'`. If the resulting `tools[]` would be empty → `CodeInvalidToolbox` with `Delete the toolbox instead` suggestion before POSTing. |
| `list` | `<toolbox>` | `GET /toolboxes/{name}/versions/{default_version}` and emit entries with `project_connection_id` set (including the nested form for `azure_ai_search`). Table columns: `NAME  CONNECTION  TYPE` (`CONNECTION` shows the connection's short name parsed from the ARM ID's trailing segment). |

If the ARM lookup in step 1 returns 404, the CLI returns `CodeInvalidToolbox` with the suggestion *"Run `azd ai connection list` to see available connections."*

### 5.7 `azd ai agent toolbox tag set | remove | list`

All three verbs return `exterrors.Compatibility(CodeToolboxTagsUnavailable, ...)` with the message *"Toolbox tags are not yet supported on the Foundry data plane."* Help text mirrors `az resource tag` conventions:

| Verb | Positional args |
| --- | --- |
| `set` | `<toolbox> KEY=VALUE [KEY=VALUE …]` |
| `remove` | `<toolbox> KEY [KEY …]` |
| `list` | `<toolbox>` |

Flags on all three: `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

## 6. Endpoint Resolution

The toolbox commands consume the 5-level cascade defined by the project-context spec (`azure-ai-project-commands.md` § 4):

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
- Records are cleared by `connection add` (after the first successful POST) or by `delete`.
- `toolbox list` merges in records for the resolved endpoint only.

## 8. Test Plan

Unit tests (table-driven, no network; inject a `toolboxClient` interface that is a subset of `*toolbox.Client`):

- **`create`** — new name records a pending entry and prints the registered one-liner; existing name does not POST and prints the existing one-liner; description round-trips through the pending record.
- **`update`** — `--default-version` happy path; missing flag → `CodeInvalidToolbox`.
- **`show`** — `default_version` path; explicit `--version`; table and JSON snapshots; 404 surfaces `ErrToolboxNotFound`; `endpoint` field in JSON output is exactly `{projectEndpoint}/toolboxes/<name>/mcp?api-version=v1` and is stable across `--version` changes.
- **`list`** — pagination across two pages; pending records merged and tagged `(pending)`; pending records for a different endpoint are not surfaced.
- **`delete`** —
  - Toolbox path: 204 happy path; 404 swallowed.
  - Per-version path (non-default): 204 happy path.
  - Per-version path (default, other versions exist): CLI rejects pre-flight with `CodeInvalidToolbox` and the retarget suggestion.
  - Per-version path (default, only remaining version): CLI rejects without `--force`; with `--force` proceeds and reports the cascaded toolbox removal.
  - `--no-prompt` without `--force` → `CodeMissingForceFlag`.
- **`connection add`** —
  - `RemoteTool` category: ARM lookup resolves `target` → `server_url`; tool entry is `{type:"mcp", name, server_label, server_url, project_connection_id}`.
  - `CognitiveSearch` category: requires `--index <name>`; tool entry is `{type:"azure_ai_search", name, azure_ai_search:{indexes:[{project_connection_id, index_name}]}}`. Missing `--index` → `CodeInvalidToolbox`.
  - Unsupported category (`ApiKey`, `CustomKeys`, `AppInsights`, …) → `CodeInvalidToolbox` with the category in the message; no toolbox-side calls made.
  - `--index` on a `RemoteTool` connection → `CodeInvalidToolbox` (flag rejected for non-search categories).
  - Pending-record promotion: POSTs v1 with the resolved tool entry and the recorded description; clears the pending record. No PATCH.
  - Existing-toolbox path: fetch default version → append entry → POST new version → PATCH `default_version`.
  - Duplicate `project_connection_id` in the current default version → `CodeInvalidToolbox` before any POST.
  - Connection name not on the project (ARM 404) → `CodeInvalidToolbox` with the "Run `azd ai connection list`" suggestion; no toolbox-side calls made.
- **`connection remove`** —
  - Happy path: POST new version with the entry filtered out (works for both `mcp` and nested `azure_ai_search` entries); PATCH `default_version`.
  - Missing connection → `CodeInvalidToolbox`.
  - Removing a tool that would leave `tools[]` empty → `CodeInvalidToolbox` with the "delete the toolbox instead" suggestion; service is not called.
- **`connection list`** — emits entries with `project_connection_id` set (top-level on `mcp`, nested under `azure_ai_search.indexes[]`); table `TYPE` column shows each entry's tool `type`; respects `--output`.
- **`tag set | remove | list`** — assert `Compatibility` error code and stable help text.

E2E:

- Smoke test that runs `create → connection add → list → show → connection remove → delete` against the built extension and asserts exit codes plus stdout/stderr shape.

Snapshots: `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'` from `cli/azd`.

## 9. Impact on Existing Commands

None at the command level. The toolbox surface is purely additive:

- `internal/cmd/root.go` registers the new toolbox parent alongside `session`, `files`, and `connection`. No existing command's flags, behavior, or output shape is changed.
- `internal/exterrors/codes.go` gains new constants (§ 11); existing codes are not touched.
- `internal/pkg/toolbox/` is a new directory; no existing package imports it.

## 10. Telemetry

One event per command, reusing the extension's existing telemetry surface. All include `endpointHostHash` (sha256 of host) and `resolvedSource` (enum string from the cascade).

| Event | Additional properties |
| --- | --- |
| `azd.ai.toolbox.create` | `pending` (bool — always true in v1), `hasDescription` (bool) |
| `azd.ai.toolbox.update` | — |
| `azd.ai.toolbox.delete` | `scope` (`toolbox` \| `version`), `forced` (bool) |
| `azd.ai.toolbox.show` | `versionMode` (`default` \| `explicit`) |
| `azd.ai.toolbox.list` | `count` (int), `pendingCount` (int) |
| `azd.ai.toolbox.connection.add` | `promotedFromPending` (bool), `armResolveOk` (bool) |
| `azd.ai.toolbox.connection.remove` | — |
| `azd.ai.toolbox.connection.list` | `count` (int) |
| `azd.ai.toolbox.tag.{set,remove,list}` | `outcome=compatibility_stub` |

No PII. `endpointHostHash` is sha256 of the project endpoint hostname; toolbox names are sent as-is (user-chosen labels with no credential value).

## 11. Errors

New codes added to `internal/exterrors/codes.go`:

| Code | Used by |
| --- | --- |
| `CodeInvalidToolbox` | `update`, `delete --version <default>`, `connection add` (duplicate), `connection remove` (missing). |
| `CodeMissingForceFlag` | `delete` with `--no-prompt` and without `--force`. |
| `CodeToolboxTagsUnavailable` | All `tag` verbs (Compatibility). |

New `Op*` constants for `exterrors.ServiceFromAzure`:

```
OpRegisterPendingToolbox
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
- Endpoint URLs and toolbox names are persisted in plain text to `~/.azd/config.json` for pending records (§ 7). Endpoints are not credentials. File permissions are managed by azd core; no change.
- The data-plane client uses the existing Foundry pipeline factory, inheriting its TLS / proxy configuration.

## 13. Open Questions

1. Should `create` also accept an optional `--connection <name>` flag to publish v1 immediately, skipping the pending-record step? Current proposal: no — the pending-record model keeps `create` non-network and matches the "one command, one action" principle from the umbrella spec.
2. Should `toolbox list` surface every version's tool count, or just the default-version's count? Current proposal: just the default — matches what `show` displays by default and keeps the list call to a single GET per toolbox.
3. Should `connection add` accept `--as <alias>` to decouple the tool entry's `name` from the connection name? Current proposal: no — defer until users ask.

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

azd ai agent toolbox tag set    <toolbox> KEY=VALUE [KEY=VALUE …]                                        [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent toolbox tag remove <toolbox> KEY [KEY …]                                                    [--project-endpoint <url>] [--no-prompt] [--debug]
azd ai agent toolbox tag list   <toolbox>                                                                [--project-endpoint <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: `--project-endpoint` flag → azd env (`AZURE_AI_PROJECT_ENDPOINT`) → `~/.azd/config.json` (`extensions.ai-agents.context.endpoint`) → `FOUNDRY_PROJECT_ENDPOINT` → structured error.
