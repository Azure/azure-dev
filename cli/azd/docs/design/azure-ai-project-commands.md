<!-- cspell:ignore foundry huimiu exterrors -->

# Design Spec: `azd ai agent project` Context Commands + Shared Endpoint Resolution

## 1. Summary

This spec covers the workspace-level project-context commands:

- `azd ai agent project set <endpoint>` — persist an active Foundry project endpoint in azd global config.
- `azd ai agent project unset` — clear the persisted endpoint.
- `azd ai agent project show` — display the currently resolved endpoint and the source that provided it.

It also defines the endpoint-resolution behavior these commands rely on.

## 2. Scope and Non-Goals

In scope:

- The three `project` subcommands above.
- The 5-level endpoint-resolution cascade used by these commands.
- Cross-cutting flags (`--output table|json`, `--no-prompt`, `--debug`, `-p` / `--project-endpoint`) on the new commands.

Out of scope:

- Any service-side calls. These commands are pure local state management against `~/.azd/config.json`.
- A new top-level extension. The project commands live **inside the existing `azure.ai.agents` extension** for now, in clearly-separated files so they can be lifted into a future `azure.ai.project` extension without rewrite.

## 3. Extension Placement

The `project` subtree is added under the existing `azure.ai.agents` extension. No new module and no change to `registry.json`.

> **Command surface.** The existing agents extension registers its root as `agent`, so the `project` commands surface as **`azd ai agent project set | unset | show`**. The command names and config layout are chosen so a future move to a standalone `azd ai project …` extension is a registration-only change with no behavior diff.

## 4. Endpoint Resolution

### 4.1 Resolution Order

The 5-level cascade (matches feature spec § "AZD Environment Scoping"):

1. `-p` / `--project-endpoint` flag on the invoked command.
2. Active azd env value (`AZURE_AI_PROJECT_ENDPOINT`) when inside an azd project.
3. Global config: `extensions.ai-agents.context.endpoint` in `~/.azd/config.json`.
4. Environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error printed to stderr with an actionable suggestion (see §4.3).

Only `FOUNDRY_PROJECT_ENDPOINT` is honored as a host env var — no aliases. `AZURE_AI_PROJECT_ENDPOINT` is read **only** from the azd env, not from the host environment, to avoid silent precedence ambiguity.

> **Flag scope.** `--project-endpoint` (short: `-p`) is added only on the new `project` subcommands. Existing `agent` commands are **not** given a new `--project-endpoint` flag in this change; they keep the per-command overrides they have today (e.g. `--agent-endpoint` on `agent invoke`) and pick up levels 2–5 automatically through the widened resolver. The short form `-p` is already taken on `agent run` (`--port`) and `agent invoke` (`--protocol`), so `--project-endpoint` is the canonical long name.

> **Implementation note.** `FOUNDRY_PROJECT_ENDPOINT` already appears in the agents extension (`run.go` bridges `AZURE_AI_PROJECT_ENDPOINT` → `FOUNDRY_PROJECT_ENDPOINT` for the launched local-agent process), but it is not yet documented as an azd-recognized environment variable. The implementation PR must add it to [`cli/azd/docs/environment-variables.md`](../environment-variables.md) per the AGENTS.md guideline for new env vars.

### 4.2 Endpoint Validation

All five sources go through the same validator:

- Must parse as an absolute `https://` URL.
- Hostname must end with the recognized Foundry suffix `.services.ai.azure.com` — the shape produced by the existing `buildAgentEndpoint` in `agent_context.go` and used everywhere else in the codebase (CHANGELOG, `models`/`finetune` extensions, `AZURE_AI_PROJECT_ENDPOINT` value). The accepted-suffix list is kept centralized so additional canonical Foundry hostnames can be added in one place if Foundry ever introduces a new official surface.
- Path is expected to look like `/api/projects/<proj>`. Absence is a warning, not a hard failure, to leave room for future host shapes.
- Whitespace trimmed; trailing `/` stripped before persistence.

`project set` runs the same validator before writing — invalid endpoints never reach config.

### 4.3 Error Shape

When nothing resolves, the resolver returns a structured validation error. The human-readable form uses a suggestion list that does not reference per-command flags, so it is valid whether the caller exposes `--project-endpoint` or not:

```text
Error: No Foundry project endpoint resolved.

Suggestion:
  • Persist a workspace default with `azd ai agent project set <endpoint>`.
  • Set `AZURE_AI_PROJECT_ENDPOINT` in the active azd environment.
  • Export `FOUNDRY_PROJECT_ENDPOINT` in your shell.
```

When the calling command exposes `--project-endpoint` (the `project` subcommands), an extra leading bullet — `Pass --project-endpoint <url> on this command.` — is prepended by the command, not by the resolver. Existing `agent` commands, which do not have that flag, never produce that line.

With `--output json`, the same information is emitted as a structured error envelope so coding agents can parse it.

