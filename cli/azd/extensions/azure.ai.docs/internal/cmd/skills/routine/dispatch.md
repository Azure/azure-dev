---
short: Manual trigger via dispatch, plus run-history inspection and how to debug a failed run.
order: 50
---
# Routine dispatch + run history

`dispatch` fires a routine **right now**, regardless of its schedule. `run list` shows what happened on each firing (auto OR manual). This is the operate-and-investigate topic for routines: testing trigger setup, replaying with custom input, hunting down a failed run.

For trigger + action mental model, see `overview`. For the CRUD CLI, see `manage`.

## Manual dispatch

```bash
azd ai routine dispatch weekday-standup
```

Fires the routine. The action runs server-side using the routine's stored configuration. The CLI prints the new dispatch envelope:

```text
Routine 'weekday-standup' dispatched.
Dispatch ID: dsp_01HFK4...
Action Correlation ID: cor_01HFK4...
Task ID: tsk_01HFK4...
```

JSON output:

```bash
azd ai routine dispatch weekday-standup --output json
```

```json
{
  "dispatch_id": "dsp_01HFK4...",
  "action_correlation_id": "cor_01HFK4...",
  "task_id": "tsk_01HFK4..."
}
```

The dispatch IDs are how you correlate the firing with the run record:

| Field                   | Meaning                                                                                       |
| ----------------------- | --------------------------------------------------------------------------------------------- |
| `dispatch_id`           | Foundry's ID for THIS dispatch attempt. Appears on the matching `RoutineRun` row.              |
| `action_correlation_id` | Foundry's ID for the downstream agent invocation. Use it to correlate with agent-side logs.   |
| `task_id`               | Foundry's task tracking ID. Optional; used by long-running background actions.                |

`dispatch` works on **enabled AND disabled routines**. Disabling a routine stops AUTO-firing; manual `dispatch` is unaffected. Pair `disable` + `dispatch` to run a routine on demand without it also auto-firing on its schedule.

### `--input` -- override the action payload

By default, `dispatch` fires the routine with the action's **default payload** (no user input). To pass a one-off user-message-style payload, use `--input`:

```bash
azd ai routine dispatch weekday-standup --input "Skip the news section today and just do calendar."
```

The CLI wraps the input string into the action's discriminated payload shape:

```json
{
  "payload": {
    "type": "invoke_agent_responses_api",
    "input": "Skip the news section today and just do calendar."
  }
}
```

The `type` field comes from the routine's own action type -- the CLI fetches the routine first to read it. If the routine has no action configured at all (rare; only via direct REST authoring), the dispatch fails with a structured error.

For routines with action type `invoke_agent_invocations_api`, the same `--input` value lands as the invocation's input field.

### `--async` -- suppress descriptive output

`--async` collapses the dispatch output to just the dispatch ID. Useful for scripting:

```bash
DISPATCH_ID=$(azd ai routine dispatch weekday-standup --async)
echo "Fired: $DISPATCH_ID"
```

`--async` is purely cosmetic -- the actual dispatch is always asynchronous (the service runs the routine in the background regardless). To get a richer envelope for scripting, use `--output json` instead.

## Run history (`run list`)

```bash
azd ai routine run list weekday-standup
```

Returns every firing's record (auto + manual), most recent first. The table view shows ID / STATUS / PHASE / STARTED / ENDED. The JSON view returns the full envelope:

```json
{
  "value": [
    {
      "id": "run_01HFK4...",
      "status": "succeeded",
      "phase": "completed",
      "trigger_type": "schedule",
      "attempt_source": "scheduler",
      "action_type": "invoke_agent_responses_api",
      "triggered_at": "2026-05-26T13:00:00Z",
      "started_at": "2026-05-26T13:00:01Z",
      "ended_at": "2026-05-26T13:00:05Z",
      "dispatch_id": "dsp_...",
      "action_correlation_id": "cor_...",
      "response_id": "resp_..."
    },
    {
      "id": "run_01HFK3...",
      "status": "failed",
      "phase": "action",
      "trigger_type": "schedule",
      "attempt_source": "scheduler",
      "action_type": "invoke_agent_responses_api",
      "triggered_at": "2026-05-25T13:00:00Z",
      "started_at": "2026-05-25T13:00:01Z",
      "ended_at": "2026-05-25T13:00:03Z",
      "dispatch_id": "dsp_...",
      "action_correlation_id": "cor_...",
      "error_type": "AgentInvocationFailed",
      "error_message": "agent 'standup-agent' returned 503 Service Unavailable"
    }
  ],
  "next_page_token": ""
}
```

Key fields:

