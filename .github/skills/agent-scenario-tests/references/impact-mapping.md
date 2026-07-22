<!-- cspell:ignore nextstep exterrors -->
# Impact mapping — changed files → scenario tags

Goal: from a PR's changed-file list, derive the **smallest** scenario tag set that still
covers the change, plus the tier ceiling for cost gating.

All paths below are relative to `cli/azd/extensions/azure.ai.agents/`.

## 1. Command source → `cmd:*` tag

Files under `internal/cmd/` map to the command they implement:

| Changed file (glob) | Tag(s) | Notes |
| --- | --- | --- |
| `internal/cmd/init*.go` | `cmd:init` | Includes `init_from_code*`, `init_from_templates*`, `init_models`, `init_locations`, `init_validate`, `init_copy`, `init_foundry_resources_helpers`. |
| `internal/cmd/show.go` | `cmd:show` | |
| `internal/cmd/invoke*.go` | `cmd:invoke` | `invoke.go`, `invoke_raw.go`. |
| `internal/cmd/run.go` | `cmd:run` | |
| `internal/cmd/session.go` | `cmd:sessions` | |
| `internal/cmd/files.go` | `cmd:files` | |
| `internal/cmd/monitor*.go` | `cmd:monitor` | `monitor.go`, `monitor_format.go`. |
| `internal/cmd/update.go` | `cmd:endpoint` | `update.go` defines `endpoint update`. |
| `internal/cmd/doctor*.go` | `cmd:doctor` | `doctor.go`, `doctor_format.go`. |
| `internal/cmd/eval*.go` | `cmd:eval` | `eval.go`, `eval_init.go`, `eval_run.go`, `eval_list.go`, `eval_show.go`, etc. Tier 2 (needs a deployed agent + Foundry endpoint). |
| `internal/cmd/optimize*.go` | `cmd:optimize` | `optimize.go`, `optimize_apply.go`, `optimize_status.go`, etc. Tier 2 (submits a cloud optimization job). |
| `internal/cmd/sample*.go` | `cmd:sample` | `sample.go`, `sample_list.go`. |
| `internal/cmd/code*.go` | `cmd:code` | `code.go` (code download). |
| `internal/cmd/delete*.go` | `cmd:delete` | `delete.go` (agent deletion). |
| `internal/cmd/version.go` | `cmd:version` | |
| `internal/cmd/root.go` | `cmd:help` + broad | Touches the whole command tree — treat as broad (see §3). |
| `internal/cmd/listen.go` | — | gRPC host entrypoint; not scenario-testable. |

## 2. Changed command with NO scenario coverage (gaps)

These commands have **no** scenario in the suite yet. If the PR touches them, you cannot
run a regression check — **report the gap** and recommend the author add a scenario
(per the extension `AGENTS.md`), rather than silently passing:

| Changed file (glob) | Uncovered command |
| --- | --- |
| `internal/cmd/mcp.go` | `mcp start` (hidden/preview) |

## 3. Shared / cross-cutting code → broaden

Changes outside a single command file affect many flows. When the diff touches any of
these, broaden the impacted set (and ask the user how wide to go):

| Changed file (glob) | Broaden to |
| --- | --- |
| `internal/cmd/helpers.go`, `internal/cmd/agent_context.go`, `internal/cmd/agent_endpoint.go`, `internal/cmd/*_context.go` | All `cmd:*` for commands that resolve project/agent context — at minimum `cmd:init`, `cmd:invoke`, `cmd:show`, `cmd:doctor`. |
| `internal/cmd/root.go`, `internal/cmd/banner.go`, `internal/cmd/nextstep_output.go` | Run a Tier 0 smoke set (`tier:0`) across all commands. |
| `internal/pkg/**`, `internal/project/**`, `internal/exterrors/**` | Map by what the package feeds: parsers/manifests → `cmd:init`; deployment/project target → `cmd:provision` + `cmd:deploy` (Tier 2). When unclear, propose a Tier 0/1 sweep and ask before any Tier 2. |
| `go.mod` / `go.sum` / dependency bumps | Tier 0 smoke + ask whether a fuller sweep is warranted. |
| files **outside** `cli/azd/extensions/azure.ai.agents/` (e.g. `cli/azd/` core) | This skill is scoped to the agents extension; note that core changes may need core azd testing instead, and proceed only with the agents-relevant subset. |

## 4. Tier ceiling (cost gate)

From the impacted `cmd:*` set, decide the **highest tier to offer**:

- Default to **Tier 0 + Tier 1** for any change to a covered command (free + auth-only).
- Offer **Tier 2** only when the change can plausibly affect cloud behavior — i.e. it
  touches `cmd:invoke`, `cmd:sessions`, `cmd:files`, `cmd:monitor`, `cmd:endpoint`,
  `cmd:show`, `cmd:run`, `cmd:eval`, `cmd:optimize`, `cmd:doctor` provisioned paths,
  deployment/project code, or the provision/deploy flow. `cmd:eval` and `cmd:optimize`
  are Tier 2-only (no offline happy path beyond their negative-path validation
  scenarios). Tier 2 always requires the explicit cost confirmation in
  `workflow.md` Step 4.
- A pure Tier 0 change (e.g. `version.go`, help text, `sample list` formatting) should run
  Tier 0 only.

## 5. Translate tags → run list

Combine the derived `cmd:*` tags with the chosen tier tags and call
`list_scenarios(tags=[...])`. Example: an `invoke.go` change approved for Tier 2 →
`list_scenarios(tags=["cmd:invoke"])`, then keep the Tier 0/1 results plus the Tier 2
`22-*` scenarios, prefixed by `20-setup` and suffixed by `2Z-teardown`.
