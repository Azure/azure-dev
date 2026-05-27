---
short: Imperative CLI reference for create, update, show, list, delete, enable, disable, plus the manifest file format.
order: 40
---
# Routine management (imperative CLI)

`azd ai routine <verb>` commands target the Foundry project directly. They do **not** touch `azure.yaml` and `azd deploy` does **not** create or update routines on Foundry. Drive the lifecycle explicitly with the commands below.

For the trigger reference, see `triggers`. For the action reference, see `actions`. For manual triggering + run history, see `dispatch`.

## Quick reference

| Verb       | Purpose                                                                              |
| ---------- | ------------------------------------------------------------------------------------ |
| `create`   | Land a new routine (flag-driven or `--file <manifest>`). Default `enabled: true`.    |
| `update`   | Mutate fields on an existing routine. Trigger / action TYPE are immutable.            |
| `show`     | Print the routine envelope.                                                          |
| `list`     | Print all routines on the project.                                                   |
| `delete`   | Remove a routine. Irreversible. Confirmation prompt unless `--force`.                |
| `enable`   | Re-enable a disabled routine. Idempotent.                                            |
| `disable`  | Pause an enabled routine (no auto-firing). `dispatch` still works. Idempotent.        |

All verbs accept `-p` / `--project-endpoint`, `--output table|json`, `--no-prompt`, and `--debug`.

## Create

Two mutually exclusive modes:

```bash
# Flag-driven: build the routine from individual flags.
azd ai routine create weekday-standup \
  --trigger recurring \
  --cron "0 9 * * 1-5" \
  --time-zone America/New_York \
  --action agent-response \
  --agent-endpoint-id /subscriptions/.../agents/standup-agent \
  --description "Daily 09:00 ET standup brief."

# Manifest-driven: author the full Routine shape in a file.
azd ai routine create weekday-standup --file ./routine.yaml
```

`--file` and `--trigger` are **mutually exclusive**. Passing both is rejected with a structured error (`CodeConflictingArguments`).

Flags by purpose:

| Flag                     | Required           | Purpose                                                                                  |
| ------------------------ | ------------------ | ---------------------------------------------------------------------------------------- |
| `--trigger`              | flag mode only     | `timer` or `recurring`. (`github-issue` not surfaced; use `--file`.) See `triggers`.     |
| `--at`                   | timer trigger      | ISO 8601 datetime for the one-shot firing.                                               |
| `--cron`                 | recurring trigger  | 5-field cron expression. Min 5-minute interval enforced by Foundry.                       |
| `--time-zone`            | optional            | IANA name. Default `UTC`.                                                                |
| `--action`               | optional            | `agent-response` (default) or `agent-invoke`. See `actions`.                              |
| `--agent-name`           | one of these       | Project-scoped agent name. Foundry resolves to default endpoint.                          |
| `--agent-endpoint-id`    | one of these       | Full ARM ID. Version-pinned.                                                              |
| `--conversation-id`      | optional (preview) | Threads `agent-response` firings into a single conversation.                              |
| `--session-id`           | optional            | Threads `agent-invoke` firings into a single session.                                     |
| `--description`          | optional            | Human description (also accepted via manifest).                                           |
| `--enabled`              | optional            | Default `true`. Pass `--enabled=false` to land disabled.                                  |
| `--force`                | optional            | Upsert: overwrite an existing routine with the same name.                                 |
| `--file`                 | manifest mode      | Path to a YAML or JSON Routine manifest.                                                  |

### Manifest file format

The manifest matches the wire shape of the `Routine` resource. YAML, YML, or JSON; the file extension picks the parser.

```yaml
# routine.yaml
name: weekday-standup                         # optional; positional <name> wins
description: Daily 09:00 ET standup brief.
enabled: true
triggers:
  default:                                    # CLI authors a single trigger under "default"
    type: schedule                            # NOT "recurring" -- that is the CLI alias
    cron_expression: "0 9 * * 1-5"
    time_zone: America/New_York
action:
  type: invoke_agent_responses_api            # NOT "agent-response" -- that is the CLI alias
  agent_endpoint_id: /subscriptions/.../agents/standup-agent
```

JSON form takes the same field names. Unknown fields are NOT rejected today (Foundry ignores them); keep manifests clean by sticking to the documented shape.

Mode rules for `--file`:

* The positional `<name>` argument **always wins** over any `name:` in the file.
* Top-level fields (`description`, `enabled`, `triggers`, `action`) come from the file unless overridden by an explicit flag (flag wins).
* Trigger and action TYPE values use the **wire names** (`schedule`, `invoke_agent_responses_api`), not the CLI aliases.

