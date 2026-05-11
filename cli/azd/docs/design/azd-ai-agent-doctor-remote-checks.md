<!-- cspell:ignore nextsteps unredacted walltime -->

# Design: `azd ai agent doctor` — Remote Checks (7–12)

## Status

**Draft — for design review.**
Tracking issue: [#7975](https://github.com/Azure/azure-dev/issues/7975).
Companion to [`azd-ai-agent-nextsteps.md`](./azd-ai-agent-nextsteps.md), which covers the doctor command's plumbing and local checks (1–6). **All code lives under `cli/azd/extensions/azure.ai.agents/`. No files outside the extension are modified.**

## Terminology

- **MVP doctor** — the first shippable slice of the command: local checks (1–6) only. Defined in the [companion doc](./azd-ai-agent-nextsteps.md). MVP is a chronological term ("what lands first"), not a permanent capability boundary.
- **Local checks** — read-only inspection of `azure.yaml`, `agent.yaml`, env vars, etc. No network, no token.
- **Remote checks** — anything that issues a network call under user credentials. Specified in this doc.

## Goal

Extend `azd ai agent doctor` past its local-only MVP so it can answer the question every developer eventually asks: *"the project is wired up correctly, so why doesn't this work end-to-end?"*

The end-state default is to run **all** checks (local then remote) on every `doctor` invocation. CI and air-gapped users opt out of the remote ones via `--local-only` (see [Execution Model](#execution-model)).

Concretely: add six checks that exercise the live Azure surface — auth, Foundry reachability, model deployments, user RBAC, agent runtime status, agent-identity role assignments — each with a precise "what to do next" fix.

Non-goals:
- Fixing remote problems automatically (`doctor --fix`). Surfacing the right command is enough for v1.
- Re-implementing what `az` / Foundry portal already do well — we just point the user there when relevant.
- Any modification to files outside `cli/azd/extensions/azure.ai.agents/`.

## Background

Local checks (1–6) — `azure.yaml` exists, env vars are set, YAML parses — run in tens of milliseconds and never need network or auth. They catch ~70% of "why is my project broken" questions and are what MVP doctor ships first.

The remaining ~30% — and arguably the most frustrating cases — *intentionally* require network calls under the user's credentials:
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
| 10 — User RBAC | Does my principal have the roles needed to deploy / invoke? |
| 11 — Agent status | Does the deployed agent actually exist and is it active? |
| 12 — Agent identity roles | What roles does the agent's managed identity have, and on which scopes? |

## Architecture

### Component layout (additive only)

```
cli/azd/extensions/azure.ai.agents/internal/cmd/
└── doctor/
    ├── checks_local.go       ← (MVP) checks 1–6
    ├── checks_remote.go      ← NEW: checks 7–12
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

### Default: all checks (1–12)

`azd ai agent doctor` runs the full check set in order. Local checks (1–6) run first; remote checks (7–12) run only if their preconditions pass (see [Dependency Matrix](#dependency-matrix)). The default is "tell me everything that's wrong" — a half-answer that skips the most frustrating cases isn't worth shipping.

### Opt-out: `--local-only`

`azd ai agent doctor --local-only` skips checks 7–12. Use this when:
- Running in CI / non-interactive shells where an interactive auth prompt would hang.
- Working offline / behind a restrictive firewall.
- Investigating a config-only issue and you don't want network latency.

Non-interactive detection (`!isTerminal`) is **not** automatic — explicit is better than magic. If a user forgets `--local-only` in CI, the auth check fails fast with a clear message and the rest of the remote checks Skip cleanly (see [Risks](#risks--mitigations)).

We considered `--remote` opt-in but rejected it: doctor's value is highest when it answers the live-Azure questions by default, and every "why doesn't this work" question we've seen in practice falls in that bucket. CI is the niche, not the default.

### Dependency matrix

| Check | Depends on | Action if dependency fails |
|---|---|---|
| 7 — Auth | check 3 (env selected) | Skip with "select an env first" |
| 8 — Reachability | 5 (`AZURE_AI_PROJECT_ENDPOINT` set), 7 (auth) | Skip |
| 9 — Models | 6 (service stanza valid), 8 (reachability) | Skip |
| 10 — RBAC | 7 (auth) | Skip — RBAC reads ARM, not Foundry data plane, so it does **not** depend on check 8 |
| 11 — Agent status | 7 (auth), 8 (reachability) | Skip — agents-list is a Reader-level Foundry call and does not require check 10's deploy/invoke roles |
| 12 — Agent identity roles | 11 (agent status pass — we need the agent's MI principal ID) | Skip — without an active agent there is no MI to introspect |

Skips are explicit in the rendered report. We never quietly drop a check.

## Check Specifications

### Check 7 — Authentication

**What it does:** acquires a token via `azidentity.NewAzureDeveloperCLICredential` (the same credential `agent_context.go` already uses), then introspects expiry.

**Pass:** "<UPN> · token valid for <N> minutes"
**Warn:** token expires in <5 min (suggest re-login proactively)
**Fail:** token acquisition error → `azd auth login`

**Why a separate fail vs warn:** users hit this all the time when a CLI session outlives a token; the warn variant tells them to re-login *before* a long-running deploy 401s.

### Check 8 — Foundry project reachability

**What it does:** issues a single `GET <AZURE_AI_PROJECT_ENDPOINT>/agents?api-version=<DefaultAgentAPIVersion>&$top=1` with the credential from check 7. The api-version is read from the `DefaultAgentAPIVersion` constant in `internal/cmd/agent_context.go` (currently `2025-11-15-preview`) — never a doc-side literal — so a single source of truth governs which Foundry surface we probe.

**Pass:** "endpoint reachable (HTTP 200)"
**Fail:** maps the HTTP status to one of:
- `401` → token expired or scope mismatch. Fix: `azd auth login`; if the issue persists, see check 7.
- `403` → wrong tenant **or** insufficient RBAC. Fix: confirm the active subscription/tenant matches the Foundry project; if it does, see check 10's role-assignment fix.
- `404` → endpoint is wrong or project is gone. Fix: `azd provision` or fix env var.
- network/DNS/TLS → "verify VPN / firewall / typo in `AZURE_AI_PROJECT_ENDPOINT`".

The single-shot probe avoids paging and works on tiny test projects. Timeout: 10s, no retries — doctor is diagnostic, not resilient.

### Check 9 — Model deployments

**What it does:** parses each service's `agent.yaml` for model references, then queries the Foundry project's deployments list once and matches names locally.

> **Tracking note ([#7962](https://github.com/Azure/azure-dev/issues/7962)).** Once the `agent.yaml` → `azure.yaml` unification lands, this check (and any other reference to `agent.yaml` in this doc) reads model refs from `azure.yaml` instead.

**Pass:** "all <N> referenced models present"
**Fail:** "missing: <model-name> (referenced by <service>)" → `azd provision` (or in the rare case the user deleted a deployment manually, "redeploy via Foundry portal" with link).

**Edge cases:**
- Multi-version models (e.g., `gpt-4o:2024-08-06`) — we match on deployment name, not version. Version mismatch surfaces at runtime; not in scope here.
- Agents with no model reference (orchestration-only) — pass with "no model references".

### Check 10 — User RBAC

**What it does:** for the current principal, lists role assignments on the Foundry project scope and checks for the minimum role set:
- `Azure AI Developer` (or stronger) on the project — required for deploy + invoke.
- `Cognitive Services User` on the AI account — required for model usage.

**Pass:** "<role-1>, <role-2>"
**Warn:** has invoke role but not deploy role → user can run agents but not `azd deploy`. Surfaces as "you can invoke but not deploy" with the exact `az role assignment create` command for the missing role.
**Fail:** missing both → command + portal link.

The fix command is templated with the actual principal ID and scope so the user can paste it directly. Example (interactive TTY):
```
fix:  az role assignment create \
        --role "Azure AI Developer" \
        --assignee <principal-id> \
        --scope <project-resource-id>
```

**Redaction in non-interactive output.** When stdout is piped or `--output json` is set, principal IDs, scope ARNs, and full UPNs in both `detail` and `fix` strings are replaced with `<redacted>`. The JSON envelope sets `"redacted": true` so callers know they're getting the safe variant (see [`Exit codes & JSON output`](./azd-ai-agent-nextsteps.md#exit-codes--json-output) in the companion doc). An interactive-only `--unredacted` flag exists for users who need the raw values in a JSON pipeline.

**Why we don't auto-create the assignment:** doctor is read-only. RBAC changes are the kind of action that should be explicit and audited.

### Check 11 — Agent status

**What it does:** for each service in `azure.yaml` with `host: ai-agent`, calls the Foundry agents-list endpoint and finds the matching agent by name.

**Pass:** "<agent-name>: active (v<N>)"
**Warn:** "<agent-name>: deploying (v<N>)" — running `monitor --follow` is suggested.
**Fail:**
- agent not found → `azd deploy`
- agent in failed state → `azd ai agent monitor --follow` (look at logs, not redeploy blindly)

This is the check that closes the loop on "I deployed and it succeeded — why doesn't `invoke` work?" Often the answer is the agent is still rolling out; check 11 says so explicitly.

### Check 12 — Agent identity role assignments

**What it does:** for each active agent found in check 11, resolves the agent's managed-identity principal ID, then lists role assignments on the **agent identity** at three scope levels:

1. The Foundry project (scope of the agent itself).
2. The parent AI account.
3. The resource group containing the project (proxy for "any resource the agent might touch").

We don't try to predict *which specific resource* the agent will need at invocation time — that's data-plane and varies per agent code. Instead we surface the visible role-assignment landscape so the user can answer "does my agent identity have any roles where I'd expect it to?"

**Pass:** agent MI has at least one assignment at the project scope, plus a non-empty list at account or resource-group scope.
**Warn:** agent MI has assignments only at the agent's own scope — it almost certainly cannot read project / account resources at runtime. Render the list with a "this looks under-privileged for typical agents" hint.
**Fail:** agent MI has zero role assignments anywhere reachable. This is the smoking-gun for "my agent runs but every tool call 403s."

**Output shape:**
```
ⓘ INFO  agent identity roles  (agent: research-bot)
        principal: <mi-principal-id>
        project scope:
          - Cognitive Services User
        account scope:
          - (none)
        resource-group scope:
          - Storage Blob Data Reader
```

We render this as `INFO` (a fourth glyph alongside pass/warn/fail/skip) when there's nothing actionable to flag, since the value is mostly informational — the user inspects the list to confirm it matches their mental model. Warn/Fail variants surface only the actionable cases above.

**Why a separate check from 10:** check 10 is about the *user's* permissions to deploy/invoke; check 12 is about the *agent's* permissions at runtime. They fail differently, fix differently (`az role assignment create --assignee <mi-id>` vs `<user-id>`), and the agent MI doesn't exist until check 11 has confirmed an active agent — so they can't be merged without losing precision.

**Redaction.** Same rules as check 10: principal IDs and scope ARNs are replaced with `<redacted>` in non-interactive output, and the JSON envelope sets `"redacted": true`.

## Output Format

Same renderer as MVP. Skipped checks render with a distinct glyph so they're visible:

```
azd ai agent doctor
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
  ✗ FAIL  user RBAC
          principal alice@contoso.com has no role on project scope
          fix:  az role assignment create --role "Azure AI Developer" \
                  --assignee 0000... --scope /subscriptions/.../projects/...
  -  SKIP  agent status
          skipped: reachability check failed
  -  SKIP  agent identity roles
          skipped: agent status check did not pass

Next:  az role assignment create ...     -- grant yourself the required role
       azd ai agent doctor               -- re-run to verify
```

The trailing `Next:` block is resolved from the **first** failed remote check that has a single user-actionable fix — same logic as MVP, just extended to know about remote-check fix commands.

## Performance & Side Effects

Per [Performance Budget](#performance-budget) below — `--local-only` keeps doctor under 100ms for users who explicitly opt out. Default mode adds a few seconds and explicit credential use.

| Check | Network calls | Worst-case time |
|---|---|---|
| 7 — Auth | 0 (cached token) – 1 (refresh) | 2s |
| 8 — Reach | 1 GET | 10s (timeout) |
| 9 — Models | 1 list | 5s |
| 10 — User RBAC | 1 list-role-assignments | 5s |
| 11 — Status | 1 list-agents | 5s |
| 12 — Agent MI roles | 3 list-role-assignments (project / account / RG) | 6s |

Worst-case full-sweep walltime: ~35s with a sick endpoint. Each check has its own timeout, applied via `context.WithTimeout`. The runner reports per-check duration in `--verbose` mode.

## Testing Strategy

- **Unit tests per check** with a fake `agentClient` interface. Cover pass / warn / fail / skip paths and HTTP status mapping (especially 401/403/404 disambiguation in check 8 — including the "wrong tenant or insufficient RBAC" wording for 403).
- **Skip-cascade test.** For each dependency edge in the matrix above, assert the dependent check returns `Status: Skip` with the expected detail string when its dependency returns `Status: Fail`. Run as a table-driven test over the matrix so adding/removing edges automatically widens coverage.
- **Redaction test.** Snapshot the rendered RBAC fix string in interactive (full values) and non-interactive (`<redacted>`) modes, plus the JSON envelope's `redacted` field.
- **Snapshot test** for the rendered report in mixed pass/fail/skip configurations — same harness as MVP doctor's local-checks snapshot test.
- **Functional test** (recorded cassette via `mage record`) covering one happy-path full run.
- No live tests in CI for the remote-check path — replay-only. Cassette refresh is a manual maintainer step.

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| User in CI forgets `--local-only`; remote checks 401 or hang on auth refresh | Detect non-interactive (`!isTerminal`) inside check 7; if a token refresh would require interaction, fail fast with `Status: Fail, detail: "non-interactive shell — re-run with --local-only"`. Checks 8–12 then Skip via the dependency matrix. No hang, no silent success. |
| RBAC list call requires a permission users may also lack | If the role-assignments call itself 403s in checks 10 or 12, render the check as Warn, not Fail — give the user the role-assignment command anyway and let them try. |
| Foundry API drift breaks the reachability probe | Pin the api-version to `DefaultAgentAPIVersion` (`internal/cmd/agent_context.go`), not a doc literal. Failure renders as "API not understood" with a doc link rather than a stack trace. |
| Slow tenant or VPN turns full sweep into a long wait | Strict per-check timeouts. After the dependency-matrix simplification above, **independent checks 8 and 10 can run concurrently** once their shared dependency (check 7) passes. Check 9 still waits on 8; check 11 waits on 8; check 12 waits on 11. |
| User runs `doctor` against a fresh project before `azd provision` | Checks 8–12 short-circuit to Skip via the dependency matrix; check 5 (env var missing) tells them to provision. |
| Sensitive identifiers (principal IDs, scope ARNs) leak into shared CI logs | See [Output Format](#output-format) and the redaction rule in checks 10 and 12: non-interactive output replaces values with `<redacted>` and the JSON envelope flags `"redacted": true`. |

## Performance Budget

`--local-only` doctor: <100ms. Don't regress.
Default (full sweep): ≤ 35s walltime in the worst case (sick endpoint). Typical: <6s.
We do **not** add any background polling, prefetching, or daemon work for these checks. Doctor remains a one-shot diagnostic.

## Implementation Phases

1. **Runner refactor** — split `checks_local.go` / `checks_remote.go`, add `--local-only` flag (default false), no behavior change at this step beyond exposing the flag.
2. **Auth + reachability (7, 8)** — the two checks every other remote check depends on. Useful even on their own.
3. **Models + agent status (9, 11)** — leverage existing `agentClient` calls. Closes the deploy-loop questions.
4. **User RBAC (10)** — role-list API for the current principal, with the fix-command wording.
5. **Agent identity roles (12)** — last because it depends on a passing check 11 and adds a new INFO glyph to the renderer.

Each phase is independently shippable. While 7–8 are landing, the renderer can already display "remote checks not yet implemented" for 9–12 without breaking existing users (who will be on `--local-only` behavior until they explicitly opt in via the flag-default flip in phase 1).

## Appendix: Why is this a separate doc from MVP?

Three reasons, in order of weight:

1. **Auth + network coupling.** Local checks (1–6) run in any project state, including before `azd auth login`. Remote checks fundamentally cannot. Splitting the design makes the contract explicit and lets us iterate on the remote surface without churning the local one.
2. **Review surface.** The MVP design (the companion doc) is already large. Adding six live-Azure checks with their own error mapping, timeout, redaction, and skip semantics doubles it.
3. **Iteration cost on Foundry surfaces.** The Foundry agents API is still evolving; we want to land remote checks in a phased rollout (see [Implementation Phases](#implementation-phases)) so we can iterate the wording, the role-set, and the api-version pinning without churning everything at once.
