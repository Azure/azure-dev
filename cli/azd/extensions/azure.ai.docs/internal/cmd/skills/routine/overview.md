---
short: What a Foundry routine is (trigger + action), the lifecycle, and the azd ai routine CLI surface.
order: 10
---
# Routine overview

A **Foundry routine** pairs a **trigger** (when to fire) with an **action** (what to do) and stores the pair on a Foundry project. Foundry fires the routine on its own whenever the trigger matches; you can also fire it manually with `azd ai routine dispatch`. Each firing records a `RoutineRun` row visible via `azd ai routine run list`.

Use routines when an agent needs to do work **on a schedule** (recurring cron), **at a specific time** (one-shot timer), or **in response to an external event** (e.g. a GitHub issue opening; deferred from the CLI surface today) -- as opposed to the on-demand `azd ai agent invoke` path where a user kicks off each invocation.

Today, routines are managed through the `azd ai routine` CLI (from the `azure.ai.routines` extension). `azd deploy` does NOT create or update routines on Foundry. You install the extension once, then drive the lifecycle explicitly.

For trigger-side reference, see `triggers`. For action-side reference, see `actions`. For step-by-step CLI usage, see `manage`. For manual triggering + run history + debugging, see `dispatch`.

## Install the extension

```bash
azd extension install azure.ai.routines
```

Then `azd ai routine --help` to see the verbs.

## The CLI surface

| Command                                                                                       | What it does                                                                                                       |
| --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `azd ai routine create <name> --trigger ... --action ... [trigger-specific + action-specific flags]` | Create a routine from individual flags.                                                                            |
| `azd ai routine create <name> --file ./routine.yaml`                                          | Create a routine from a YAML or JSON manifest file. Mutually exclusive with `--trigger`.                            |
| `azd ai routine create <name> --file ... --force`                                             | Upsert: overwrite an existing routine with the same name.                                                          |
| `azd ai routine update <name> [field flags ...]`                                              | Update fields on an existing routine. Trigger TYPE and action TYPE are immutable -- delete + recreate to change.    |
| `azd ai routine show <name>`                                                                  | Show the routine envelope (name, description, enabled, triggers, action, timestamps).                              |
| `azd ai routine list`                                                                         | List all routines in the Foundry project.                                                                          |
| `azd ai routine delete <name> [--force]`                                                      | Delete a routine. `--force` skips the confirmation prompt; required in `--no-prompt` mode.                          |
| `azd ai routine enable <name>`                                                                | Enable a disabled routine. Idempotent.                                                                              |
| `azd ai routine disable <name>`                                                               | Disable an enabled routine (pause auto-firing without deleting it). Idempotent.                                     |
| `azd ai routine dispatch <name> [--input <text>] [--async]`                                   | Manually fire the routine right now. Records a `RoutineRun` like an auto-firing.                                    |
| `azd ai routine run list <name> [--top N] [--filter <odata>]`                                 | List execution history for a routine. Auto-paginates via page tokens.                                               |

All commands accept the standard cross-cutting flags: `-p` / `--project-endpoint`, `--output table|json`, `--no-prompt`, and `--debug`.

## Lifecycle

```
   create ----------> enabled (default) -----> [trigger fires] -> RoutineRun
                          |                          OR
                          v                    [dispatch fires] -> RoutineRun
                       disabled
                          |
                          v
                       delete (irrecoverable)
```

* `create` lands the routine in the `enabled: true` state by default. Pass `--enabled=false` (or set `enabled: false` in a manifest) to land disabled.
* `enable` / `disable` flip the state. Disabled routines do NOT auto-fire; they still accept manual `dispatch` calls but the action runs as usual.
* `dispatch` is for testing or for one-off invocations outside the trigger schedule -- it produces a `RoutineRun` row indistinguishable from an auto-firing except for the `attempt_source` field.
* `delete` is irreversible. There is no soft-delete; restore-from-history means re-running `create`.

## The trigger + action model (a quick tour)

Every routine has exactly one trigger (today; the wire shape uses a `triggers` map keyed by `default`, but the CLI surfaces only one trigger per routine) and exactly one action.

* **Trigger TYPE** is one of `timer` (one-shot), `recurring` (cron-scheduled), or `github_issue` (deferred from the CLI today; can be authored via `--file`). Reference: `triggers`.
* **Action TYPE** is one of `agent-response` (`invoke_agent_responses_api`) or `agent-invoke` (`invoke_agent_invocations_api`). Reference: `actions`.

The `type` field on each is a **discriminated union key**: it selects which sibling fields are valid. The trigger + action TYPES are immutable once a routine exists -- to change them, delete and recreate. Other fields (description, time zone, cron expression, target agent, etc.) ARE mutable on `update`.

## Project endpoint resolution

The Foundry project endpoint is resolved in the same cascade used by every other `azd ai` extension:

1. `-p` / `--project-endpoint` flag on the command.
2. Active azd env value `AZURE_AI_PROJECT_ENDPOINT`.
3. Global config `extensions.ai-routines.project.context.endpoint` (falls back to the sibling `azure.ai.projects` / `azure.ai.agents` global config so users who already configured the endpoint via another extension are not forced to re-run `set`).
4. Host environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error with an actionable suggestion.

## Permissions

Foundry requires the **Foundry User** role on the project. The role was previously named "Azure AI User"; the rename is rolling out but the IDs and permissions are unchanged. Routine CRUD requires this role; the routine runtime uses the routine's configured action target identity for the agent invocation itself.

## Where to go next

* "Which trigger types exist and what fields does each take?" -> `triggers`
* "Which action types exist and what fields does each take?" -> `actions`
* "How do I create / update / delete / list a routine?" -> `manage`
* "How do I fire a routine manually and inspect what happened?" -> `dispatch`