| Field                   | Meaning                                                                                               |
| ----------------------- | ----------------------------------------------------------------------------------------------------- |
| `id`                    | The run record's unique ID.                                                                            |
| `status`                | `succeeded`, `failed`, `running`, `cancelled`.                                                         |
| `phase`                 | Which lifecycle phase the run is in or ended in (`scheduled`, `dispatching`, `action`, `completed`).   |
| `trigger_type`          | The trigger type that fired it (`schedule`, `timer`, `github_issue`).                                  |
| `attempt_source`        | `scheduler` (auto-firing) or `dispatch` (manual via `azd ai routine dispatch`).                        |
| `action_type`           | The wire action type that ran.                                                                          |
| `triggered_at`          | When Foundry decided the routine should fire (cron resolved, timer hit, event arrived).                |
| `started_at` / `ended_at` | When the action actually started and finished.                                                       |
| `dispatch_id`           | Matches the dispatch envelope from `dispatch`.                                                          |
| `action_correlation_id` | Correlation ID for the downstream agent invocation -- use it to find agent-side logs / responses.      |
| `response_id`           | Present for `agent-response` actions; the ID of the agent's response record.                           |
| `task_id`               | Present for `agent-invoke` actions that produce a tracked task.                                         |
| `error_type` / `error_message` | Present on `failed` runs. Both are server-supplied; treat as the canonical failure description.   |

### Filters and pagination

```bash
# Cap the total returned.
azd ai routine run list weekday-standup --top 10

# Server-side OData filter (Foundry-supported subset only).
azd ai routine run list weekday-standup --filter "status eq 'failed'" --output json
```

The CLI auto-paginates via Foundry's page tokens and stops when it has gathered `--top` runs (or hits the end). Default is no cap; on a busy routine, set `--top` to keep the response bounded.

## Debugging recipes

### Recipe: "I just changed the cron expression; did it work?"

```bash
# 1) Verify the routine has the new cron expression.
azd ai routine show weekday-standup --output json | jq '.triggers.default.cron_expression'

# 2) Fire manually to confirm the action target still works.
DISPATCH_ID=$(azd ai routine dispatch weekday-standup --output json | jq -r .dispatch_id)
echo "Test dispatch: $DISPATCH_ID"

# 3) Wait a few seconds, then inspect the run record.
sleep 5
azd ai routine run list weekday-standup --top 1 --output json | jq
```

If the dispatch run record shows `status: succeeded`, the routine is healthy -- the cron expression will pick up at the next firing. If the dispatch run records show `status: failed`, the action target is broken; the cron expression change is irrelevant until you fix the target.

### Recipe: "A scheduled run failed -- what happened?"

```bash
# 1) Find the most recent failed run.
azd ai routine run list my-routine --filter "status eq 'failed'" --top 1 --output json

# 2) Read the error envelope.
# error_type / error_message describe the server-side failure.

# 3) Correlate with the agent-side response or task using
#    action_correlation_id (and, for agent-response, response_id).
azd ai agent invoke list --filter "correlation_id eq 'cor_...'" --output json   # if your CLI surfaces this
```

Common `error_type` values:

| Value                      | Likely cause                                                                                  |
| -------------------------- | --------------------------------------------------------------------------------------------- |
| `AgentInvocationFailed`    | The agent endpoint returned an HTTP error (5xx, 4xx). Check the agent's recent logs.           |
| `AgentNotFound`            | The target agent was deleted or repointed. Update the routine's `agent-name` / `agent-endpoint-id`. |
| `RoutineDisabled`          | The routine was disabled between trigger fire and action run. Re-enable if you want it to fire. |
| `InvalidPayload`           | A `dispatch --input` payload was wrong shape for the action TYPE. Check the routine's action.   |
| `RateLimited`              | The agent endpoint or downstream model hit a rate limit. Retry or scale the agent.              |

### Recipe: "The routine isn't firing on its schedule"

```bash
# 1) Confirm the routine is enabled.
azd ai routine show my-routine --output json | jq '.enabled'   # must be true

# 2) Confirm the cron expression matches a real future minute.
azd ai routine show my-routine --output json | jq '.triggers.default.cron_expression, .triggers.default.time_zone'

# 3) Confirm prior firings happened (it's not a brand-new routine that has never fired).
azd ai routine run list my-routine --top 5

# 4) Manually dispatch to confirm the action target itself works.
azd ai routine dispatch my-routine
```

If `dispatch` succeeds but auto-firing produces no run records, the routine is enabled, the action target is fine, but the SCHEDULER is not picking it up -- either the cron expression resolves to a minute Foundry skipped, or the routine landed AFTER the most recent firing minute. Wait until the next firing window and re-check `run list`.

## Where to go next

* "What CRUD verbs are there for the routine itself?" -> `manage`
* "Which trigger types fire routines and how do I configure each?" -> `triggers`
* "Which action types do routines invoke and how do I configure each?" -> `actions`
