# Release History

## 0.0.2-preview

### Changed

- Aligned data-plane client with Foundry Routines TypeSpec
  [azure-rest-api-specs PR #43498](https://github.com/Azure/azure-rest-api-specs/pull/43498):
  - `github_issue` trigger: replaced `assignee` with `owner`; added required
    `issue_event` (`opened`/`closed`).
  - Added new `custom` trigger (`provider`, `event_name`, `parameters`).
  - Removed `time_zone` from `timer` triggers (it remains valid for `schedule`).
  - Renamed action wire field `conversation_id` to `conversation`.
  - `invoke_agent_invocations_api` actions now accept `agent_name` in addition
    to `agent_endpoint_id` (mutually exclusive).
  - Added static `input` (any JSON value) on routine actions.
  - Routines list response uses `value` + `continuationToken`; run-history
    list uses `value` + `nextPageToken`. Both lists paginate via `limit` and
    `after` query parameters.
  - `RoutineRun` gained `trigger_name`, `agent_id`, `agent_endpoint_id`,
    `conversation_id`, `session_id`, `scheduled_fire_at`, and
    `error_status_code`; the legacy `diagnostics` block was removed.
  - Manual `dispatch` payload `input` is now any JSON value. The CLI
    auto-detects JSON literals on `--input` and forwards them as JSON;
    everything else flows through as a plain string.

### Added

- `routine create` and `routine update` now expose the new triggers:
  - `--trigger github-issue` with `--connection-id`, `--owner`,
    `--repository`, `--issue-event`.
  - `--trigger custom` with `--provider`, `--event-name`, `--parameters`
    (JSON object).

## 0.0.1-preview - Initial Version