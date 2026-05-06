# Design: Context-Aware Next-Step Guidance for `azd ai agent`

## Status

**Draft — for design review.**
Tracking issue: [#7975](https://github.com/Azure/azure-dev/issues/7975) — "Context-aware next-step guidance and diagnostics".
Scope: the `azure.ai.agents` extension only. **All code lives under `cli/azd/extensions/azure.ai.agents/`. No files outside the extension are modified.** The deploy hook returns its Next: block via the standard extension SDK return value (`*azdext.Artifact`) — specifically the `Metadata["note"]` key on an `ArtifactKindEndpoint` artifact, which `pkg/project/artifact.go` already renders as a continuation line under the artifact bullet (gated on `Kind == ArtifactKindEndpoint`). The extension already emits endpoint-kind artifacts at `internal/project/service_target_agent.go`; we populate the `note` metadata key on the same artifacts.

## Goal

After every successful `azd ai agent` command — and on demand via a new `azd ai agent doctor` — print a short, state-aware **Next:** block telling the developer the single best command to run next. Replace the current static, often-misleading hints with output derived from the project's real state (`azure.yaml`, the azd environment, optionally a running agent's OpenAPI endpoint).

Non-goals:
- Reworking error-path guidance — `internal/exterrors` already does this well.
- Any modification to files outside `cli/azd/extensions/azure.ai.agents/`. State is read via the existing gRPC `azdext` API.
- Sample-author hooks, README markers, or YAML-declared hints — out of scope for this design.
- Authoring or editing core azd packages (`cli/azd/pkg/...`, `cli/azd/cmd/...`). The design only *consumes* the existing `Metadata["note"]` rendering contract.

## Assumptions

