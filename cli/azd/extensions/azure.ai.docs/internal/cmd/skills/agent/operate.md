---
short: Run write commands, billed jobs, and destructive operations safely.
order: 40
---
# Operate: write commands, billed jobs, destructive ops

Audience: an AI coding assistant about to mutate a Foundry agent on behalf of a developer. Every command in this topic is gated by the agent-friendly confirmation envelope -- read this whole topic before running any of them.

---

## The confirmation envelope -- universal contract

EVERY write command in this topic accepts three flags:

- `--dry-run` -- emit the JSON envelope, exit 0, mutate nothing.
- `--force` -- skip the prompt/envelope, proceed immediately.
- (neither) -- non-interactive callers get the envelope + exit 2; interactive callers get a y/n prompt on stderr.

When the command exits 2 with the envelope below on stdout, the agent MUST:

1. Present `description` and `changes` to the human.
2. NEVER auto-append `--force` -- the explicit human consent IS the re-invocation.
3. Run `confirmCommand` exactly as printed once approved.

```json
{
  "status": "confirmation_required",
  "command": "agent files delete",
  "description": "Delete report.csv from agent \"my-agent\".",
  "classification": {
    "readOnly": false,
    "destructive": true,
    "idempotent": false
  },
  "changes": [
    "Will delete report.csv from session sess-1 on agent my-agent"
  ],
  "confirmCommand": "azd ai agent files delete report.csv --session-id sess-1 --force"
}
```

`classification.destructive: true` means the operation cannot be undone. Be especially careful presenting these to the human.

---

## Invoke (billed remote calls)

```bash
# Preview the invocation envelope
azd ai agent invoke "Hello!" --dry-run

# Invoke after confirmation
azd ai agent invoke "Hello!" --force

# Local invocation -- NOT gated (no billing). See `develop` for the full local-dev flow.
azd ai agent invoke "Hello!" --local
```

Invoking remote agents is a billed API call, so the envelope appears even though it is technically idempotent. `--local` invocations skip the gate.

Useful flags (full surface):

- `--agent-endpoint <url>` -- explicit deployed agent endpoint (overrides project / env detection). Use the URL from `azd ai agent show --output json`. Useful for invoking from a directory that has no azd project.
- `-p, --protocol responses|invocations` -- wire format. Defaults to `responses`.
- `-f, --input-file <path>` -- send the file contents as the request body instead of a positional message. Pairs with `--protocol invocations` for structured payloads.
- `-s, --session-id <id>` -- explicit session id override. Default: reuses the last invoke session for this agent.
- `--conversation-id <id>` -- explicit conversation id override.
- `--new-session` -- force a fresh session (discard the saved one).
- `--new-conversation` -- force a fresh conversation.
- `--version <n>` -- invoke a specific deployed agent version (creates / reuses a session backed by that version). REJECTED with `--local`.
- `-t, --timeout <seconds>` -- per-request timeout (0 = no timeout).
- `--user-isolation-key <value>` / `--chat-isolation-key <value>` -- required for agents configured with header-based isolation.
- `--port <n>` -- used with `--local` when `azd ai agent run` is on a non-default port.

Sessions are persisted per-agent: consecutive invokes reuse the same session automatically. Pass `--new-session` to reset. Named-agent invocation against `--local` is REJECTED -- the local server runs one agent at a time.

---

## Files (mutations)

```bash
# Upload (non-destructive create -- NOT yet gated by the envelope)
azd ai agent files upload ./data/input.csv
azd ai agent files upload ./input.csv --target-path /data/input.csv

# Create a directory (non-destructive create -- NOT gated)
azd ai agent files mkdir /data/output

# Delete (destructive -- gated)
azd ai agent files delete /data/old-file.csv --dry-run
azd ai agent files delete /data/old-file.csv --force

# Delete a directory tree
azd ai agent files delete /data/temp --recursive --force
```

