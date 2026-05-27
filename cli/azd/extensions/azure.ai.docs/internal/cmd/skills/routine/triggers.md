---
short: Trigger types reference (timer, recurring, github_issue) with field matrices and worked manifest snippets.
order: 20
---
# Routine triggers reference

A routine fires when its **trigger** matches. The trigger is a discriminated-union record stored under the `triggers` map (keyed by `default`) on the Routine resource. The `type` field selects which sibling fields are valid for that trigger.

For the broader concept + lifecycle, see `overview`. For the action half, see `actions`. For the CLI verbs that author / mutate triggers, see `manage`.

## Trigger types at a glance

| Wire `type`     | CLI alias (`--trigger`) | CLI surface | When                                                       |
| --------------- | ----------------------- | ----------- | ---------------------------------------------------------- |
| `timer`         | `timer`                 | Yes         | One-shot. Fires once at an explicit ISO 8601 datetime.     |
| `schedule`      | `recurring`             | Yes         | Recurring. Fires on a 5-field cron expression.             |
| `github_issue`  | (deferred)              | Only via `--file` manifest | External event. Fires when a GitHub issue opens. |

The CLI's `--trigger timer` and `--trigger recurring` map to wire `type: timer` and `type: schedule` respectively (the wire field is `schedule`, not `recurring` -- the CLI alias is friendlier). The `github_issue` trigger has no CLI alias today; author it via `--file` if you need it, and check `dispatch` (the run list view) to confirm it fires.

## Type immutability

Once a routine exists, you **cannot change its trigger TYPE** via `update`. The CLI rejects `--trigger` on `update` with a structured error pointing at delete-then-recreate. You CAN mutate the TYPE-specific fields below (the `at` datetime, the cron expression, the time zone) via `update --at` / `update --cron` / `update --time-zone`.

## `timer` -- one-shot at a specific datetime

Fields:

| Field          | Required | Source                          | Notes                                                                                 |
| -------------- | -------- | ------------------------------- | ------------------------------------------------------------------------------------- |
| `type`         | Yes      | wire-only                        | Value: `timer`.                                                                       |
| `at`           | Yes      | `--at` / manifest `at:`         | ISO 8601 datetime. Must be in the future (Foundry rejects past timestamps).            |
| `time_zone`    | No       | `--time-zone` / manifest `time_zone:` | IANA name (e.g. `America/New_York`). Defaults to `UTC`. Interpreted by Foundry server-side. |

Worked CLI invocation:

```bash
azd ai routine create nightly-once \
  --trigger timer \
  --at 2026-06-01T03:00:00Z \
  --action agent-response \
  --agent-endpoint-id /subscriptions/.../agents/my-agent
```

Worked manifest snippet:

```yaml
# routine.yaml
name: nightly-once
description: One-shot kick-off on 2026-06-01.
enabled: true
triggers:
  default:
    type: timer
    at: "2026-06-01T03:00:00Z"
    time_zone: UTC
action:
  type: invoke_agent_responses_api
  agent_endpoint_id: "/subscriptions/.../agents/my-agent"
```

Once a `timer` routine fires it has done its job. Foundry leaves it in place (you can re-arm it via `update --at <new-time>`); the CLI doesn't auto-delete fired one-shot routines.

## `recurring` -- cron schedule

Fields:

| Field             | Required | Source                                       | Notes                                                                                                            |
| ----------------- | -------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `type`            | Yes      | wire-only                                     | Value: `schedule` (NOT `recurring`; the CLI alias is `recurring`).                                                |
| `cron_expression` | Yes      | `--cron` / manifest `cron_expression:`       | Standard 5-field POSIX cron (minute hour day-of-month month day-of-week). Foundry enforces a **5-minute minimum interval**. |
| `time_zone`       | No       | `--time-zone` / manifest `time_zone:`        | IANA name. Defaults to `UTC`. Cron times are interpreted in this zone.                                            |

Foundry's 5-minute-minimum interval rule rejects cron expressions that would resolve to less than 5 minutes between firings (e.g. `*/2 * * * *`). Use `*/5 * * * *` as the floor.

Worked CLI invocation:

```bash
# Every weekday at 09:00 New York time.
azd ai routine create weekday-standup \
  --trigger recurring \
  --cron "0 9 * * 1-5" \
  --time-zone America/New_York \
  --action agent-invoke \
  --agent-endpoint-id /subscriptions/.../agents/my-standup-agent
```

Worked manifest snippet:

```yaml
name: weekday-standup
description: Daily 09:00 ET standup brief.
enabled: true
triggers:
  default:
    type: schedule
    cron_expression: "0 9 * * 1-5"
    time_zone: America/New_York
action:
  type: invoke_agent_invocations_api
  agent_endpoint_id: "/subscriptions/.../agents/my-standup-agent"
```

You CAN mutate the cron expression and the time zone after creation:

```bash
azd ai routine update weekday-standup --cron "0 10 * * 1-5"
azd ai routine update weekday-standup --time-zone Europe/London
```

## `github_issue` -- external event (deferred from the CLI)

The `github_issue` trigger type is **deferred from the CLI's `--trigger` switch today**. The wire shape accepts:

| Field           | Required | Notes                                                                                                 |
| --------------- | -------- | ----------------------------------------------------------------------------------------------------- |
| `type`          | Yes      | Value: `github_issue`. (The TypeSpec renames this to `github_issue_opened`; the live service still accepts `github_issue` only.) |
| `connection_id` | Yes      | Project connection that authenticates to GitHub (a `github` connection).                              |
| `repository`    | Yes      | `<owner>/<repo>` slug.                                                                                |
| `assignee`      | No       | GitHub login. Restricts firing to issues assigned to this user.                                       |

To use it today, author a manifest file and create via `--file`:

```yaml
name: triage-issues
description: Fire when an issue opens on contoso/widgets.
enabled: true
triggers:
  default:
    type: github_issue
    connection_id: gh-conn
    repository: contoso/widgets
    assignee: triage-bot
action:
  type: invoke_agent_responses_api
  agent_endpoint_id: "/subscriptions/.../agents/triage-agent"
```

```bash
azd ai routine create triage-issues --file ./routine.yaml
```

The CLI will accept this on `--file` (the JSON shape passes through verbatim). It will not let you author the same routine via `--trigger github-issue` today.

## Time zones

The `time_zone` field accepts standard IANA names (e.g. `America/New_York`, `Europe/London`, `Asia/Tokyo`). Defaults to `UTC` when omitted. Foundry interprets cron expressions and timer datetimes in this zone server-side; you don't need to do client-side conversion.

If you pass a string that is not a recognized IANA name (e.g. an abbreviation like `EST` or `PST`), Foundry rejects the routine on `create` / `update` with a structured error. Stick to the IANA database names.

## The `triggers` map and the `default` key

The Routine resource stores triggers as a **map keyed by name**, even though the CLI surface only authors a single trigger per routine. The CLI uses the constant key `default` for the trigger it authors. Wire shape:

```yaml
triggers:
  default:
    type: schedule
    cron_expression: "0 9 * * 1-5"
    time_zone: America/New_York
```

When `--file` authoring or hand-editing the manifest, keep the trigger under the `default` key for the CLI to see it. Future multi-trigger support will add named entries alongside `default`.

## Where to go next

* "Which action types pair with these triggers?" -> `actions`
* "How do I create / update / list routines via the CLI?" -> `manage`
* "How do I fire a routine right now to test the trigger setup?" -> `dispatch`
