# Design: Context-Aware Next-Step Guidance for `azd ai agent`

## Status

**Draft — for design review.**
Tracking issue: [#7975](https://github.com/Azure/azure-dev/issues/7975) — "Context-aware next-step guidance and diagnostics".
Scope: the `azure.ai.agents` extension only. **All code lives under `cli/azd/extensions/azure.ai.agents/`. No files outside the extension are modified.** The deploy hook returns its Next: block via the standard extension SDK return value (`*azdext.Artifact`) — same path other extensions use today.

## Goal

After every successful `azd ai agent` command — and on demand via a new `azd ai agent doctor` — print a short, state-aware **Next:** block telling the developer the single best command to run next. Replace the current static, often-misleading hints with output derived from the project's real state (`azure.yaml`, the azd environment, optionally a running agent's OpenAPI endpoint).

Non-goals:
- Reworking error-path guidance — `internal/exterrors` already does this well.
- Any modification to files outside `cli/azd/extensions/azure.ai.agents/`. State is read via the existing gRPC `azdext` API.
- Sample-author hooks, README markers, or YAML-declared hints (see [Alternatives](#alternatives-considered)).

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
- **`azdext.Artifact` return value** from `Deploy()` — the extension SDK's normal mechanism for an extension to surface lines below its progress bullet. Already used by the existing `service_target_agent.go` to print endpoint URLs. We populate the same field with our Next: block. Indentation rules are documented in [Output Discipline](#output-discipline) and pinned by an extension-side regression test.

## Architecture

### Component layout

```
cli/azd/extensions/azure.ai.agents/internal/cmd/
├── nextstep/                     ← new package (this design)
│   ├── types.go                  ← Suggestion, State, ServiceState
│   ├── state.go                  ← AssembleState(ctx, azdClient) → State
│   ├── resolver.go               ← decision tree per command
│   ├── format.go                 ← PrintNext / FormatNextForNote
│   └── openapi.go                ← OpenAPI-derived invoke examples
├── doctor.go                     ← new command — calls nextstep + extra checks
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
type State struct {
    HasProjectEndpoint     bool
    HasUnresolvedInfraVars bool   // ${...} → Bicep outputs not in azd env
    HasUnresolvedManualVars []string // ${...} → user-supplied vars not set
    Services               []ServiceState
    AgentStatus            string  // optional (Foundry API), empty if unknown
    HasOpenAPI             bool    // run-time only
    OpenAPIPayload         string  // run-time only, ""=no spec
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

State is **assembled fresh on each call**. No singleton, no cache across commands. `azure.yaml` parsing inside one call is cached by `azdClient` already. The cost is one gRPC round-trip per resolver invocation, which is negligible compared to anything the user perceives.

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
| `HasUnresolvedInfraVars` | `azd provision` | — |
| `HasUnresolvedManualVars` (non-empty) | `azd env set <KEY> <value>` (one per missing var, up to 3) | — |
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

Single agent: `azd deploy`, secondary `azd ai agent monitor --follow`.
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
| `--output json` / structured output flag | Suppressed entirely |
| Inside `azd deploy` | Attached to the returned `azdext.Artifact` (same SDK field already used for endpoint URLs) |

### Multi-line note pre-indentation

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
| `deploy` | inside `Deploy()` return path | attach Next: to the returned `azdext.Artifact` |

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

## `doctor` Command

`doctor` is the persistence layer for state-aware guidance: when the user has lost terminal context or hit a confusing error, they run one command and get the full picture plus the specific fix for each broken thing.

### Checks (run in order, top to bottom)

| # | Check | Pass | Fail → Fix |
|---|---|---|---|
| 1 | `azd` reachable + extension running | "extension running, gRPC channel established" | install / start daemon |
| 2 | `azure.yaml` valid | "1 service: <names>" | `azd ai agent init` |
| 3 | azd environment selected | "<env-name>" | `azd env new` / `azd env select` |
| 4 | Agent service in `azure.yaml` | service count + names | `azd ai agent init` |
| 5 | `AZURE_AI_PROJECT_ENDPOINT` set | URL value | `azd provision` |
| 6 | `agent.yaml` per service is valid | path | fix YAML |
| 7 (post-MVP) | Authentication (`az account show`) | signed-in user | `azd auth login` |
| 8 (post-MVP) | Foundry project reachable | endpoint probe OK | network/firewall |
| 9 (post-MVP) | Model deployments exist | name + version | `azd provision` |
| 10 (post-MVP) | RBAC sufficient | role list | `az role assignment create …` |
| 11 (post-MVP) | Agent status (if deployed) | `active (vN)` | `azd ai agent monitor --follow` |

Checks 7–11 are listed in the issue but pulled into a follow-up to keep the MVP shippable. Checks 1–6 are pure local reads; 7–11 require Foundry control-plane calls and RBAC introspection that need their own design pass — see [`azd-ai-agent-doctor-remote-checks.md`](./azd-ai-agent-doctor-remote-checks.md).

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

## Testing Strategy

| Layer | What | How |
|---|---|---|
| `nextstep/format` | indentation, blank-line rules, secondary suppression, empty-suggestions early return | table-driven unit tests |
| `nextstep/resolver` | every branch of every per-command tree | table-driven, fake `State` |
| `nextstep/openapi` | each extraction path + corruption / empty fallback | golden specs in `testdata/` |
| `nextstep/state` | layered source priority, partial state | mocked `azdClient` (testify/mock) |
| `service_target_agent` | with/without README on disk → with/without hint line | `t.TempDir` + `t.Chdir` |
| `doctor` | each check pass/fail rendering | mocked `azdClient` |

All tests run under `go test -short -timeout 180s ./internal/...`. No live Azure calls.

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Resolver guidance contradicts what the command actually did (false advice) | Each resolver receives the *post-execution* state. Tests cover deployed / not-deployed / partial-env permutations. |
| OpenAPI fetch slows down `run` startup | Already capped + cached + silent on failure. We do not block the listening notice on the fetch. |
| Output noise: every command grows by 3 lines | One primary + ≤1 secondary; suppressed under `--output json` and on piped stderr; user can ignore. |
| Decision tree drifts from sample reality | All trees are table-driven and unit-tested per branch. Sample contract changes go through the same review. |
| Artifact-note rendering behavior could shift in a future SDK update | Pin the indentation contract with an extension-side regression test that constructs an artifact and asserts the formatted string. If the SDK changes shape, the test breaks loudly and we adapt formatting — still no edits required outside the extension. |
| Multi-agent projects produce wall-of-text Next: blocks | `ResolveAfterDeploy` cap: 5 agents inline; beyond that, suggest `azd ai agent show` (no args, lists all). |

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
