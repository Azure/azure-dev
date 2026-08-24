# Workflow

### Step 1 — Verify prerequisites

Run the checks in `prerequisites.md`. If a hard prerequisite is missing, stop with a clear
message. Don't auto-install or work around a missing MCP server or profile.

### Step 1b — Build and verify the `azd` binary (mandatory gate)

Before running **any** scenarios, the orchestrator must ensure a working native Linux `azd`
dev build is available. The exact steps depend on the host OS:

#### Windows (WSL)

On Windows, scenarios run inside WSL where the default `azd` may resolve to Windows
`azd.exe` via interop — which causes file-locking failures on UNC paths (`\\wsl.localhost\…`).
**This step is mandatory and must not be skipped.** Execute `setup-wsl.sh` inside WSL via
the tester:

```text
start_session(command="bash /mnt/c/<path-to-scenarios>/setup-wsl.sh",
              cwd="/mnt/c/<path-to-repo>/cli/azd/extensions/azure.ai.agents",
              session_id="setup-wsl-<timestamp>",
              run_name="setup-wsl")
```

`setup-wsl.sh` owns the WSL build toolchain: it reads the exact Go version from
`cli/azd/go.mod` and .NET SDK version from the scenario suite's `dotnet-sdk.version`, installing
the official versions under `/usr/local/go` and `/usr/local/dotnet` when absent or mismatched.
The downloads are verified against the SHA-256/SHA-512 values from their official release
metadata. This authorized bootstrap happens before scenario execution and is not a scenario
workaround.

Wait for it to print "Done. WSL is ready for scenario testing." and then `finish_session`.
If Go or .NET cannot be downloaded, verified, or installed, or if the build fails, stop and
report the gate error — do not proceed with stale binaries and do not ask a scenario worker to
repair the environment.

After `setup-wsl.sh` succeeds, **verify** the installation by starting a quick tester session
and running `which azd && azd version && dotnet --version`. Confirm that:
1. `which azd` returns `/usr/local/bin/azd` (not `/mnt/c/…` or a path ending in `azd.exe`)
2. `azd version` output contains the expected dev version string (e.g. `0.0.0-dev.0`)
3. `dotnet --version` equals the exact SDK from `dotnet-sdk.version`

Record the verified version string for the report. If any check fails, stop — do NOT
proceed to Step 5.

#### Native Linux / macOS

On native Linux or macOS, `setup-wsl.sh` does not apply. The user builds and installs `azd`
from source using their normal workflow (e.g. `go install`, `make`, or equivalent). Before
proceeding, verify that `azd version` returns the expected dev build version. If it does not,
stop and ask the user to build and install the correct version.

#### Hard gate

**If Step 1b is skipped or verification fails, do NOT proceed to Step 5.** No scenarios may
run until the `azd` binary is verified. This is not optional — running scenarios against
the wrong binary produces unreliable results and wastes time and cost.

### Step 2 — Resolve the PR

```bash
gh pr view --json number,url,headRefName,baseRefName,title
```

- **PR found:** capture `number`, `url`, and `baseRefName` (the merge base for the diff).
- **No PR for the current branch:** ask the user via `ask_user` whether to (a) supply a PR
  number/URL, (b) run against the local diff vs `origin/main` without posting a comment, or
  (c) abort.

### Step 3 — Compute the impacted scenario tag set

1. Get the changed files:

   ```bash
   gh pr diff <number> --name-only      # when a PR exists
   # or, for a local-only run:
   git diff --name-only origin/main...HEAD
   ```

2. Map those files to scenario tags using `impact-mapping.md`. The result is:
   - a set of `cmd:*` tags (which commands changed),
   - the **highest tier** you should offer (cost gating), and
   - any **coverage gaps** (changed commands that have *no* scenario yet — e.g. `mcp`).
     Surface gaps to the user; do not silently skip them.

3. Enumerate matching scenarios via the tester:

   ```text
   list_scenarios(root="<scenarios-dir>", tags=[<cmd:* tags>])
   ```

   `list_scenarios` filtering is **OR across tags, case-sensitive, exact match**. For
   command-scoped coverage, query with the derived `cmd:*` tags only and then filter the
   returned rows locally to the tiers approved in Step 4. Never add tier tags to the same
   query to simulate an AND filter. Broad whole-tier coverage is a separate tier-only query.

4. Inspect every retained scenario's `requires:` field and recursively add its referenced
   prerequisites to the concrete plan, deduplicating paths. If a referenced scenario does not
   exist, stop with a plan-construction error rather than scheduling a dependent that can only
   be skipped.

