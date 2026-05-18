# Design Spec: `azd ai agent routine` Commands

## 1. Summary

This spec covers the `routine` command subtree under the existing `azure.ai.agents`
extension. A routine pairs one trigger (when) with one action (what) on a Foundry
project — e.g. "every weekday at 8 AM UTC, invoke `daily-report-agent`" — without
standing up Logic Apps / Functions / cron infra.

Commands registered in v1:

- `azd ai agent routine create <name>`
- `azd ai agent routine update <name>`
- `azd ai agent routine show <name>`
- `azd ai agent routine list`
- `azd ai agent routine delete <name>`
- `azd ai agent routine enable <name>`
- `azd ai agent routine disable <name>`
- `azd ai agent routine dispatch <name>`
- `azd ai agent routine run list <routine>`

`routine run show` and `routine run delete` are deferred until their APIs ship
([§4.8](#48-routine-run-show--routine-run-delete)).


## 2. Scope, Placement, and Non-Goals

### Placement

The `routine` subtree lives inside the existing `azure.ai.agents` extension,
alongside `project`, `invoke`, `show`, `monitor`, `files`, and `sessions`. Same
pattern as [`project.go`](../../extensions/azure.ai.agents/internal/cmd/project.go):
`newRoutineCommand(extCtx)` wired into `root.go`, one file per verb, with a
sub-`run` group via `newRoutineRunCommand`. No new extension; no `registry.json`
change.

> **Command surface.** The agents extension registers its root as `agent`, so
> these commands surface as **`azd ai agent routine …`** today. The eventual
> umbrella surface is `azd ai routine …` after the extension is split/renamed,
> which is a registration-only change with no behavior diff. See feature issue
> [#8159](https://github.com/Azure/azure-dev/issues/8159) for the umbrella
> context.

### Impact on existing commands

`routine` is purely additive. No changes to `agent` (`run`, `invoke`, `show`,
`monitor`, `files`, `sessions`), `project` (`set` / `unset` / `show`), or
`azure.yaml`. No new persistent state in `~/.azd/config.json`. The existing
`agent invoke` and the new `routine dispatch` deliberately overlap: `dispatch`
is the trigger-side manual fire (records a `RoutineRunDto`); `invoke` is the
direct agent call (does not). Both must keep working.

### In scope

- The commands listed in [§1](#1-summary).
- Mapping from CLI flags onto the wire format in [TypeSpec PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186) (merged into `feature/foundry-release`).
- Reuse of the 5-level project endpoint resolver (flag → azd env → global config → `FOUNDRY_PROJECT_ENDPOINT` → structured error).

### Out of scope

- Declarative routines (`routine.yaml`, `azd provision` integration, `azd up`) —
  the imperative `routine create`/`update` verbs in this spec cover the v1 jobs-to-be-done;
  the declarative `routine.yaml` + `provision`/`up` story belongs to the future
  orchestrated config-driven model and is intentionally out of scope here.
- Multi-trigger routines via the CLI — deferred ([§7 OQ-2](#7-open-questions)).
- Changing `--trigger` or `--action` *type* on an existing routine — delete and
  recreate, mirroring the `connection` auth-type rule ([§4.2](#42-create-vs-update)).
- Creating routines from a file (`--file`) — tracked as [#8187](https://github.com/Azure/azure-dev/issues/8187).

## 3. Endpoint Resolution

Every `routine` subcommand resolves the Foundry project endpoint through the
standard 5-level cascade: `-p` / `--project-endpoint` flag → active azd env
(`AZURE_AI_PROJECT_ENDPOINT`) → global config (the `endpoint` field of the
`extensions.ai-agents.project.context` object, written by
`azd ai agent project set`) → `FOUNDRY_PROJECT_ENDPOINT` env var → structured
dependency error (code `CodeMissingProjectEndpoint`).

Standalone usability is required: every `routine` subcommand must work outside an
azd project given a resolvable endpoint, matching `connection`, `toolbox`, and
`skill`.

The preview opt-in header `x-ms-foundry-features-opt-in: Routines=V1Preview` is
sent on every routine data-plane call (per TypeSpec `RoutinesPreviewHeader`); it
is set by the extension, not user-configurable.

> **Implementation checklist.** The implementation PR must add
> `FOUNDRY_PROJECT_ENDPOINT` to
> [`docs/environment-variables.md`](../environment-variables.md) if not already
> documented by the project-context work (per AGENTS.md guidelines).

## 4. Command Behavior

Cross-cutting flags on every subcommand: `--output table|json`, `--no-prompt`,
`--debug`, `-p` / `--project-endpoint`.

### 4.1 `routine create <name>`

Required positional: `<name>`.\
Required flags (always): `--trigger <recurring|timer|github-issue>` (enum, not free-form; see [§5.1](#51-trigger-flags--routinetrigger-discriminator) for the supported types and per-type required flags).\
Conditionally required flags: per trigger/action type (see [§5.1](#51-trigger-flags--routinetrigger-discriminator) / [§5.2](#52-action-flags--routineaction-discriminator)).

Optional flags:

| Flag                 | Notes                                                             |
| -------------------- | ----------------------------------------------------------------- |
| `--description`      | Free-form text.                                                   |
| `--action`           | Defaults to `agent-response`.                                     |
| `--enabled`          | Bool. Defaults to `true` on creation. Pass `--enabled=false` to create disabled. |
| `--force`            | Allow PUT to overwrite an existing routine (upsert). Without it, `create` fails if `<name>` already exists. |

**Prompt / no-prompt** — mirrors `connection create`:

- Interactive: missing required per-trigger / per-action flags are prompted for.
- `--no-prompt`: exits non-zero with a structured validation error listing missing flags.

**Output:**

- Table: `Routine 'daily-ops-report' created.` plus a short summary block.
- JSON: the server's `Routine` body, normalized.

### 4.2 Create vs. Update

The data plane exposes a single idempotent `PUT /routines/{name}`. The CLI splits
it into two verbs for usability.

**Create semantics.** Fails by default if the resource exists. `--force` makes it
an upsert (matches `connection create --force`).

**Update semantics.** GET-then-PUT internally — only the named flags change; all
other fields are preserved verbatim. Accepted flags: `--description`, `--cron`,
`--time-zone`, `--at`, `--agent-name`, `--agent-endpoint-id`, `--conversation-id`,
`--session-id`.

**Type-switch guard.** `--trigger` and `--action` are registered on `update`
solely to surface a friendly client-side error when supplied: the command exits
non-zero with a `delete and recreate` suggestion before calling the service.
This mirrors the `connection` auth-type rule.

**Post-merge validation.** After applying the named fields, `update` validates
the merged body against the existing trigger/action type:
- Action-specific flags are accepted only for the current action type
  (`--conversation-id` → `agent-response`; `--session-id` → `agent-invoke`).
- For `agent-response`, `--agent-name` and `--agent-endpoint-id` remain mutually
  exclusive: specifying one clears the other; specifying both is a validation
  error.
- If the merged body no longer satisfies required fields for its trigger/action
  type, the command exits with a structured validation error before calling the service.

### 4.3 `routine show <name>` / `routine list`

Standard read commands. `list` auto-pages via `continuation_token`. In
`--output table`, one row per routine. In `--output json`, a single stable
object: `{ "value": [ ... ], "continuation_token": "" }` (empty token because
all pages are drained).

### 4.4 `routine delete <name>`

Confirmation prompt by default. `--force` skips it. In `--no-prompt` mode,
`--force` is required; without it the command exits non-zero with a structured
validation error. Matches `connection delete`.

### 4.5 `routine enable | disable <name>`

Dedicated verbs that map directly to the service's dedicated action routes
defined in [TypeSpec PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186):
`POST /routines/{name}:enable` and `POST /routines/{name}:disable`. Calling
these routes directly avoids the TOCTOU race that a client-side GET-then-PUT
toggle would introduce.

Both are idempotent: enabling an already-enabled routine (or disabling an
already-disabled one) is a no-op success. Non-existent routines surface the
service's 404.

### 4.6 `routine dispatch <name>`

The only dispatch route in [TypeSpec PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186)
is `POST /routines/{name}:dispatch_async`; both sync (default) and `--async`
modes call it. The `--async` flag controls only client-side waiting behavior,
not which route is used.

| Flag                  | Notes                                                                |
| --------------------- | -------------------------------------------------------------------- |
| `--async`             | Returns `dispatch_id` immediately after the `:dispatch_async` call.  |
| `--input "<text>"`    | Plain-text user-message payload wrapped into `RoutineDispatchPayload`. The string is passed through verbatim; JSON content is not parsed by the CLI. |
| `--conversation-id`   | Preview — forwarded as `conversation_id` for `agent-response` routines. Not yet in TypeSpec ([§7 OQ-3](#7-open-questions)). |

> **Implementation note.** A leading `GET /routines/{name}` is performed when
> any payload-level flag is set (`--input` and/or `--conversation-id`) to derive
> the action type. When neither flag is provided, the CLI sends an empty body
> (`{}`) and skips the GET; dispatch telemetry records `actionType` as `unknown`
> in that path.

**Output:** both modes hit `:dispatch_async`; the default mode polls the
returned `dispatch_id` and streams the agent response back to the user, while
`--async` returns the raw `DispatchRoutineResponse` immediately.

| Mode    | Table                                                                          | JSON                             |
| ------- | ------------------------------------------------------------------------------ | -------------------------------- |
| Default | Agent response streamed + `dispatch_id` / `action_correlation_id` trailer      | `DispatchRoutineResponse` body   |
| `--async` | `DispatchRoutineResponse` (no streaming)                                     | Same                             |

### 4.7 `routine run list <routine>`

Maps onto `GET /routines/{routine_name}/runs`:

| CLI flag      | Query param        |
| ------------- | ------------------ |
| `--top N`     | `maxResults` per page; CLI stops auto-paging once `N` items have been returned |
| `--filter`    | `filter`           |

`--orderby` is intentionally **not** registered in v1: `ListRoutineRunsParameters`
in [TypeSpec PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186)
only exposes pagination plus `filter`. The flag will be added when (and if) the
service grows an `orderBy` query parameter.

Auto-pagination via `pageToken` / `next_page_token`, same rules as `routine list`
([§4.3](#43-routine-show-name--routine-list)). When `--top N` is set the CLI
caps the total returned at `N` items across all drained pages.

### 4.8 `routine run show` / `routine run delete`

**Not registered in v1.** The `GET /routines/{name}/runs/{run-id}` endpoint
needed for `run show` was [added in TypeSpec PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186/files#diff-0920b2f67a7816e1e9ef440782ce714e40358a2a5c161b322271b19c19fb1e9fR163);
`run delete` is still not in the TypeSpec. Both verbs will be added as a
strictly additive change in a follow-up PR, with no churn on already-shipped
verbs.

### Output shapes for state-changing verbs

| Command   | Table output                   | JSON output                         |
| --------- | ------------------------------ | ----------------------------------- |
| `create`  | `Routine '<name>' created.` + summary | Server `Routine` body          |
| `update`  | `Routine '<name>' updated.` + summary of changed fields | Updated `Routine` body |
| `delete`  | `Routine '<name>' deleted.`    | `{ "deleted": true, "name": "<name>" }` |
| `enable`  | `Routine '<name>' enabled.`    | Updated `Routine` body              |
| `disable` | `Routine '<name>' disabled.`   | Updated `Routine` body              |

## 5. Wire Format Mapping

### 5.1 Trigger flags → `RoutineTrigger` discriminator

> **Why `recurring` and not `schedule`?** Feature issue [#8159](https://github.com/Azure/azure-dev/issues/8159)
> uses `schedule` (the API discriminator name). The CLI uses `recurring` because
> it reads more naturally alongside `timer` on the command line, and the CLI
> already kebab-cases multi-word values everywhere. A single mapping table
> absorbs any upstream rename. See [§7 OQ-1](#7-open-questions).

| CLI `--trigger` | TypeSpec `type`  | Required CLI flags                                                   | Status |
| --------------- | ---------------- | -------------------------------------------------------------------- | ------ |
| `recurring`     | `schedule`       | `--cron "<expr>"`, `--time-zone <tz>`                                | v1     |
| `timer`         | `timer`          | `--at "<ISO 8601>"`, `--time-zone <tz>`                              | v1     |
| `github-issue`  | `github_issue`   | `--connection <id>`, `--assignee <a>`, `--repository <r>`                                 | Deferred — pending workspace connection model |

CLI emits `triggers: { "default": { "type": "<wire>", ... } }` to match the
TypeSpec `Record<RoutineTrigger>` shape. The key `"default"` is an implementation
detail (single-trigger CLI shape) and is not surfaced to the user.

> **Heads-up.** The Foundry team is adding a generic event-based trigger plus
> additional strong-typed triggers to the TypeSpec shortly after #43186. The
> mapping table absorbs new rows additively; CLI aliases will be added as those
> trigger types land, without churn on the verbs above.

### 5.2 Action flags → `RoutineAction` discriminator

| CLI `--action`          | TypeSpec `type`                  | Required CLI flags                              | Optional CLI flags    |
| ----------------------- | -------------------------------- | ----------------------------------------------- | --------------------- |
| `agent-response` (def.) | `invoke_agent_responses_api`     | one of `--agent-name` / `--agent-endpoint-id`   | `--conversation-id`   |
| `agent-invoke`          | `invoke_agent_invocations_api`   | `--agent-endpoint-id`                           | `--session-id`        |

`--agent-name` maps to the TypeSpec `agent_name` field (the project-scoped
agent name, max 256 chars) — not an opaque ID. For `agent-response`, the CLI
validates "exactly one of `--agent-name` / `--agent-endpoint-id`" locally
before the PUT.

### 5.3 Routes and API status

All requests include the `RoutinesPreviewHeader` and the `api-version=v1` query
parameter, matching the existing toolboxes/agents Foundry clients in this
extension (for example
[`listen.go`](../../extensions/azure.ai.agents/internal/cmd/listen.go) builds
`/toolboxes/{name}/versions/{version}/mcp?api-version=v1`). The
`continuationToken` and `pageToken` query parameters are added on top of
`api-version` where applicable.

| CLI verb                              | HTTP                                                          | API status      |
| ------------------------------------- | ------------------------------------------------------------- | --------------- |
| `routine create` / `routine update`   | `PUT  {endpoint}/routines/{name}`                             | Ready           |
| `routine show`                        | `GET  {endpoint}/routines/{name}`                             | Ready           |
| `routine list`                        | `GET  {endpoint}/routines` (with `continuationToken`)         | Ready           |
| `routine delete`                      | `DELETE {endpoint}/routines/{name}`                           | Ready           |
| `routine enable`                      | `POST {endpoint}/routines/{name}:enable`                      | Ready           |
| `routine disable`                     | `POST {endpoint}/routines/{name}:disable`                     | Ready           |
| `routine dispatch` (default and `--async`) | `POST {endpoint}/routines/{name}:dispatch_async` ([§4.6](#46-routine-dispatch-name)) | Ready           |
| `routine run list`                    | `GET  {endpoint}/routines/{name}/runs`                        | Ready           |
| `routine run show` *(deferred)*       | `GET  {endpoint}/routines/{name}/runs/{run-id}`               | Ready in TypeSpec; registration deferred |
| `routine run delete` *(deferred)*     | `DELETE {endpoint}/routines/{name}/runs/{run-id}`             | Not in TypeSpec |

Additional API gaps not captured in the routes table:

- **`conversation_id` on `DispatchRoutineRequest`**: Not in TypeSpec PR; CLI
  accepts `--conversation-id` as preview ([§7 OQ-3](#7-open-questions)).
- **Trigger / action discriminator aliases**: `agent_response` / `agent_invoke`
  requested upstream; CLI kebab-case aliases absorb any rename.

## 6. Telemetry

One event per command, on the existing agents-extension surface. No PII;
endpoints hashed.

| Event                          | Properties                                                                |
| ------------------------------ | ------------------------------------------------------------------------- |
| `azd.ai.routine.create`        | `trigger`, `action`, `forced` (bool), `hasAzdProject` (bool)              |
| `azd.ai.routine.update`        | `fieldsChanged` (count), `hasAzdProject`                                  |
| `azd.ai.routine.show`          | `source` (resolver), `resolved` (bool)                                    |
| `azd.ai.routine.list`          | `pageCount`, `resolved`                                                   |
| `azd.ai.routine.delete`        | `forced`, `existed` (bool)                                                |
| `azd.ai.routine.enable`        | `previouslyEnabled` (bool)                                                |
| `azd.ai.routine.disable`       | `previouslyEnabled`                                                       |
| `azd.ai.routine.dispatch`      | `async` (bool), `actionType` (`unknown` allowed), `hasInput`, `hasConversationId` |
| `azd.ai.routine.run.list`      | `pageCount`, `top`, `hasFilter`                                           |

## 7. Open Questions

| # | Question | Default proposal |
|---|----------|------------------|
| 1 | **Trigger / action enum names.** CLI aliases (`recurring`, `agent-response`, `agent-invoke`) vs. 1:1 API parity (`schedule`, `invoke_agent_responses_api`, …). Note: feature issue [#8159](https://github.com/Azure/azure-dev/issues/8159) uses `schedule`; this spec proposes `recurring`. | Ship CLI aliases. API names are verbose on the command line; a single mapping table absorbs upstream renames. |
| 2 | **Multi-trigger routines.** TypeSpec `triggers` is `Record<RoutineTrigger>`. Add `routine trigger add \| remove \| list` now? | Defer. All hero scenarios use one trigger, keyed as `"default"`. Re-evaluate when a real multi-trigger scenario lands. |
| 3 | **`--conversation-id` on dispatch.** Field is in the routines conceptual spec but not in TypeSpec PR #43186. | Ship the flag, mark preview-only in `--help`. If the service rejects unknown fields, the user sees a service error and re-runs without it. Revisit on TypeSpec lock. |

## 8. Test Plan

### Unit tests (no network)

- Flag → wire mapping for each `(--trigger, --action)` combination ([§5.1](#51-trigger-flags--routinetrigger-discriminator) / [§5.2](#52-action-flags--routineaction-discriminator)), including the `triggers.default` key.
- Per-kind required-flag prompt vs. `--no-prompt` error shape.
- `update`: GET-then-PUT round-trip preserves untouched fields; type-switch
  rejection; post-merge validation rejects wrong-action flags; `agent-response`
  identity updates clear the peer field.
- `create` vs. `create --force` against a pre-existing routine.
- `enable` / `disable` idempotency; dedicated `:enable` / `:disable` route calls (not GET-then-PUT).
- `dispatch` default vs. `--async` both hit `:dispatch_async`; default mode polls
  and streams while `--async` returns immediately; leading GET triggered/skipped
  based on payload flags; `actionType` telemetry `unknown` in the no-payload path.
- `run list` query-param mapping (`--top` → `maxResults`, `--filter` → `filter`) and pagination; JSON output is one stable object.
- `delete --no-prompt` without `--force` produces a structured validation error.
- Output shapes match [§4 table](#output-shapes-for-state-changing-verbs) in both
  table and JSON modes.

### E2E

Smoke test: `routine create` (recurring + agent-response) → `show` → `disable` →
`enable` → `dispatch --async` → `run list` → `delete`. Asserts exit codes and
output shape. Skipped when no Foundry project endpoint is resolvable in CI
(mirrors existing agents-extension E2E gate).

## 9. Reference: Command Summary

```bash
azd ai agent routine create <name> \
  --trigger <recurring|timer> \
  [--cron "0 8 * * *"] [--time-zone UTC] \
  [--at "2026-04-24T15:00:00Z"] \
  [--action <agent-response|agent-invoke>] \
  [--agent-name <name>] [--agent-endpoint-id <id>] \
  [--conversation-id <id>] [--session-id <id>] \
  [--description "..."] [--enabled=false] [--force]

azd ai agent routine update <name> \
  [--description ...] [--cron ...] [--time-zone ...] [--at ...] \
  [--agent-name ...] [--agent-endpoint-id ...] \
  [--conversation-id ...] [--session-id ...]

azd ai agent routine show <name>
azd ai agent routine list
azd ai agent routine delete <name> [--force]

azd ai agent routine enable <name>
azd ai agent routine disable <name>

azd ai agent routine dispatch <name> [--async] [--input "<text>"] [--conversation-id <id>]

azd ai agent routine run list <routine> [--top N] [--filter ...]
```

Cross-cutting on every command: `--output table|json`, `--no-prompt`, `--debug`,
`-p` / `--project-endpoint`.
