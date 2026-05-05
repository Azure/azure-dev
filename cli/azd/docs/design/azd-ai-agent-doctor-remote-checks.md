# Design: `azd ai agent doctor` — Remote Checks (7–11)

## Status

**Draft — for design review.**
Tracking issue: [#7975](https://github.com/Azure/azure-dev/issues/7975).
Companion to [`azd-ai-agent-nextsteps.md`](./azd-ai-agent-nextsteps.md), which covers the doctor command's plumbing and local checks (1–6). **All code lives under `cli/azd/extensions/azure.ai.agents/`. No files outside the extension are modified.**

## Goal

Extend `azd ai agent doctor` past its local-only MVP so it can answer the question every developer eventually asks: *"the project is wired up correctly, so why doesn't this work end-to-end?"*

Concretely: add five checks that exercise the live Azure surface — auth, Foundry reachability, model deployments, RBAC, agent runtime status — each with a precise "what to do next" fix.

Non-goals:
- Fixing remote problems automatically (`doctor --fix`). Surfacing the right command is enough for v1.
- Re-implementing what `az` / Foundry portal already do well — we just point the user there when relevant.
- Any modification to files outside `cli/azd/extensions/azure.ai.agents/`.

## Background

The MVP doctor checks (1–6) are pure local reads — `azure.yaml` exists, env vars are set, YAML parses. They run in tens of milliseconds and never need network or auth. They catch ~70% of "why is my project broken" questions.

The remaining ~30% — and arguably the most frustrating cases — sit in remote land:
- Token expired silently → every command 401s with no obvious "you're logged out" hint.
- Foundry endpoint typo'd in env → DNS/TLS error buried in a stack trace.
- Model deployment got deleted out from under the project → first invoke fails with a cryptic 404.
- Azure-AI Developer role missing → vague 403.
- Agent deployed but unhealthy → user thinks `azd deploy` succeeded so it must be fine.

These all require live calls and credentials, which is why they're carved out into this separate design.

## Scope of this doc

| Check | What it answers |
|---|---|
| 7 — Authentication | Am I logged in? Is the token still valid? |
| 8 — Foundry reachability | Can I reach `AZURE_AI_PROJECT_ENDPOINT`? |
| 9 — Model deployments | Do the model(s) my agent references exist on the project? |
| 10 — RBAC | Does my principal have the roles needed to deploy / invoke? |
| 11 — Agent status | Does the deployed agent actually exist and is it active? |

## Architecture

### Component layout (additive only)

```
cli/azd/extensions/azure.ai.agents/internal/cmd/
└── doctor/
    ├── checks_local.go       ← (MVP) checks 1–6
    ├── checks_remote.go      ← NEW: checks 7–11
    ├── runner.go             ← orchestrates ordered execution
    └── types.go              ← Check, Result, Severity
```

Existing MVP files unchanged. Only `runner.go` learns to dispatch the new check set, gated by a flag (see [Execution Model](#execution-model)).

### `Check` contract

Same shape as MVP — keeps the renderer, sort order, and `Next:` resolver code path identical.

```go
type Check interface {
    ID() string                   // stable, e.g. "auth.token"
    Title() string                // human label
    Run(ctx context.Context) Result
}

type Result struct {
    Status   Status               // Pass | Warn | Fail | Skip
    Detail   string               // one-line description
    Fix      string               // single command or instruction; empty for Pass
    Links    []string             // doc links (optional)
    Duration time.Duration        // captured by runner, not the check
}
```

A remote check that can't run (e.g., not authenticated when running check 9) returns `Status: Skip` with `Detail: "skipped: authentication required (see check 7)"`. Skips never block subsequent checks — the user sees the full picture in one pass.

## Execution Model

### Default: local-only

`azd ai agent doctor` runs checks 1–6 only. Same as MVP. No network, no token, no surprise prompts. This is the daily-driver mode.

### Opt-in: `--remote`

`azd ai agent doctor --remote` runs **1–11** in order. Local checks always run first; remote checks run only if their preconditions pass (see [Dependency Matrix](#dependency-matrix)).

Rationale for opt-in (vs. always-on or auto-detect):
- Remote checks need credentials and add seconds to a command users may run frequently.
- Hitting Foundry on every `doctor` invocation is bad citizenship and surprising in CI.
- A flag is discoverable, reversible, and self-documenting.

We considered `--quick` / `--full` naming but `--remote` describes the actual side effect — network calls under your credentials — which is what reviewers and CI authors care about.

### Dependency matrix

| Check | Depends on | Action if dependency fails |
|---|---|---|
| 7 — Auth | check 3 (env selected) | Skip with "select an env first" |
| 8 — Reachability | 5 (`AZURE_AI_PROJECT_ENDPOINT` set), 7 (auth) | Skip |
| 9 — Models | 6 (`agent.yaml` valid), 8 (reachability) | Skip |
| 10 — RBAC | 7 (auth), 8 (reachability) | Skip |
| 11 — Agent status | 8, 9, 10 | Skip |

Skips are explicit in the rendered report. We never quietly drop a check.

## Check Specifications

### Check 7 — Authentication

**What it does:** acquires a token via `azidentity.NewAzureDeveloperCLICredential` (the same credential `agent_context.go` already uses), then introspects expiry.

**Pass:** "<UPN> · token valid for <N> minutes"
**Warn:** token expires in <5 min (suggest re-login proactively)
**Fail:** token acquisition error → `azd auth login`

**Why a separate fail vs warn:** users hit this all the time when a CLI session outlives a token; the warn variant tells them to re-login *before* a long-running deploy 401s.

### Check 8 — Foundry project reachability

**What it does:** issues a single `GET <AZURE_AI_PROJECT_ENDPOINT>/agents?api-version=…&$top=1` with the credential from check 7.

**Pass:** "endpoint reachable (HTTP 200)"
**Fail:** maps the HTTP status to one of:
- `403` → check 10 (RBAC) will explain. Fix: see check 10 output.
- `404` → endpoint is wrong or project is gone. Fix: `azd provision` or fix env var.
- network/DNS/TLS → "verify VPN / firewall / typo in `AZURE_AI_PROJECT_ENDPOINT`".

The single-shot probe avoids paging and works on tiny test projects. Timeout: 10s, no retries — doctor is diagnostic, not resilient.

### Check 9 — Model deployments

**What it does:** parses each service's `agent.yaml` for model references, then queries the Foundry project's deployments list once and matches names locally.

**Pass:** "all <N> referenced models present"
**Fail:** "missing: <model-name> (referenced by <service>)" → `azd provision` (or in the rare case the user deleted a deployment manually, "redeploy via Foundry portal" with link).

**Edge cases:**
- Multi-version models (e.g., `gpt-4o:2024-08-06`) — we match on deployment name, not version. Version mismatch surfaces at runtime; not in scope here.
- Agents with no model reference (orchestration-only) — pass with "no model references".

### Check 10 — RBAC

**What it does:** for the current principal, lists role assignments on the Foundry project scope and checks for the minimum role set:
- `Azure AI Developer` (or stronger) on the project — required for deploy + invoke.
- `Cognitive Services User` on the AI account — required for model usage.

**Pass:** "<role-1>, <role-2>"
**Warn:** has invoke role but not deploy role → user can run agents but not `azd deploy`. Surfaces as "you can invoke but not deploy" with the exact `az role assignment create` command for the missing role.
**Fail:** missing both → command + portal link.

The fix command is templated with the actual principal ID and scope so the user can paste it directly. Example:
```
fix:  az role assignment create \
        --role "Azure AI Developer" \
        --assignee <principal-id> \
        --scope <project-resource-id>
```

**Why we don't auto-create the assignment:** doctor is read-only. RBAC changes are the kind of action that should be explicit and audited.

### Check 11 — Agent status

**What it does:** for each service in `azure.yaml` with `host: ai-agent`, calls the Foundry agents-list endpoint and finds the matching agent by name.

**Pass:** "<agent-name>: active (v<N>)"
**Warn:** "<agent-name>: deploying (v<N>)" — running `monitor --follow` is suggested.
**Fail:**
- agent not found → `azd deploy`
- agent in failed state → `azd ai agent monitor --follow` (look at logs, not redeploy blindly)

This is the check that closes the loop on "I deployed and it succeeded — why doesn't `invoke` work?" Often the answer is the agent is still rolling out; check 11 says so explicitly.

## Output Format

Same renderer as MVP. Skipped checks render with a distinct glyph so they're visible:

```
azd ai agent doctor --remote
  ✓ PASS  azd reachable
  ✓ PASS  azure.yaml valid
  ✓ PASS  azd environment selected
  ✓ PASS  agent service in azure.yaml
  ✓ PASS  AZURE_AI_PROJECT_ENDPOINT set
  ✓ PASS  agent.yaml valid
  ✓ PASS  authentication
          alice@contoso.com · token valid for 47 minutes
  ✗ FAIL  Foundry project reachability
          GET /agents → 403
          fix:  see RBAC check below
  -  SKIP  model deployments
          skipped: reachability check failed
  ✗ FAIL  RBAC
          principal alice@contoso.com has no role on project scope
          fix:  az role assignment create --role "Azure AI Developer" \
                  --assignee 0000... --scope /subscriptions/.../projects/...
  -  SKIP  agent status
          skipped: reachability check failed

Next:  az role assignment create ...     -- grant yourself the required role
       azd ai agent doctor --remote      -- re-run to verify
```

The trailing `Next:` block is resolved from the **first** failed remote check that has a single user-actionable fix — same logic as MVP, just extended to know about remote-check fix commands.

## Performance & Side Effects

Per [Performance Budget](#performance-budget) below — defaults remain local-only so users can keep running `doctor` reflexively. `--remote` adds a couple of seconds and explicit credential use.

| Check | Network calls | Worst-case time |
|---|---|---|
| 7 — Auth | 0 (cached token) – 1 (refresh) | 2s |
| 8 — Reach | 1 GET | 10s (timeout) |
| 9 — Models | 1 list | 5s |
| 10 — RBAC | 1 list-role-assignments | 5s |
| 11 — Status | 1 list-agents | 5s |

Worst-case `--remote` walltime: ~30s with a sick endpoint. Each check has its own timeout, applied via `context.WithTimeout`. The runner reports per-check duration in `--verbose` mode.

## Testing Strategy

- **Unit tests per check** with a fake `agentClient` interface. Cover pass / warn / fail / skip paths and HTTP status mapping (especially 401/403/404 disambiguation in check 8).
- **Snapshot test** for the rendered report in mixed pass/fail/skip configurations — same harness as MVP doctor's local-checks snapshot test.
- **Functional test** (recorded cassette via `mage record`) covering one happy-path full run.
- No live tests in CI for `--remote` — replay-only. Cassette refresh is a manual maintainer step.

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Auth prompt during `--remote` surprises users in CI | Detect non-interactive (`!isTerminal`) and refuse the auth refresh; emit a clear "skipped: non-interactive" instead of hanging. |
| RBAC list call requires a permission users may also lack | If the role-assignments call itself 403s, render the check as Warn, not Fail — give the user the role-assignment command anyway and let them try. |
| Foundry API drift breaks the reachability probe | Pin the api-version explicitly. Failure renders as "API not understood" with a doc link rather than a stack trace. |
| Slow tenant or VPN turns `--remote` into a 30s wait | Strict per-check timeouts, parallel fan-out for independent checks (8 + 10 can run concurrently). |
| User runs `--remote` against a fresh project before `azd provision` | Checks 8–11 short-circuit to Skip via the dependency matrix; check 5 (env var missing) tells them to provision. |

## Performance Budget

Local-only `doctor` (today): <100ms. Don't regress.
`--remote`: ≤ 30s walltime in the worst case (sick endpoint). Typical: <5s.
We do **not** add any background polling, prefetching, or daemon work for these checks. Doctor remains a one-shot, opt-in diagnostic.

## Implementation Phases

1. **Runner refactor** — split `checks_local.go` / `checks_remote.go`, add `--remote` flag, no behavior change at this step.
2. **Auth + reachability (7, 8)** — the two checks every other remote check depends on. Useful even on their own.
3. **Models + agent status (9, 11)** — leverage existing `agentClient` calls. Closes the deploy-loop questions.
4. **RBAC (10)** — last because the role-list API is the trickiest to stub and the most likely to need iteration on the fix-command wording.

Each phase is independently shippable and behind the `--remote` flag, so partial rollout is safe. We can ship phases 1–2 and gather feedback before completing 3–4.

## Appendix: Why not fold these into MVP?

Three reasons, in order of weight:

1. **Auth + network coupling.** MVP doctor runs in any project state, including before `azd auth login`. Remote checks fundamentally cannot. The flag separation makes the contract explicit.
2. **Review surface.** The MVP design (the companion doc) is already large. Adding five live-Azure checks with their own error mapping, timeout, and skip semantics doubles it.
3. **Iteration cost on Foundry surfaces.** The Foundry agents API is still evolving; we want to land remote checks behind a flag so we can iterate the wording, the role-set, and the api-version pinning without churning the always-on path.
