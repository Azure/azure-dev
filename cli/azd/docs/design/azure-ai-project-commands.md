<!-- cspell:ignore foundry -->

# Design Spec: `azd ai project` Context Commands + Shared Endpoint Resolution

- Feature spec: `azd ai` Direct Commands (CLI Surface for Foundry)
- Tracking issue: [Azure/azure-dev#8124](https://github.com/Azure/azure-dev/issues/8124)
- Owner: @huimiu
- Status: Draft

## 1. Summary

This spec covers the workspace-level project-context commands:

- `azd ai project set <endpoint>` — persist an active Foundry project endpoint in azd global config.
- `azd ai project unset` — clear the persisted endpoint.
- `azd ai project show` — display the currently resolved endpoint and the source that provided it.

It also defines the endpoint-resolution behavior these commands rely on.

## 2. Scope and Non-Goals

In scope:

- The three `project` subcommands above.
- The 5-level endpoint-resolution chain used by these commands.
- Cross-cutting flags (`--output table|json`, `--no-prompt`, `--debug`, `-p` / `--project-endpoint`) on the new commands.

Out of scope:

- Any service-side calls. These commands are pure local state management against `~/.azd/config.json`.
- A new top-level extension. The project commands live **inside the existing `azure.ai.agents` extension** for now, in clearly-separated files so they can be lifted into a future `azure.ai.project` extension without rewrite.

## 3. Extension Placement

The `project` subtree is added under the existing `azure.ai.agents` extension, in clearly-separated files so it can later be lifted into a dedicated extension without rewrite. No new module and no change to `registry.json`.

> **Command surface note.** The existing agents extension registers its root as `agent`, so commands surface as `azd ai agent …`. The feature spec uses the unprefixed form `azd ai project …`. Because we are not creating a new extension yet, the v1 implementation will surface as **`azd ai agent project set | unset | show`**. The command names and config layout are chosen so a future move to `azd ai project …` is a registration-only change with no behavior diff. See Open Question 1.

## 4. Endpoint Resolution

### 4.1 Resolution Order

The 5-level cascade (matches feature spec § "AZD Environment Scoping"):

1. `-p` / `--project-endpoint` flag on the invoked command.
2. Active azd env value (`AZURE_AI_PROJECT_ENDPOINT`) when inside an azd project.
3. Global config: `extensions.ai-agents.context.endpoint` in `~/.azd/config.json`.
4. Environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error printed to stderr with an actionable suggestion (see §4.3).

Only `FOUNDRY_PROJECT_ENDPOINT` is honored as a host env var — no aliases. `AZURE_AI_PROJECT_ENDPOINT` is read **only** from the azd env, not from the host environment, to avoid silent precedence ambiguity.

> **Flag scope.** Level 1 (`-p` / `--project-endpoint`) applies to the new `project` subcommands and future resource commands (`connection`, `toolbox`, `skill`). It is **not** added to existing `agent` commands (`show`, `invoke`, `monitor`, `files`, `session`), which already have their own endpoint overrides (`--account-name` + `--project-name`, `--agent-endpoint`). The existing agent commands benefit from levels 2–5 via the widened `resolveAgentEndpoint` body. Note: `-p` as a short flag is already taken on `agent run` (`--port`) and `agent invoke` (`--protocol`), so the long form `--project-endpoint` is the stable cross-command flag name.

### 4.2 Endpoint Validation

All five sources go through the same validator:

- Must parse as an absolute `https://` URL.
- Hostname must end with the Foundry suffix `.services.ai.azure.com`.
- Path is expected to look like `/api/projects/<proj>`. Absence is a warning, not a hard failure, to leave room for future host shapes.
- Whitespace trimmed; trailing `/` stripped before persistence.

`project set` runs the same validator before writing — invalid endpoints never reach config.

### 4.3 Error Shape

When nothing resolves, the resolver returns a structured validation error. Human-readable form:

```text
Error: No Foundry project endpoint resolved.

Suggestion: Run `azd ai agent project set <endpoint>` to set one,
            or pass `--project-endpoint <url>` on this command,
            or set the FOUNDRY_PROJECT_ENDPOINT environment variable.
```

> The suggestion text uses the v1 surface form (`azd ai agent project set`). When the command moves to `azd ai project set` (see Open Question 1), update the suggestion accordingly.

With `--output json`, the same information is emitted as a structured error envelope so coding agents can parse it.

Implementation: the structured error uses `exterrors.Dependency` with a new code (e.g., `exterrors.CodeMissingProjectEndpoint`) so the azd host renders the message/suggestion/links consistently with other extension errors. Commands should **not** write a separate JSON error to stdout — the `exterrors` gRPC pipeline already handles JSON rendering when `--output json` is active.

## 5. Config Store

The persisted state lives in azd user config (`~/.azd/config.json`) under the prefix `extensions.ai-agents.context`:

```jsonc
{
  "extensions": {
    "ai-agents": {
      "context": {
        "endpoint": "https://my-project.services.ai.azure.com/api/projects/my-project",
        "setAt": "2026-05-12T10:23:00Z",
      },
    },
  },
}
```

- `endpoint` — the normalized URL written by `project set`.
- `setAt` — RFC3339 UTC timestamp, written for diagnostics. Surfaced only in `project show --output json`.

`project unset` removes the `context` subtree but leaves other sibling keys under `extensions.ai-agents` untouched.

## 6. Command Behavior

### 6.1 `azd ai project set <endpoint>`

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

   Informational, not an error. Suppressed when `--output json` and `--no-prompt` are both set.

4. Confirmation:
   - Table: `Project endpoint set: <endpoint>`
   - JSON: `{ "endpoint": "...", "source": "global config (~/.azd/config.json)", "setAt": "..." }`

Exit code `0` on success.

### 6.2 `azd ai project unset`

Flags: `--output`, `--no-prompt`, `--debug`.

Behavior:

1. If no context is currently set: print `No active project endpoint to clear.` and exit `0` (idempotent).
2. Otherwise: clear the `context` subtree.
3. Output:
   - Table: `Project endpoint cleared.`
   - JSON: `{ "cleared": true, "previousEndpoint": "..." }`

### 6.3 `azd ai project show`

Flags: `-p` / `--project-endpoint`, `--output`, `--no-prompt`, `--debug`.

Behavior:

1. Run the resolver, passing the flag value if present.
2. If unresolved: return the `exterrors.Dependency` structured error (non-zero exit). The azd host renders the human text to stderr and, when `--output json` is active, the structured envelope to stdout.
3. On success:
   - Table (default):

     ```text
     Project endpoint:  https://my-project.services.ai.azure.com/api/projects/my-project
     Source:            global config (~/.azd/config.json)
     ```

     When the source is the azd env, the source line includes the env name, e.g. `azd env (dev)`.

   - JSON:

     ```json
     {
       "endpoint": "https://...",
       "source": "globalConfig",
       "sourceDetail": "~/.azd/config.json",
       "azdEnv": ""
     }
     ```

### 6.4 Output Formatting

- Default is `table` (human-readable). JSON is opt-in.
- The JSON shapes above are part of the public contract and must not change without a deprecation.

## 7. Test Plan

Unit tests (no network):

- Resolver: each level wins when higher levels are absent; each level overrides lower levels when both are present; invalid endpoint at any source surfaces a validation error rather than being silently dropped; URL normalization (trailing slash, whitespace).
- Config store: round-trip read/write/clear; `unset` is idempotent; `unset` does not delete sibling keys.
- Per command: table and JSON output snapshots; inside-azd-project warning is emitted exactly once and only when applicable; `show` source labeling for each possible source.

E2E:

- Smoke test that runs `project set` → `project show` → `project unset` → `project show` against the built extension and asserts exit codes plus stderr/stdout shape.

## 8. Impact on Existing Commands

Today, the agents extension already has a 2-level endpoint resolver (`resolveAgentEndpoint`): explicit `--account-name` + `--project-name` flags first, then the active azd env's `AZURE_AI_PROJECT_ENDPOINT`. This resolver is called by the existing commands:

- `azd ai agent show`
- `azd ai agent invoke`
- `azd ai agent monitor`
- `azd ai agent files`
- `azd ai agent session`

(Plus the deployment path `service_target_agent.go`, which reads `AZURE_AI_PROJECT_ENDPOINT` directly from the azd env and is **not** in scope for this change.)

The proposal: route this existing resolver through the new 5-level chain. The `--account-name` / `--project-name` path is unchanged (still wins, still validated together), and the azd-env path is unchanged. The new behavior is purely additive at the tail of the cascade:

| Scenario                                                                      | Before              | After                                       |
| ----------------------------------------------------------------------------- | ------------------- | ------------------------------------------- |
| `--account-name` + `--project-name` provided                                  | Used                | Used (unchanged)                            |
| Only one of the two provided                                                  | Error               | Error (unchanged)                           |
| Inside azd project, `AZURE_AI_PROJECT_ENDPOINT` set                           | Used                | Used (unchanged)                            |
| Inside azd project, `AZURE_AI_PROJECT_ENDPOINT` unset, **`project set` done** | Error               | **Uses global config**                      |
| Outside azd project, **`project set` done**                                   | Error               | **Uses global config**                      |
| `FOUNDRY_PROJECT_ENDPOINT` set, nothing else                                  | Error               | **Uses env var**                            |
| Nothing resolvable                                                            | Error (old message) | Error (new structured message + suggestion) |

Implications:

- **Back-compat is preserved**: every input combination that produced a valid endpoint today produces the same endpoint after the change.
- **Error message changes** when nothing resolves. The old text ("Provide `--account-name` and `--project-name` flags, or run `azd init`...") is replaced by the structured error in §4.3, which is the right surface for both `agent` commands and the new `project` commands.
- **`azd ai agent` commands now work in more situations than before** — specifically, when only global config or `FOUNDRY_PROJECT_ENDPOINT` is set. This is a strict expansion, not a regression. The practical impact varies by command:
  - **`show`, `monitor`, `files`, `session`** — these call `resolveAgentServiceFromProject` *before* the endpoint resolver, which requires `azure.yaml` and a deployed agent. Widening the endpoint resolver alone does not make them standalone; they still fail without an azd project.
  - **`run`** — requires `azure.yaml` for the service source directory and startup command. Still not standalone.
  - **`invoke`** (without `--agent-endpoint`) — when a positional agent name is provided, `resolveAgentServiceFromProject` failure is best-effort (the name comes from the argument). After this change, `azd ai agent invoke my-agent "hi"` can succeed outside an azd project if global config or `FOUNDRY_PROJECT_ENDPOINT` resolves. This is an intentional expansion, consistent with the existing `--agent-endpoint` standalone path, and acceptable because the feature spec's "agent commands require an azd project" constraint applies to `run | eval | optimize` (which need `azure.yaml` for service config), not to one-shot invocations where the user supplies both agent name and endpoint.
- **No call-site signature change required.** `resolveAgentEndpoint(ctx, accountName, projectName)` keeps its current signature. The new global-config and env-var levels are self-contained lookups added to the function body (reading `UserConfig` and `os.Getenv`), so callers do not need to pass additional parameters. `newAgentContext` is similarly unchanged. Existing tests in `show_test.go` continue to pass for the explicit-flag and partial-flag cases; new tests cover the additive levels.

Out of scope for this change (called out so they aren't surprised later):

- `service_target_agent.go` still reads `AZURE_AI_PROJECT_ENDPOINT` directly from the azd env at deploy time. Reconciling that with the broader cascade is a separate decision tied to the orchestrated (`azd up`) model.
- No flag deprecations. `--account-name` / `--project-name` remain supported on the agent commands that accept them today.

## 9. Migration / Back-Compat

- No config-schema migration required: the `extensions.ai-agents.context` subtree is created on first `project set`. Absence is the v0 state.
- The new resolution chain is additive — a user who never runs `project set` and has no `FOUNDRY_PROJECT_ENDPOINT` set behaves exactly as today.

## 10. Telemetry

One event per command, reusing the extension's existing telemetry surface:

- `azd.ai.project.set` — properties: `hasAzdProject` (bool), `endpointHostHash` (sha256 of host).
- `azd.ai.project.unset` — properties: `hadValue` (bool).
- `azd.ai.project.show` — properties: `source` (enum string), `resolved` (bool).

No PII; endpoints are hashed.

## 11. Security Considerations

- The endpoint URL is not a credential. No secret material is written to or read from this config path.
- File permissions on `~/.azd/config.json` are managed by azd core; no change.
- The validator rejects non-`https` schemes and non-Foundry hostnames, preventing accidental persistence of arbitrary URLs.

## 12. Open Questions

| #   | Question                                                                                                                                                                                                                                                                      | Owner   |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| 1   | Surface form: `azd ai agent project …` (v1, no new extension) vs. `azd ai project …` (requires either a top-level passthrough in azd core or a new `azure.ai.project` extension). Decision affects only command-tree registration; everything else in this spec is invariant. | TBD     |
| 2   | Should the inside-azd-project warning on `project set` be suppressible via a flag (e.g. `--quiet`) or always-on? Current proposal: always-on, auto-suppressed when `--output json` + `--no-prompt`.                                                                           | @huimiu |
| 3   | Should `project show` also accept `-p` as an override (useful for "what would I resolve to if I passed this flag?"), or is that semantically odd? Current proposal: yes, accept it — keeps the resolver pure and aids debugging via coding agents.                            | @huimiu |

## 13. Reference: Command Summary

```bash
azd ai [agent] project set <endpoint>     [--output table|json] [--no-prompt] [--debug]
azd ai [agent] project unset              [--output table|json] [--no-prompt] [--debug]
azd ai [agent] project show               [-p <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: `-p` flag → azd env (`AZURE_AI_PROJECT_ENDPOINT`) → `~/.azd/config.json` (`extensions.ai-agents.context.endpoint`) → `FOUNDRY_PROJECT_ENDPOINT` → structured error.
