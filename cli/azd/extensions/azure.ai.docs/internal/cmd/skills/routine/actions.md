---
short: Action types reference (agent-response, agent-invoke) with field matrices and worked manifest snippets.
order: 30
---
# Routine actions reference

When a routine's trigger fires, its **action** runs. The action is a discriminated-union record stored on the Routine's `action` field. The `type` field selects which sibling fields are valid for that action.

For the broader concept + lifecycle, see `overview`. For the trigger half, see `triggers`. For the CLI verbs that author / mutate actions, see `manage`.

## Action types at a glance

| Wire `type`                       | CLI alias (`--action`) | What it does                                                                                                              |
| --------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `invoke_agent_responses_api`      | `agent-response`        | Default. Invokes the agent's Responses API (conversational, returns a single response). Optional `conversation_id` carries history. |
| `invoke_agent_invocations_api`    | `agent-invoke`          | Invokes the agent's Invocations API (lower-level; typically used to kick off a longer task). Optional `session_id` ties runs together. |

The `agent-response` alias is the default when `--action` is omitted on `azd ai routine create`. Both wire `type` values pass through verbatim if you author the manifest manually via `--file`.

## Type immutability

Once a routine exists, you **cannot change its action TYPE** via `update`. The CLI rejects `--action` on `update` with a structured error pointing at delete-then-recreate. You CAN mutate the TYPE-specific fields (the target agent, the conversation / session id) via `update --agent-name` / `update --agent-endpoint-id` / `update --conversation-id` / `update --session-id`.

## Identifying the target agent

Both action types accept the same agent-targeting fields:

| Field                | Required (one-of)                    | Source                                       | Notes                                                                                                 |
| -------------------- | ------------------------------------ | -------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `agent_endpoint_id`  | one of these is required             | `--agent-endpoint-id` / manifest `agent_endpoint_id:` | Full ARM ID of the deployed agent endpoint. Resolves to a specific agent + version + endpoint.        |
| `agent_name`         | one of these is required             | `--agent-name` / manifest `agent_name:`      | Project-scoped agent name. Foundry resolves it to the default endpoint server-side.                   |

Prefer `agent_endpoint_id` when you want **version pinning** (the ARM ID embeds the endpoint, which embeds the version). Prefer `agent_name` when you want the routine to track the agent's default endpoint as it gets re-promoted.

Foundry validates the target agent exists on the project at `create` / `update` time. A typo or a deleted agent fails fast with a structured error.

## `agent-response` -- Responses API

Wire `type`: `invoke_agent_responses_api`. CLI alias: `agent-response` (the default).

Fields:

| Field                | Required | Source                                       | Notes                                                                                                            |
| -------------------- | -------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `type`               | Yes      | wire-only                                     | Value: `invoke_agent_responses_api`.                                                                              |
| `agent_endpoint_id` OR `agent_name` | Yes      | see above                                    | One of them is required.                                                                                          |
| `conversation_id`    | No       | `--conversation-id` / manifest `conversation_id:` | **Preview.** Threads each firing into the named conversation so the agent sees prior context.                  |

Worked CLI invocation:

```bash
azd ai routine create morning-brief \
  --trigger recurring \
  --cron "30 8 * * 1-5" \
  --time-zone America/New_York \
  --action agent-response \
  --agent-name news-summarizer \
  --conversation-id morning-thread
```

Worked manifest snippet:

```yaml
name: morning-brief
description: Weekday morning news brief threaded into a single conversation.
enabled: true
triggers:
  default:
    type: schedule
    cron_expression: "30 8 * * 1-5"
    time_zone: America/New_York
action:
  type: invoke_agent_responses_api
  agent_name: news-summarizer
  conversation_id: morning-thread
```

Omit `conversation_id` for an isolated invocation per firing (no shared context across firings). Each firing still records a `RoutineRun` either way; only the agent-side conversation state is affected.

## `agent-invoke` -- Invocations API

Wire `type`: `invoke_agent_invocations_api`. CLI alias: `agent-invoke`.

Fields:

| Field                | Required | Source                                       | Notes                                                                                                            |
| -------------------- | -------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `type`               | Yes      | wire-only                                     | Value: `invoke_agent_invocations_api`.                                                                            |
| `agent_endpoint_id` OR `agent_name` | Yes      | see above                                    | One of them is required.                                                                                          |
| `session_id`         | No       | `--session-id` / manifest `session_id:`      | Ties multiple firings into the same session record on the agent.                                                  |

Worked CLI invocation:

```bash
azd ai routine create kickoff \
  --trigger timer \
  --at 2026-06-01T03:00:00Z \
  --action agent-invoke \
  --agent-endpoint-id /subscriptions/.../agents/long-task-agent \
  --session-id launch-001
```

Worked manifest snippet:

```yaml
name: kickoff
description: One-shot kick-off invocation into the launch-001 session.
enabled: true
triggers:
  default:
    type: timer
    at: "2026-06-01T03:00:00Z"
action:
  type: invoke_agent_invocations_api
  agent_endpoint_id: "/subscriptions/.../agents/long-task-agent"
  session_id: launch-001
```

`agent-invoke` is the lower-level call; pick it when the agent runtime expects an invocation envelope rather than a conversational response. For most "fire a scheduled task" cases, start with `agent-response` (the default) -- it produces a response record visible in run history without extra setup.

## What input does the action receive?

When the trigger fires on its own, the action receives the **default input** wired into its type. For `invoke_agent_responses_api` that is the agent's default greeting / system prompt; for `invoke_agent_invocations_api` it is an empty invocation envelope. The trigger payload itself (cron expression, ISO 8601 firing time, GitHub issue body if `github_issue`) is NOT auto-mapped into the action input today -- if your agent needs the firing context, encode it in the routine description and reference it from the agent's system prompt.

When you manually `dispatch` a routine, you can override the action input via `--input <text>` -- see `dispatch` for the payload shape and the `RoutineDispatchPayload` wrapper.

## Identity the routine runs as

The Foundry service invokes the agent endpoint on the routine's behalf using a service-managed identity scoped to the project. The user who created the routine is not in the loop at firing time; revoking that user's access does not stop the routine. To stop a routine, `disable` or `delete` it.

## Where to go next

* "Which trigger types fire these actions?" -> `triggers`
* "How do I create / update / list routines via the CLI?" -> `manage`
* "How do I fire a routine right now to test the action setup?" -> `dispatch`