### `--force` (upsert)

By default, creating a routine that already exists fails with a `409 Conflict`. `--force` deletes the existing routine first (no confirmation prompt) and then re-creates. Use it deliberately:

```bash
azd ai routine create weekday-standup --file ./routine.yaml --force
```

`--force` does NOT preserve run history -- the new routine starts with an empty `run list`. If preserving history matters, use `update` instead.

## Update

```bash
# Tweak one field. All other fields preserved verbatim.
azd ai routine update weekday-standup --description "9 AM ET standup."

# Adjust the cron schedule. (Trigger TYPE is unchanged.)
azd ai routine update weekday-standup --cron "0 10 * * 1-5"

# Repoint the action target.
azd ai routine update weekday-standup --agent-endpoint-id /subscriptions/.../agents/v2-agent

# Manifest-driven update: file fields overwrite existing fields.
azd ai routine update weekday-standup --file ./routine.yaml
```

`update` is a **GET-then-PUT** under the hood: the CLI fetches the current routine, overlays the supplied flag / manifest fields, and writes it back. Fields you don't mention are preserved.

### Type immutability

`--trigger <type>` and `--action <type>` on `update` are **rejected** with a structured error (`CodeConflictingArguments`). To change a trigger TYPE from `timer` to `recurring` (or an action TYPE from `agent-response` to `agent-invoke`), delete and recreate.

You CAN mutate the type-specific fields without changing the type itself: `--at` for an existing `timer` trigger, `--cron` for an existing `recurring` trigger, `--time-zone` on either, etc. The CLI checks the existing trigger TYPE before applying a mutation and refuses incompatible combinations (e.g. `--cron` on a `timer` trigger).

## Show

```bash
azd ai routine show weekday-standup --output json
```

Returns the full Routine envelope:

```json
{
  "name": "weekday-standup",
  "description": "Daily 09:00 ET standup brief.",
  "enabled": true,
  "triggers": {
    "default": {
      "type": "schedule",
      "cron_expression": "0 9 * * 1-5",
      "time_zone": "America/New_York"
    }
  },
  "action": {
    "type": "invoke_agent_responses_api",
    "agent_endpoint_id": "/subscriptions/.../agents/standup-agent"
  },
  "created_at": "2026-05-20T18:42:00Z",
  "updated_at": "2026-05-26T10:14:00Z"
}
```

Run history is NOT included on the show envelope -- pull it with `run list` (see `dispatch`).

## List

```bash
azd ai routine list --output json
```

Returns `{ value: [routine, ...], continuation_token: "" }`. The continuation token is a placeholder today -- the CLI does not surface server-side pagination. For large projects, use `--filter` on `run list` (see `dispatch`) to scope queries; routine list itself returns the full project set in one call.

The table view (default) shows `NAME / ENABLED / TRIGGER / ACTION` (one summary line per routine).

## Delete

```bash
azd ai routine delete weekday-standup
# -> Interactive confirmation prompt: "Delete routine 'weekday-standup'? [y/N]"

azd ai routine delete weekday-standup --force
# -> Skips the prompt.
```

`--no-prompt` mode **requires** `--force`: running `azd ai routine delete <name> --no-prompt` without `--force` is rejected with a structured error so a non-interactive script can't surprise-delete a routine.

Delete is **irreversible**. Run history is removed along with the routine.

## Enable / disable

```bash
azd ai routine enable weekday-standup
azd ai routine disable weekday-standup
```

Both are **idempotent**: enabling an already-enabled (or disabling an already-disabled) routine succeeds and reports the no-op. `disable` pauses auto-firing only; manual `dispatch` still works on disabled routines (the action still runs).

## Output formats

Every verb accepts `--output table|json`. JSON is the recommended form when piping to a coding agent. The table form is a hand-readable summary -- not all fields are visible (e.g. `triggers.default.cron_expression` is collapsed to its TYPE on `list`).

## Authentication and project endpoint

All requests use `Bearer` tokens from `DefaultAzureCredential` with scope `https://ai.azure.com/.default`. The CLI handles token acquisition transparently; you only need to be signed in via `azd auth login`.

The project endpoint comes from `-p` / `--project-endpoint`, then `AZURE_AI_PROJECT_ENDPOINT`, then global config, then `FOUNDRY_PROJECT_ENDPOINT`. See `overview` for the full cascade.

## Debug logging

`--debug` writes diagnostic output to stderr. The HTTP client opts OUT of request/response body logging until a sanitizer is in place; the log shows headers and status codes but never the JSON payload.
