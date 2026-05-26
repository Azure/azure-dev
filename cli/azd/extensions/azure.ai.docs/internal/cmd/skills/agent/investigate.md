---
short: Inspect agent state, sessions, evals, and optimizations.
order: 45
---
# Investigate: inspect agent state, sessions, evals, and optimizations

Audience: an AI coding assistant tracing a deployed agent for the user. Every command in this topic is read-only -- safe to run anywhere, never mutates state, never requires `--force`.

---

## Start here

Always call these two first when investigating any agent issue:

```bash
azd ai project show --output json
azd ai agent show --output json
```

If `show` returns `status: "not_deployed"`, the agent isn't there yet -- switch to the `initialize` topic. Otherwise the rest of this topic is fair game.

---

## Agent record

`show` returns the full agent version object. Key fields:

- `name` -- Foundry agent name (azure.yaml service name plus suffix).
- `version` -- current deployed version (versions are immutable).
- `status` -- one of `active`, `idle`, `creating`, `failed`, etc.
- `agent_endpoints` -- map of protocol label -> URL.
- `playground_url` -- portal link the user can open in a browser.
- `next_step` -- present only when `status` is not active/idle; carries the recommended remediation command.

---

## Sessions

Every invocation runs in a session. List, inspect, and trace them:

```bash
# List all sessions for the active agent
azd ai agent sessions list --output json

# Inspect one
azd ai agent sessions show <session-id> --output json
```

Per-session log stream (NOT JSON; raw structured-event SSE):

```bash
# Tail the most recent session
azd ai agent monitor --session-id <id> --tail 50

# Follow in real time
azd ai agent monitor --session-id <id> --follow

# System events instead of stdout/stderr
azd ai agent monitor --session-id <id> --type system
```

`monitor` is the only investigate command that does not emit JSON -- it's a stream surface, not a query surface. Use `--raw` to skip the formatter and consume the raw SSE.

---

## Files in a session

```bash
azd ai agent files list --output json
azd ai agent files show <remote-path> --output json
azd ai agent files stat <remote-path> --output json

# Download to a local path (defaults to the remote basename in cwd)
azd ai agent files download <remote-path>
azd ai agent files download <remote-path> --target-path ./local.csv
```

`files list` and `files show` return JSON listings. `files stat` returns a single-file metadata record (size, mtime, content type). `files download` writes the file to disk -- read-only over the agent state.

Upload, mkdir, and delete are mutations -- see the `operate` topic.

---

## Evals

The eval subgroup tracks eval definitions and historical runs:

```bash
# All evals known to the current project
azd ai agent eval list --output json

# Detail for one eval and its recent runs
azd ai agent eval show <eval-id> --output json

# Detail for a specific run
azd ai agent eval show <eval-id> --eval-run-id <run-id> --output json
```

`eval list` JSON shape:

```json
{
  "items": [
    {
      "id": "eval-id",
      "name": "smoke-core",
      "active": true,
      "runCount": 4,
      "lastRunStatus": "completed",
      "createdBy": "alice@example.com",
      "createdAt": 1737045821
    }
  ]
}
```

`eval show` for a specific run returns the full OpenAIEvalRun object under `.run` plus the eval id under `.eval`.

---

## Optimization jobs

The optimize subgroup tracks optimization runs:

```bash
# All recent optimization jobs
azd ai agent optimize list --output json

# Detail for one job
azd ai agent optimize status <operation-id> --output json

# Watch until the job reaches a terminal status (single JSON object emitted
# at the end -- no spinner contamination)
azd ai agent optimize status <operation-id> --watch --output json
```

`optimize list` JSON shape:

```json
{
  "items": [
    {
      "id": "opt_abc123",
      "status": "completed",
      "agent": "echo",
      "score": 0.87,
      "createdAt": "2026-05-22T20:14:31Z"
    }
  ],
  "statusFilter": "completed"
}
```

`statusFilter` echoes any `--status` filter you passed so the caller knows the result set is constrained.

---

## Connections

```bash
azd ai agent connection list --output json
azd ai agent connection show <name> --output json
```

Connection write commands (create / update / delete) live in a separate package; see the `configure` topic.

---

## Health check

When something is off but you can't pinpoint the cause:

```bash
azd ai agent doctor --output json
```

`doctor` runs a sequence of local + remote checks and returns a machine-readable `Report` with per-check status (`pass`, `warn`, `fail`, `skip`, `info`), suggestions, and links. Use `--local-only` to skip the network-dependent checks.

Exit codes:

- `0` -- at least one check passed and no checks failed.
- `1` -- any check failed.
- `2` -- all checks were skipped (e.g. no project detected).

---

## Common error codes you'll see while investigating

- `session_not_found` -- session has already been deleted or never existed. Re-list with `sessions list`.
- `file_not_found` -- the remote path doesn't exist. Use `files list`.
- `agent_definition_not_found` -- the deployed agent name doesn't match azure.yaml. Re-deploy from the workspace root.
- `eval_config_invalid` -- the local `eval.yaml` failed validation. See `azd ai agent doctor` for the specific cause.

---

## What this topic does NOT cover

- Mutating sessions or files -- see `operate`.
- Submitting eval / optimize jobs -- see `operate`.
- Configuration of the agent definition -- see `configure`.