Implementation: the structured error uses `exterrors.Dependency` with a new code (e.g., `exterrors.CodeMissingProjectEndpoint`) so the azd host renders the message/suggestion/links consistently with other extension errors. Commands should **not** write a separate JSON error to stdout — the `exterrors` gRPC pipeline already handles JSON rendering when `--output json` is active.

## 5. Config Store

The persisted state lives in azd user config (`~/.azd/config.json`) under the prefix `extensions.ai-agents.context`:

```json
{
  "extensions": {
    "ai-agents": {
      "context": {
        "endpoint": "https://my-project.services.ai.azure.com/api/projects/my-project",
        "setAt": "2026-05-12T10:23:00Z"
      }
    }
  }
}
```

- `endpoint` — the normalized URL written by `project set`.
- `setAt` — RFC3339 UTC timestamp, written for diagnostics. Surfaced by `project show` in both table (as a `Set at:` line) and JSON output when the resolved source is the global config; see §6.3.

`project unset` removes the `context` subtree but leaves other sibling keys under `extensions.ai-agents` untouched.

## 6. Command Behavior

### 6.1 `azd ai agent project set <endpoint>`

Flags:

| Flag          | Type                 | Default | Notes                                                              |
| ------------- | -------------------- | ------- | ------------------------------------------------------------------ |
| `<endpoint>`  | positional, required | —       | Validated per §4.2.                                                |
| `--output`    | enum                 | `table` | `table` \| `json`.                                                 |
| `--no-prompt` | bool                 | `false` | Currently no prompts; flag accepted for cross-cutting consistency. |
| `--debug`     | bool                 | `false` | Inherits root persistent flag.                                     |

Behavior:

1. Validate the endpoint.
2. Persist `endpoint` + `setAt` to global config.
3. If invoked inside an azd project (an active azd environment exists), print a single-line warning to stderr:

   ```text
   warning: an active azd environment is present; its AZURE_AI_PROJECT_ENDPOINT takes precedence over global context.
   ```

   Informational, not an error. Suppressed when `--output json` or `--no-prompt` is set.

4. Confirmation:
   - Table: `Project endpoint set: <endpoint>`
   - JSON: `{ "endpoint": "...", "source": "globalConfig", "sourceDetail": "~/.azd/config.json", "setAt": "..." }`

   The `source` / `sourceDetail` shape matches `project show` (see §6.3) so consumers can parse a single contract.

Exit code `0` on success.

### 6.2 `azd ai agent project unset`

Flags: `--output`, `--no-prompt`, `--debug`.

Behavior:

1. If no context is currently set: print `No active project endpoint to clear.` and exit `0` (idempotent).
2. Otherwise: clear the `context` subtree.
3. Output:
   - Table: `Project endpoint cleared.`
   - JSON: `{ "cleared": true, "previousEndpoint": "..." }`

### 6.3 `azd ai agent project show`