5. If the dependency-closed result contains **any Tier 2 scenario**, add
   `tier2/2.00-setup-deploy-shared-agent.yaml` and
   `tier2/2.99-teardown-down.yaml` to the concrete plan, deduplicating them if already
   selected. This lifecycle closure happens before cost confirmation so the user sees and
   approves the exact set that will create and tear down resources.

6. Read and retain each selected scenario's optional top-level `produces` value so it can be
   passed directly to that scenario's worker. The tester's `load_scenario` summary does not
   expose orchestrator-only fields.

### Step 4 — Confirm the plan (cost gate)

Show the user the concrete scenario list grouped by tier, plus estimated cost/auth needs,
and confirm via `ask_user` before running:

- Always list the Tier 0 scenarios that will run (free).
- If the set includes **Tier 1**, confirm `az login` is done.
- If the set includes **Tier 1b** or **Tier 2**, require an **explicit cost acknowledgement**
  ("Tier 1b/2 provisions real Azure resources and incurs cost — proceed?"). If the user
  declines, drop cost-incurring tiers and prerequisites added solely for those dropped
  dependents; retain Tier 0/1 scenarios selected independently by the impact mapping.

Generate one `<run-id>` per `prerequisites.md` and reuse it for the whole run. All artifacts go
under `<scenarios-dir>/.reports/<run-id>/`.

### Step 5 — Run the scenarios

Drive each selected scenario per the executor spec
`cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios/driving-mechanics.md`
— use one **foundry-extension-scenario-worker** per scenario phase. Start with a mandatory
validation step, then schedule by readiness:

1. **Recipe validation (mandatory).** Run one Tier 0 scenario synchronously before fanning
   out. Pick a fast, non-interactive scenario (e.g. `0.01-version`). If it fails with an
   infrastructure error (file-locking, wrong binary, missing tool), **stop the entire run** —
   do not fan out into a fleet of failures. Fix the environment issue (re-run Step 1b on
   Windows, rebuild on native Linux) and start over.

2. **Adaptive rolling pool.** Choose a safe target parallelism from the selected workload,
   available model/tool capacity, and Azure side effects. Launch ready Tier 0 / Tier 1 /
   Tier 1b scenarios up to that capacity. As each worker completes, immediately process its
   result and refill available capacity without waiting for unrelated workers. Give Tier 0 /
   Tier 1 workers the scenario-specific `instance` / `instance_id` derived from the run ID.
3. **Tier 1b** (`verify-deploy`) is `parallel-safe` but depends only on its declared Tier 1
   producer. Make it ready immediately when that producer PASSES; do not wait for all Tier 1
   scenarios. Reuse the producer's exact instance ID. Its PASS must include a verified absolute
   `scaffold_dir`; add that exact value to the dependent's `session_vars` as
   `prerequisite_scaffold_dir`. Never reconstruct the path from scenario or agent naming. A
   producer failure or PASS without `scaffold_dir` skips only its dependent. Tier 1b requires
   cost acknowledgement (same as Tier 2) since it provisions Azure resources.
4. **Tier 2** is an independent `serial-only` lane that starts after recipe validation even
   while other tiers are running. Never run more than one Tier 2 scenario:
   `2.00-setup-deploy-shared-agent` **first**, then the targeted `2.01-`…`2.18-` scenarios
   **serially**, then `2.99-teardown-down` **last**. Setup must PASS before functional
   scenarios run; otherwise skip them and proceed to cleanup/recovery. Each functional
   completion unlocks only its successor, and teardown follows the final attempted functional
   scenario regardless of verdict.
5. **Defer Tier 1b cleanup until a global product barrier.** Before launching each Tier 1b
   product worker, register its cleanup identity in
   `.reports/<run-id>/CLEANUP-STATUS.md`. Product workers must call `finish_session` but leave
   Tier 1b post hooks pending. After every product worker and the full Tier 2 lane (including
   teardown) finish, run pending Tier 1b post hooks serially through cleanup-mode workers. Keep
   draining after failures and merge each cleanup result into its scenario's final verdict.

Record per scenario: PASS/FAIL, active duration (`Hh Mm Ss`, product plus cleanup but excluding
cleanup-queue wait), and any product or cleanup findings.

### Step 6 — Report

Aggregate results into `.reports/<run-id>/FINAL-REPORT.md` and post a PR comment per
`reporting.md`. If a Tier 2 run started but was interrupted before `2.99-teardown`, run
`2.99-teardown-down` (or `2.00-setup`'s down hook) so no resources are orphaned, then report.

### Step 7 — Stop conditions

Stop and escalate to the user when:

- a required prerequisite is missing (Step 1),
- the diff touches a changed command with **no** scenario coverage (note it in the report so
  the user can author one — see the extension `AGENTS.md` guidance), or
- a scenario fails in a way that looks like a real product regression: report it as a FAIL
  with the finding and do **not** edit the scenario to make it pass.