Use `azd ai agent files list --output json` (from the `investigate` topic) to verify before deleting. `files download` and `files stat` are read-only and also live in `investigate`.

---

## Sessions

```bash
# Delete a session and its persistent filesystem
azd ai agent sessions delete <session-id> --dry-run
azd ai agent sessions delete <session-id> --force

# Create / update -- not yet gated (non-destructive create)
azd ai agent sessions create <agent-name>
azd ai agent sessions update <session-id> --metadata key=value
```

---

## Eval runs (billed)

The full eval lifecycle (init -> run -> show -> update -> re-run) lives in the `evaluate` topic. This section covers the WRITE side that gates each step.

```bash
# Generate eval suite (billed dataset + evaluator generation jobs)
azd ai agent eval init --dry-run
azd ai agent eval init --force

# Execute an eval run from the local eval.yaml (billed)
azd ai agent eval run --dry-run
azd ai agent eval run --force

# Upload new versions of locally-edited evaluators / datasets
azd ai agent eval update --dry-run
azd ai agent eval update --force
```

All three are billed. The `init` envelope mentions both the dataset generation and evaluator generation jobs. The `run` envelope is short because there's only one billed action. The `update` envelope lists which evaluators or datasets have local changes.

---

## Optimization

The optimize subgroup has the heaviest write surface in the extension. Every one of these is gated:

```bash
# Submit an optimization job (billed; can take minutes to hours)
azd ai agent optimize --dry-run
azd ai agent optimize --force
azd ai agent optimize --agent <name> --target instruction --force

# Cancel a running optimization job (destructive -- partial work is lost)
azd ai agent optimize cancel <op-id> --dry-run
azd ai agent optimize cancel <op-id> --force

# Apply a winning candidate's config to the local azd project
azd ai agent optimize apply --candidate <id> --dry-run
azd ai agent optimize apply --candidate <id> --force

# Deploy a winning candidate as a new agent version (skips localization)
azd ai agent optimize deploy --candidate <id> --agent <name> --dry-run
azd ai agent optimize deploy --candidate <id> --agent <name> --force
```

`apply` writes files into the user's project under `<service-dir>/.agent_configs/<candidate-id>/`. After `apply`, run `azd deploy` to deploy the optimized agent version. `deploy` skips the local file write and creates the new version directly via the Foundry API.

---

## Endpoint update (idempotent patch)

```bash
azd ai agent endpoint update --dry-run
azd ai agent endpoint update --force
```

Patches the `agent_endpoint` and `agent_card` fields from `agent.yaml` in place without creating a new agent version. Idempotent: re-running with the same `agent.yaml` is a no-op.

---

## What this topic does NOT cover

- `azd init`, `azd provision`, `azd deploy` -- those are core azd commands, not agent-extension write paths. See the `initialize` topic for how they fit into the bootstrap flow.
- Connection write commands (`connection add / update / delete`) -- those live in a separate package and do NOT yet emit envelopes. Surface their existing behavior to the human directly until the envelope is wired in.
- `init` (agent extension) -- non-destructive create, not gated.

---

## Recovery: what to do when a write fails

1. Read the structured error: stderr JSON with `code`, `message`, `suggestion`, `category` fields.
2. If `category: "validation"` -- the input was wrong; fix the command-line flag the message names and retry.
3. If `category: "dependency"` -- something the command needs is missing (auth, endpoint, file). Run `azd ai agent doctor` to pinpoint, fix, retry.
4. If `category: "service"` -- the Azure / Foundry API returned an error. The error.service.name and error code identify the service; treat as a transient retry candidate unless the code is a 4xx-equivalent.
5. If `category: "internal"` -- there's a bug. Surface verbatim to the human and ask them to file an issue.

Recovery from a destructive mistake (e.g. wrong session deleted) is NOT possible at the CLI level -- the data is gone. Use `--dry-run` ruthlessly when in doubt.