Flags: `-p` / `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

Behavior:

1. Run the resolver, passing the flag value if present.
2. If unresolved: return the `exterrors.Dependency` structured error (non-zero exit). The azd host renders the human text to stderr and, when `--output json` is active, the structured envelope to stdout.
3. On success:
   - Table (default):

     ```text
     Project endpoint:  https://my-project.services.ai.azure.com/api/projects/my-project
     Source:            global config (~/.azd/config.json)
     Set at:            2026-05-12T10:23:00Z
     ```

     When the source is the azd env, the source line includes the env name, e.g. `azd env (dev)`, and the `Set at` line is omitted (it is only meaningful for the global-config source).

   - JSON:

     ```json
     {
       "endpoint": "https://...",
       "source": "globalConfig",
       "sourceDetail": "~/.azd/config.json",
       "azdEnv": "",
       "setAt": "2026-05-12T10:23:00Z"
     }
     ```

> The JSON shapes above are part of the public contract and must not change without a deprecation.

> **Diagnosing stale context.** Because `project set` is workspace-global, a user who switches between projects can silently resolve to the wrong endpoint until they re-run `project set`. `project show` is the documented debugging tool for this scenario: it prints both the resolved endpoint and the source, and surfaces `setAt` in the table output as a staleness hint. A time-based warning threshold (e.g., 30 days) is intentionally not added in v1 to keep the resolver behavior deterministic; revisit if user feedback indicates it is needed.

## 7. Test Plan

Unit tests (no network):

- Resolver: each level wins when higher levels are absent; each level overrides lower levels when both are present; invalid endpoint at any source surfaces a validation error rather than being silently dropped; URL normalization (trailing slash, whitespace).
- Config store: round-trip read/write/clear; `unset` is idempotent; `unset` does not delete sibling keys.
- Per command: table and JSON output snapshots; inside-azd-project warning is emitted exactly once and only when applicable; `show` source labeling for each possible source.

E2E:

- Smoke test that runs `project set` → `project show` → `project unset` → `project show` against the built extension and asserts exit codes plus stderr/stdout shape.

## 8. Impact on Existing Commands

Today, the agents extension already has a 2-level endpoint resolver (`resolveAgentEndpoint`): explicit `accountName` + `projectName` parameters first (passed in by callers — note these are **not** exposed as cobra flags on `agent` today; the only call sites pass `"", ""`), then the active azd env's `AZURE_AI_PROJECT_ENDPOINT`. This resolver is called by the existing commands:

- `azd ai agent show` (via `newAgentContext`)
- `azd ai agent invoke`
- `azd ai agent monitor` (via `newAgentContext`)
- `azd ai agent files`
- `azd ai agent sessions`

`azd ai agent run` does not call `resolveAgentEndpoint`; it reads `AZURE_AI_PROJECT_ENDPOINT` from the active azd env and bridges it to `FOUNDRY_PROJECT_ENDPOINT` for the spawned local-agent process. It is therefore *not* affected by this change.

The proposal: route this existing resolver through the new 5-level chain. The flag-parameter path is unchanged (still wins, still validated together), and the azd-env path is unchanged. The new behavior is purely additive at the tail of the cascade:

| Scenario                                                                      | Before              | After                                       |
| ----------------------------------------------------------------------------- | ------------------- | ------------------------------------------- |
| Explicit `accountName` + `projectName` parameters provided                    | Used                | Used (unchanged)                            |
| Only one of the two provided                                                  | Error               | Error (unchanged)                           |
| Inside azd project, `AZURE_AI_PROJECT_ENDPOINT` set                           | Used                | Used (unchanged)                            |
| Inside azd project, `AZURE_AI_PROJECT_ENDPOINT` unset, **`project set` done** | Error               | **Uses global config**                      |
| Outside azd project, **`project set` done**                                   | Error               | **Uses global config**                      |
| `FOUNDRY_PROJECT_ENDPOINT` set, nothing else                                  | Error               | **Uses env var**                            |
| Nothing resolvable                                                            | Error (old message) | Error (new structured message + suggestion) |

Key points:

- **Back-compat preserved.** Every input combination that resolved before produces the same endpoint after the change.
- **Error message updated.** The "nothing resolved" message now uses the structured error from §4.3 instead of the old text.
- **Existing commands gain fallback sources.** `show`, `monitor`, `files`, and `sessions` still call `resolveAgentServiceFromProject` *before* the endpoint resolver, which requires `azure.yaml` and a deployed agent, so widening the endpoint resolver alone does not make them standalone. `invoke` with a positional agent name *can* now work outside an azd project — this is intentional and consistent with the existing `--agent-endpoint` path.
- **No call-site changes.** `resolveAgentEndpoint` keeps its current signature; the new levels are internal lookups (`UserConfig`, `os.Getenv`).

Out of scope for this change (called out so they aren't surprised later):

- `service_target_agent.go` still reads `AZURE_AI_PROJECT_ENDPOINT` directly from the azd env at deploy time. Reconciling that with the broader cascade is a separate decision tied to the orchestrated (`azd up`) model.
- No new cobra flags on existing `agent` commands. The `accountName` / `projectName` parameter path through `resolveAgentEndpoint` remains as-is for callers that already pass them.

## 9. Telemetry

One event per command, reusing the extension's existing telemetry surface:

- `azd.ai.project.set` — properties: `hasAzdProject` (bool), `endpointHostHash` (sha256 of host).
- `azd.ai.project.unset` — properties: `hadValue` (bool).
- `azd.ai.project.show` — properties: `source` (enum string), `resolved` (bool).

No PII; endpoints are hashed.

## 10. Security Considerations

- The endpoint URL is not a credential. No secret material is written to or read from this config path.
- File permissions on `~/.azd/config.json` are managed by azd core; no change.
- The validator rejects non-`https` schemes and non-Foundry hostnames, preventing accidental persistence of arbitrary URLs.

## 11. Open Questions

1. Should the inside-azd-project warning on `project set` be suppressible via a flag (e.g. `--quiet`) or always-on? Current proposal: always-on, auto-suppressed when `--output json` + `--no-prompt`.
2. Should `project show` also accept `-p` as an override (useful for "what would I resolve to if I passed this flag?"), or is that semantically odd? Current proposal: yes, accept it — keeps the resolver pure and aids debugging via coding agents.

## 12. Reference: Command Summary

```bash
azd ai agent project set <endpoint>     [--output table|json] [--no-prompt] [--debug]
azd ai agent project unset              [--output table|json] [--no-prompt] [--debug]
azd ai agent project show               [-p <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: `-p` flag → azd env (`AZURE_AI_PROJECT_ENDPOINT`) → `~/.azd/config.json` (`extensions.ai-agents.context.endpoint`) → `FOUNDRY_PROJECT_ENDPOINT` → structured error.
