# CI Pipeline Prompt — E2E Test Scenarios

Autonomously run azure.ai.agents CLI test scenarios using the cli-interactive-tester MCP tool. Do NOT ask questions — make all decisions yourself and run to completion.

## Environment

- Runner: Ubuntu 22.04 (GitHub Actions)
- azd is on PATH (pre-built in earlier step)
- Azure auth: active if TIER includes 1 or 2 (federated identity via earlier step)
- GitHub CLI auth: active if TIER includes 1 or 2 (earlier step)
- Scenarios directory: current working directory
- All paths are POSIX (Linux runner)

## Profile Setup

Read and merge (local overrides shared):
1. `./profile.yaml`
2. `./profile.local.yaml`

Derive `shared_agent_name = "{prefix}-{shared_agent_suffix}"`. Pass merged map as `session_vars` on every MCP call.

## Tier Selection

Check the environment variable `TIER` to determine which tiers to run:
- `TIER=0` → run Phase 0 only
- `TIER=0+1` → run Phase 0 + Phase 1
- `TIER=0+1+2` → run all phases

If `TIER` is not set, default to `0`.

## Run Plan

Record the START TIME at the beginning.

### Phase 0 — Tier 0 (offline, no auth, fast)

Run ALL `00-*.yaml` scenarios sequentially:
1. `00-version.yaml`
2. `00-help-root.yaml`
3. `00-sample-list-text.yaml`
4. `00-sample-list-json-filters.yaml`
5. `00-doctor-empty-dir.yaml`
6. `00-doctor-local-only.yaml`
7. `00-doctor-partial-failure.yaml`
8. `00-init-validate-mutually-exclusive.yaml`
9. `00-init-validate-no-prompt-missing.yaml`
10. `00-invoke-validate-protocol.yaml`
11. `00-eval-context-required.yaml`
12. `00-optimize-apply-requires-candidate.yaml`
13. `00-delete-help.yaml`
14. `00-endpoint-show-help.yaml`
15. `00-code-download-help.yaml`
16. `00-init-picker-navigation.yaml` — interactive picker UX, abort with Ctrl-C

### Phase 1 — Tier 1 (auth required, scaffold only, NO azd provision)

1. `10-init-template-python.yaml`
2. `10-init-template-dotnet.yaml`
3. `10-init-deploy-mode-code.yaml`
4. `10-init-deploy-mode-container.yaml`
5. `10-init-from-code.yaml`
6. `10-init-from-manifest-url.yaml`
7. `10-init-flags-agent-name-model.yaml`
8. `10-init-validate-deploy-mode.yaml`

### Phase 2 — Tier 2 (real Azure resources, strict order)

1. `20-setup-deploy-shared-agent.yaml` — FIRST
2. `21-show.yaml`
3. `21-show-json.yaml`
4. `22-invoke-remote.yaml`
5. `22-invoke-input-file.yaml`
6. `22-invoke-new-session.yaml`
7. `23-invoke-protocol-invocations.yaml`
8. `23-sessions-lifecycle.yaml`
9. `24-files-lifecycle.yaml`
10. `25-monitor-console.yaml`
11. `25-monitor-system.yaml`
12. `26-endpoint-update.yaml`
13. `27-run-local-and-invoke-local.yaml` — needs two sessions (port allocation)
14. `28-eval-lifecycle.yaml`
15. `29-optimize-submit-and-cancel.yaml`
16. `2A-doctor-provisioned-all-pass.yaml`
17. `2B-endpoint-show.yaml`
18. `2C-code-download.yaml`
19. `2D-delete.yaml`
20. `2Z-teardown-down.yaml` — LAST

## Rules

- Per scenario: `load_scenario` → `run_pre_hooks` (if any) → `start_session` → accomplish goals → `finish_session` → `run_post_hooks` (if any).
- Use `run_name=<scenario-stem>` for each `start_session`.
- Record start/end time for EACH scenario (wall clock).
- If a scenario fails, record FAIL with reason and **continue to next** (do not abort).
- Don't verify/retry after a select — treat select miss as hard failure.
- Prefer `choice_text` over `choice_index`.
- Clear pre-filled text fields before typing (select-all + delete).
- For Tier 2 setup: select Container deploy mode, Python language, Basic Responses template.
- For subscription selection: use the subscription from profile.
- For region: select region from profile.
- For model: select model from profile.

## Output

When all scenarios are done, write a markdown report to `./full-pipeline-run-results.md` with:

1. **Summary table**: total scenarios, PASS/FAIL/SKIP counts per tier
2. **Per-scenario detail**: name, tier, result, duration, notes
3. **Timing**: total wall clock, time per tier, top 5 slowest
4. **Issues**: any bugs, failures, or unexpected behaviors

Record END TIME and total duration.

Start now. Begin with Phase 0.