This design assumes the unified-`azure.yaml` proposal in [#7975](https://github.com/Azure/azure-dev/issues/7975) has landed: a single `azure.yaml` is the canonical config; `agent.yaml` and `agent.manifest.yaml` no longer exist as separate files. References to `agent.yaml` in the doctor checks and decision trees below are placeholders for the equivalent fields in the unified `services.<name>` stanza. If the extension ships before unification, each `agent.yaml` reference should be read as the corresponding fields under `services.<name>` in `azure.yaml`. The `AssembleState` function abstracts the source split, so resolvers are insulated from the migration — only the state-assembly code needs to change.

The extension's authentication remains `azidentity.NewAzureDeveloperCLICredential` (see [`agent_context.go`](../../extensions/azure.ai.agents/internal/cmd/agent_context.go)). User-facing fix commands therefore say `azd auth login`, not `az login`.

## Background

### What's wrong today

| Surface | Today | Problem |
|---|---|---|
| `init` | Hardcoded: "run `azd deploy` or `azd up`" | Skips local-dev path entirely. Suggests deploy even when deps aren't provisioned. |
| `run` | Hardcoded: `invoke --local "Hello!"` | Wrong payload for invocations-protocol agents. No OpenAPI awareness. |
| `invoke --local` (success) | Nothing | Dead-end. No nudge toward `azd deploy`. |
| `invoke` (remote, success) | Nothing | No nudge to `show` / `monitor`. |
| `show` (success) | Nothing | No nudge to invoke or check logs. |
| `deploy` | Endpoint URLs only | No invoke command, no link to README. |
| (none) | — | No way to ask "where was I?" or "what's broken across the project?" |

### What's already in place

These are the building blocks the design reuses:

- **`azdext` gRPC client** — extensions read `azure.yaml` services and azd env vars without re-implementing parsing.
- **`fetchOpenAPISpec`** in `helpers.go` — already probes `GET /invocations/docs/openapi.json`, caches to disk, fails silently. Drives the rich-payload story.
- **`exterrors`** factories — typed errors with `Suggestion` fields. We follow the same "structured advice" shape on success paths.
- **`azdext.Artifact` return value** from `Deploy()` — the extension SDK's normal mechanism for an extension to surface lines below its progress bullet. The renderer is `pkg/project/artifact.go` (specifically the `Metadata["note"]` branch at `ToString`, ~line 128), which is gated on `Kind == ArtifactKindEndpoint`. The extension already emits endpoint-kind artifacts in `internal/project/service_target_agent.go` (~line 720) for endpoint URLs. We attach our Next: block to the same artifacts via the same `Metadata["note"]` key. No changes to the renderer or any core file. Indentation rules are documented in [Output Discipline](#output-discipline) and pinned by an extension-side regression test that asserts the formatted string against a stub artifact.

## Architecture

### Component layout

```
cli/azd/extensions/azure.ai.agents/internal/cmd/
├── nextstep/                     ← new package (this design)
│   ├── types.go                  ← Suggestion, State, ServiceState, AuthState
│   ├── state.go                  ← AssembleState(ctx, azdClient, opts) → State
│   ├── resolver.go               ← decision tree per command
│   ├── format.go                 ← PrintNext / FormatNextForNote
│   └── openapi.go                ← OpenAPI-derived invoke examples
├── doctor/                       ← new package (matches structure in remote-checks doc)
│   ├── types.go                  ← Check, Result, Status
│   ├── runner.go                 ← orchestrates ordered execution + Next: tail
│   └── checks_local.go           ← MVP local checks (1–6)
├── init.go        ─┐
├── run.go         ─┼─ each calls nextstep.ResolveAfterX → nextstep.PrintNext
├── invoke.go      ─┤
├── show.go        ─┘
└── ...
internal/project/
└── service_target_agent.go       ← Deploy() embeds Next: in its returned artifact
```

One package owns *all* policy. Each command is a thin caller — it knows when it succeeded and which resolver to invoke; it does not assemble state, format output, or decide commands.

### End-to-end flow

```
┌────────────────────────────────────────────────────────────────────┐
│ azd ai agent <cmd> succeeds                                        │
│                                                                    │
│   1. nextstep.AssembleState(ctx, azdClient)                        │
│      ├── azdext.Project().Get      → azure.yaml services           │
│      ├── azdext.Environment().Get  → azd env vars                  │
│      └── (run only) fetchOpenAPISpec → invoke payload examples     │
│                                                                    │
│   2. nextstep.ResolveAfter<Cmd>(state, args) → []Suggestion        │
│      ├── walks command-specific decision tree                      │
│      └── chooses 1 primary + ≤1 secondary                          │
│                                                                    │
│   3a. nextstep.PrintNext(w, suggestions)        ← stdout commands  │
│   3b. nextstep.FormatNextForNote(suggestions)   ← deploy artifact  │
└────────────────────────────────────────────────────────────────────┘
```

`deploy` is the special case: an extension's `Deploy()` returns `[]*azdext.Artifact` and that is the extension's only output channel after the call returns. We populate the artifact's note field with the formatted Next: block — exactly the same mechanism `service_target_agent.go` already uses today to print endpoint URLs. Everywhere else, the extension writes directly to stdout (or stderr when piped — see [Output Discipline](#output-discipline)).

## State Model

```go
// types.go
type AuthState int

const (
    AuthUnknown   AuthState = iota // probe was not run; suggestions must skip auth-conditional advice
    AuthAuthed                     // doctor confirmed a usable token
    AuthUnauthed                   // doctor confirmed login is needed
)

type State struct {
    HasProjectEndpoint bool
    MissingInfraVars   []string  // ${...} → Bicep outputs not in azd env (named for actionable advice)
    MissingManualVars  []string  // ${...} → user-supplied vars not set
    Services           []ServiceState
    AgentStatus        string    // optional (Foundry API), empty if unknown
    HasOpenAPI         bool      // populated only when AssembleState is called from `run` or `doctor` (any mode that contacts the agent)
    OpenAPIPayload     string    // populated only when AssembleState is called from `run` or `doctor` (any mode that contacts the agent)
    IsAuthenticated    AuthState // populated only when AssembleState is called from `doctor` (full sweep, i.e. not `--local-only`)
}

type ServiceState struct {
    Name         string
    Host         string  // azure.ai.agent / azure.ai.toolbox / ...
    Protocol     string  // responses / invocations / ""
    RelativePath string  // svc.RelativePath from azure.yaml
    IsDeployed   bool    // azd env: deployment metadata present
}

type Suggestion struct {
    Command     string  // "azd ai agent run"
    Description string  // "start the agent locally"
    Priority    int     // lower = earlier
}
```

State is **assembled fresh on each call**. No singleton, no cache across commands. `azure.yaml` parsing inside one call is cached by `azdClient` already. The base cost is one gRPC round-trip per resolver invocation, which is negligible.

The `IsAuthenticated` probe is **not** part of the base assembly: it requires a token-introspection call which is network-bound and would regress the perf claim above for every command. `AssembleState` accepts an option (`WithAuthProbe`) that defaults to false; the full-sweep `doctor` path is the only caller that sets it (`--local-only` runs leave it false). Every other resolver receives `IsAuthenticated == AuthUnknown` and treats auth-conditional suggestions as "skip advice that needs a confirmed login state" rather than "tell user to log in" — i.e., we never produce login-prompt noise in success paths.

### Layered sources

The resolver works with whatever's available — partial state never silences guidance:

| Layer | Source | Authority |
|---|---|---|
| 1 | Explicit CLI flags (`--project-endpoint`, `--agent`) | Highest |
| 2 | Live runtime probes (OpenAPI, Foundry status) | Override env vars |
| 3 | `azure.yaml` services, protocols, `config.env` refs | Structural truth |
| 4 | azd env vars | Resolution truth |

If the azd environment is missing or stale (a real scenario for brownfield users), the resolver should still produce useful output from layers 1–3.

## Decision Tree (per command)

The full tree lives in `resolver.go`. This section captures the policy in tabular form for review.

### `init`

| Condition | Primary | Secondary |
|---|---|---|
| `len(MissingInfraVars) > 0` | `azd provision` | — |
| `len(MissingManualVars) > 0` | `azd env set <KEY> <value>` (one per missing var, up to 3) | — |
| Otherwise | `azd ai agent run` | — (the `run` resolver will print the protocol-correct `invoke --local` when the agent starts) |
| Always (third line) | "When ready to deploy to Azure, run `azd deploy`." | — |

### `run` (on listening callback)

| Condition | Primary |
|---|---|
| `HasOpenAPI` && payload extracted | `azd ai agent invoke --local '<extracted-payload>'` |
| `Protocol == "invocations"` | `azd ai agent invoke --local '{"message": "Hello!"}'` |
| `Protocol == "responses"` or unknown | `azd ai agent invoke --local "Hello!"` |

If OpenAPI fetch failed (404, timeout), append a Tip line:
```
Tip:  curl http://localhost:<port>/invocations/docs/openapi.json
      to verify the exact payload format your agent expects.
```

### `invoke --local` (success)

Single agent: `azd deploy` only. (The local invoke just succeeded — `monitor --follow` is hosted-only and not useful here; the natural next step is to ship.)
Multi-agent: `azd deploy` only (one line per agent for follow-up `invoke <name>`).

### `invoke` (remote)

Success: `azd ai agent show <agent>`, secondary `azd ai agent monitor --follow`.
Failure: `azd ai agent monitor --follow`.

### `show` (success)

| `AgentStatus` | Primary |
|---|---|
| `active` / `idle` | `azd ai agent invoke <agent> 'Hello!'` |
| `failed` / `""` | `azd ai agent monitor --follow` |
| transitional | `azd ai agent show <agent>` (re-check) |

### `deploy` (post-deploy hook, embedded in artifact note)

Success, single agent:
```
azd ai agent show <agent>              -- verify it's running
azd ai agent invoke '<payload>'        -- test the deployment
See <relpath>/README.md for a sample payload appropriate for this agent.
```

Notes:
- The `invoke` line **omits the agent name** for single-agent projects (matches sample README convention).
- README hint is emitted **only if** `os.Stat` succeeds on `<root>/<svc.RelativePath>/{README.md,readme.md,README.MD}`. No hint for projects without a README.
- **No live OpenAPI probe at deploy time.** The just-deployed agent may not be reachable yet (this is exactly what doctor check 11 observes). `ResolveAfterDeploy` therefore uses any cached spec produced by a prior local `run` (same disk cache as `fetchOpenAPISpec`), falling back to the README pointer, then to the protocol-generic `<payload>` literal. The deployed endpoint's payload can be verified later via `azd ai agent show` or the next-step block from a successful remote `invoke`.

Multi-agent: one `show <name>` and one `invoke <name>` line per agent.

### `doctor`

Special: runs every check unconditionally, then calls `ResolveAfterInit` (or a deploy-aware variant when `IsDeployed`) for the trailing Next: block. Same code path as success-path resolvers.

## Output Format

```
Next:  <primary command>          -- <description>
       <secondary command>        -- <description>
```

Ground rules:
- One primary, ≤1 secondary. More than two lines drowns out command output.
- Two-space gap between command and `--`.
- Multi-agent: repeat per agent on separate lines, no menus.
- Leading blank line separates from preceding command output.
- README/docs pointers go on their own line below the commands.
- Always present `Next:` prefix so the block is visually distinct.

### Output discipline

| Scenario | Destination |
|---|---|
| Interactive TTY | `stdout` |
| Piped output / CI (detected via `isTerminal`) | `stderr` so it doesn't pollute parsable output |
| `--output json` (success-path commands) | `Next:` block suppressed entirely |
| `azd ai agent doctor --output json` | **Not** the suppression rule — emits the structured check-result array (see [Exit codes & JSON output](#exit-codes--json-output)). Human-readable `Next:` lines are still suppressed; redacted/non-redacted RBAC details appear there with an explicit `redacted` flag |
| Inside `azd deploy` | Attached to the returned `azdext.Artifact` (`Metadata["note"]` on an `ArtifactKindEndpoint` artifact — same path used today for endpoint URLs in `service_target_agent.go`) |

The `isTerminal` check uses the existing helper in `internal/cmd/helpers.go` (which wraps `golang.org/x/term.IsTerminal`). All other extension code that needs to detect interactive vs. non-interactive output should call the same helper to keep the source of truth singular.

### Multi-line note pre-indentation

The `azdext.Artifact` SDK field used here renders only the **first line** at the natural indent level; subsequent lines need to be pre-indented by the producer to align under the artifact bullet. `FormatNextForNote` pre-indents lines 2+ with 4 spaces and trims leading/trailing newlines. This is purely a string-formatting concern handled inside the extension.

## Hook Points

| Command | Trigger | Mechanism |
|---|---|---|
| `init` | end of `runE` after success message | direct `nextstep.PrintNext(w, …)` |
| `run` | "agent is listening" callback | direct print before `Press Ctrl+C` line |
| `invoke --local` / `invoke` | end of `runE` on success | direct print |
| `show` | end of table render | direct print |
| `doctor` | after rendering check report | direct print |
| `deploy` | inside `Deploy()` return path | populate `Metadata["note"]` on the existing `ArtifactKindEndpoint` artifact returned by `service_target_agent.go` (~line 720). `pkg/project/artifact.go` (`ToString`, ~line 128) renders the value as a continuation line under the artifact bullet, gated on `Kind == ArtifactKindEndpoint`. We do **not** introduce a new artifact kind. |

The deploy hook returns its Next: block through the same `azdext.Artifact` SDK field that `service_target_agent.go` already uses for endpoint URLs. No new APIs, no edits outside the extension. Everywhere else, the extension owns its terminal directly.

### Why not a `listen.go` event handler for deploy?

The listen daemon's stdout is consumed by core's gRPC channel — `fmt.Printf` from a `postdeployHandler` never reaches the user terminal. Confirmed during a previous spike. The artifact-note path is the only reliable surface.

## OpenAPI Discovery

Reuses `fetchOpenAPISpec` from `helpers.go`. Adds one helper in `nextstep/openapi.go`:

```go
// ExtractInvokeExample returns a JSON-quoted payload string from the spec's
// /invocations endpoint (preferring `example`, falling back to a minimal object
// derived from `schema.required`). Returns "" if the spec is unusable.
func ExtractInvokeExample(spec []byte) string
```

Resolution order:
1. `paths./invocations.post.requestBody.content.application/json.example` — exact author intent.
2. `…schema.example` — schema-level example.
3. Generate from `schema.required` + `schema.properties[*].example` — minimum viable JSON.
4. Return `""` → fall back to protocol-generic payload + Tip line.

All failures are silent. The fetch itself is best-effort and already cached — we never block `run`'s startup notice on it.

**Silent-mode requirement.** Today `fetchOpenAPISpec` prints `OpenAPI spec saved to <path>` to stdout (see `helpers.go:373`). When invoked from the resolver path, that print is unwanted noise. The extraction path will either gate the print behind a `verbose` flag on the helper, or wrap the call in a `silent: true` variant. This is an extension-internal change to existing extension code — still no edits outside the extension.

**Out of scope: OpenAPI `$ref` resolution.** The extractor walks the spec object tree literally. If `requestBody` or `schema` uses `$ref: "#/components/..."`, the extractor returns `""` and the resolver falls back to the protocol-generic payload and Tip line. Cross-document `$ref` resolution adds a JSON-pointer dependency we don't want in the success-path. Authors who hit this can either inline the example or rely on the README-pointer line.

## `doctor` Command

`doctor` is the persistence layer for state-aware guidance: when the user has lost terminal context or hit a confusing error, they run one command and get the full picture plus the specific fix for each broken thing.

### Checks (run in order, top to bottom)

| # | Check | Pass | Fail → Fix |
|---|---|---|---|
| 1 | `azd` reachable + extension gRPC channel healthy | "extension running, gRPC channel established" | install / start daemon |
| 2 | `azure.yaml` valid | "1 service: <names>" | `azd ai agent init` |
| 3 | azd environment selected | "<env-name>" | `azd env new` / `azd env select` |
| 4 | Agent service in `azure.yaml` | service count + names | `azd ai agent init` |
| 5 | `AZURE_AI_PROJECT_ENDPOINT` set | URL value | `azd provision` |
| 6 | `agent.yaml` per service is valid (placeholder for unified-schema service stanza) | path | fix YAML |
| 7 (post-MVP) | Authentication via `azidentity.NewAzureDeveloperCLICredential` (the credential `agent_context.go` already uses) | signed-in user + token validity | `azd auth login` |
| 8 (post-MVP) | Foundry project reachable | endpoint probe OK | network/firewall |
| 9 (post-MVP) | Model deployments exist | name + version | `azd provision` |
| 10 (post-MVP) | User RBAC sufficient | role list | `az role assignment create …` |
| 11 (post-MVP) | Agent status (if deployed) | `active (vN)` | `azd ai agent monitor --follow` |
| 12 (post-MVP) | Agent identity role assignments | roles on project / account / RG | `az role assignment create --assignee <mi-id> …` |

Checks 7–12 are listed in the issue (and surfaced in review feedback) but pulled into a follow-up to keep the MVP shippable. Checks 1–6 are pure local reads; 7–12 require Foundry control-plane calls and RBAC introspection that need their own design pass — see [`azd-ai-agent-doctor-remote-checks.md`](./azd-ai-agent-doctor-remote-checks.md).

### Doctor output shape

```
azd ai agent doctor
  ✓ PASS  <check name>
          <one-line detail>
  ✗ FAIL  <check name>
          <one-line detail>
          fix:  <command or instruction>

Next:  <resolved next-step block, same format as elsewhere>
```

When all checks pass, the trailing Next: block is the resolver's `ResolveAfterInit` (if not deployed) or a deploy-aware variant (if deployed) — exactly what the user would have seen at the end of the last successful command.

### Exit codes & JSON output

| Outcome | Exit code |
|---|---|
| All checks pass (or pass+skip with no fail) — and at least one check ran | 0 |
| Any check returns `Status: Fail` | 1 |
| All checks ran were skipped (e.g., full sweep against a project missing prerequisites, or `--local-only` with all local prereqs short-circuited) | 2 |

Precedence: any `Fail` always wins (exit 1). Skip-only without any pass is the dependency-cascade case (exit 2). All-pass or pass+skip with at least one pass is exit 0.

`azd ai agent doctor --output json` emits a structured array, one entry per check, in execution order:

```json
{
  "schemaVersion": "1.0",
  "remote": false,
  "redacted": true,
  "checks": [
    {
      "id": "local.azure-yaml",
      "title": "Project loaded from azure.yaml",
      "status": "pass",
      "detail": "1 service: echo-agent",
      "fix": "",
      "links": [],
      "durationMs": 4
    },
    {
      "id": "remote.rbac",
      "title": "RBAC",
      "status": "fail",
      "detail": "principal <redacted> has no role on project scope",
      "fix": "az role assignment create --role \"Azure AI Developer\" --assignee <redacted> --scope <redacted>",
      "links": ["https://learn.microsoft.com/azure/ai-studio/concepts/rbac"],
      "durationMs": 412
    }
  ]
}
```

The `redacted` field is `true` when `--output json` is combined with non-interactive output (the default for piped stdout). Setting `--unredacted` (interactive only) populates principal IDs, scope ARNs, and full UPNs verbatim. The human-readable `Next:` block is **not** included in the JSON envelope — that is the success-path output discipline; doctor's structured emit is its own contract.

## Testing Strategy

| Layer | What | How |
|---|---|---|
| `nextstep/format` | indentation, blank-line rules, secondary suppression, empty-suggestions early return | table-driven unit tests |
| `nextstep/format` | TTY vs piped routing (stdout vs stderr); `--output json` suppression of the human `Next:` block | snapshot tests with stub `isTerminal` and a fake `OutputFormat` flag |
| `nextstep/resolver` | every branch of every per-command tree | table-driven, fake `State` |
| `nextstep/resolver` | non-interactive auth handling — when `IsAuthenticated == AuthUnknown`, no auth-conditional advice (no "azd auth login" appears in `Next:`) | dedicated table cases |
| `nextstep/openapi` | each extraction path + corruption / empty fallback / unresolved `$ref` → `""` | golden specs in `testdata/` |
| `nextstep/state` | layered source priority, partial state, `WithAuthProbe` opt-in | mocked `azdClient` (testify/mock) |
| `service_target_agent` | with/without README on disk → with/without hint line; cached spec present vs absent → payload vs `<payload>` literal | `t.TempDir` + `t.Chdir` |
| `doctor/runner` | each check pass/fail rendering | mocked `azdClient` |
| `doctor/runner` | **skip-cascade**: for each dependency edge, assert the dependent check returns `Status: Skip` with the expected detail when its dependency returns `Status: Fail` | table-driven over the dependency matrix |
| `doctor/runner` | exit-code precedence (fail wins; all-skip = 2; otherwise 0) and JSON envelope shape (schema version, `redacted` flag, per-check fields) | table-driven + snapshot |

All tests run under `go test -short -timeout 180s ./internal/...`. No live Azure calls.

## Backward Compatibility

The Next: block is purely additive; no existing exit code, error type, or stdout format changes. Specifically:

- Success-path JSON output (`--output json` on `init`/`run`/`invoke`/`show`) is unchanged — the human `Next:` block is suppressed in that mode (see [Output discipline](#output-discipline)).
- The deploy hook adds a `Metadata["note"]` value to artifacts that already exist; the artifact `Kind` stays `ArtifactKindEndpoint`. Tools parsing artifacts by kind see no change.
- `azd ai agent doctor` is a new command — its addition cannot break existing scripts. The new `--local-only` and `--output json` flags are opt-in.
- `nextstep` and `doctor` are new internal packages under the extension. No public Go API surface is added.
- Existing extension-side env vars and behaviors (e.g., `AZD_AGENT_NO_NEXTSTEPS`, if shipped — see Open Questions) are documented in `cli/azd/docs/environment-variables.md` per repo convention.

If any future change to the `Metadata["note"]` rendering behavior in `pkg/project/artifact.go` ships, the regression test pinned in this design's [Output Discipline](#output-discipline) section will break loudly. The fix is extension-side reformatting; no core edits.

## Security & PII / Telemetry

**Output redaction.** Doctor's RBAC check (10) and any future check that surfaces principal IDs or resource ARNs follows two rules:

- **Interactive TTY:** full values are shown — they are necessary for the user to construct the fix command (`az role assignment create ...`).
- **Non-interactive (piped, CI, `--output json`):** principal IDs, scope ARNs, and full UPNs are replaced with `<redacted>` in `detail` and `fix` strings. The structured JSON envelope sets `"redacted": true` so callers can see they're getting the safe variant. An interactive-only `--unredacted` flag exists for the rare case a user needs the raw values in a JSON pipeline.

**Source of truth for `isTerminal`.** All redaction and stdout/stderr routing decisions are gated by the existing `isTerminal` helper in `internal/cmd/helpers.go` (which wraps `golang.org/x/term.IsTerminal`). New code must call the same helper, never re-implement detection — this keeps the test surface singular and the redaction contract auditable.

**Logging.** The `nextstep` package logs only via the standard `log` package, which the extension already silences in production via `setupDebugLogging` (see `internal/cmd/debug.go`). Sensitive values (principal IDs, tokens, full ARNs) are logged at `debug` level only; the resolver's user-facing output never logs.

**Telemetry (deferred).** A full instrumentation plan is out of scope for this design. Initial implementation will emit a single counter event per resolver invocation (`nextstep.resolved` with `command` + `branch_id` attributes) and per-doctor-check (`doctor.check.completed` with `id` + `status` + `duration_ms`). Neither event includes user input, principal IDs, or environment values. A follow-up design will cover the full schema once we have implementation experience.

## Deferred follow-ups

The reviewer surfaced three additional concerns that are out of scope for this design pass and will be handled separately:

- **User personas / target audiences.** Not standard in design docs in this repo (see `cli/azd/docs/design/`); we treat "developers building with the agents extension" as the implicit audience.
- **Success metrics / KPIs.** Same rationale; metrics will be wired up alongside telemetry in the follow-up design once we have something concrete to measure.
- **Full telemetry / observability plan.** See the brief sketch above; the complete attribute schema, sampling policy, and dashboards are deferred.

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Resolver guidance contradicts what the command actually did (false advice) | Each resolver receives the *post-execution* state. Tests cover deployed / not-deployed / partial-env permutations. |
| OpenAPI fetch slows down `run` startup | Already capped + cached + silent on failure. We do not block the listening notice on the fetch. |
| Output noise: every command grows by 3 lines | One primary + ≤1 secondary; suppressed under `--output json` and on piped stderr; user can ignore. |
| Decision tree drifts from sample reality | All trees are table-driven and unit-tested per branch. Sample contract changes go through the same review. |
| Artifact-note rendering behavior could shift in a future SDK update | Pin the indentation contract with an extension-side regression test that constructs an artifact and asserts the formatted string. If the SDK changes shape, the test breaks loudly and we adapt formatting — still no edits required outside the extension. |
| Multi-agent projects produce wall-of-text Next: blocks | `ResolveAfterDeploy` produces one `show <name>` and one `invoke <name>` line per agent. Multi-agent walls remain readable for the project sizes we expect (≤ ~10). If the wall becomes a real problem in practice, fall back to a single `azd ai agent show` (no args, lists all) line; this can be tuned in a follow-up without changing the resolver contract. |

## Implementation Phases

1. **Foundation** — `nextstep` package (types, format, state assembly, OpenAPI helper). No callers.
2. **Wire success paths** — `init`, `run`, `invoke`, `show`. Resolver per command.
3. **Deploy hook** — attach Next: block to the returned `azdext.Artifact` (same SDK field already used for endpoint URLs in `service_target_agent.go`). README-on-disk verification.
4. **`doctor` command** — checks 1–6 (local-only). Wire trailing Next: through resolver.
5. **(Follow-up)** Doctor checks 7–11 (auth, reachability, RBAC, deployments, agent status). See [`azd-ai-agent-doctor-remote-checks.md`](./azd-ai-agent-doctor-remote-checks.md).

Each phase is independently shippable and reviewable. Phases 1–4 are the MVP for #7975.

## Appendix A: Example Outputs (full block, end-to-end)

### After `init`, fresh project, deps not provisioned

```
✓ Agent project initialized.

Next:  azd provision  -- set up your Foundry project, models, and connections

This creates your Foundry project and any model deployments, toolboxes,
or connections your agent needs for local development.

Once that finishes, run 'azd ai agent run' to start locally.
When ready to deploy to Azure, run 'azd deploy'.
```

### After `run`, invocations protocol, with OpenAPI

```
Agent is running at http://localhost:8088

Next:  azd ai agent invoke --local '{"message": "What hotels are in Seattle?"}'

Press Ctrl+C to stop.
```

### After `azd deploy` (rendered by core from artifact note)

```
  (✓) Done: Deploying service ag-ui-invocations
  - Agent playground (portal):     https://ai.azure.com/...
  - Agent endpoint (invocations):  https://....services.ai.azure.com/...
    Next:  azd ai agent show ag-ui-invocations    -- verify it's running
           azd ai agent invoke '<payload>'        -- test the deployment
    See src/ag-ui-invocations/README.md for a sample payload appropriate for this agent.
```

### After `azd ai agent doctor`, project deployed and healthy

```
azd ai agent doctor
  ✓ PASS  azd CLI is installed and reachable
          extension running, gRPC channel established
  ✓ PASS  Project loaded from azure.yaml
  ✓ PASS  Current azd environment selected
  ✓ PASS  Agent service detected in azure.yaml
  ✓ PASS  AZURE_AI_PROJECT_ENDPOINT is set
  ✓ PASS  agent.yaml for service "echo-agent" is valid

Next:  azd ai agent show echo-agent              -- verify it's running
       azd ai agent invoke '<payload>'           -- test the deployment
       See src/echo-agent/README.md for a sample payload appropriate for this agent.
```

## Appendix B: References

- Issue [#7975](https://github.com/Azure/azure-dev/issues/7975)
- Existing OpenAPI fetch: [`helpers.go#L296`](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/internal/cmd/helpers.go#L296)
- Existing init suggestion: [`init.go#L1718-L1733`](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/internal/cmd/init.go#L1718-L1733)
- Existing endpoint artifact emission (same SDK field reused for the Next: block): [`service_target_agent.go`](../../extensions/azure.ai.agents/internal/project/service_target_agent.go)
- `exterrors` package (parallel pattern for error paths): [`internal/exterrors`](../../extensions/azure.ai.agents/internal/exterrors)
- Reference Foundry sample (OpenAPI opt-in): [`microsoft-foundry/foundry-samples` human-in-the-loop](https://github.com/microsoft-foundry/foundry-samples/blob/fd4ecab832fa491b9ffb4abca862c073777b5e53/samples/python/hosted-agents/bring-your-own/invocations/human-in-the-loop/main.py#L119)
