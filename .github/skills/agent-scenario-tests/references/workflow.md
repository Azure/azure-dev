# Workflow

### Step 1 — Verify prerequisites

Run the checks in `prerequisites.md`. If a hard prerequisite is missing, stop with a clear
message. Don't auto-install or work around a missing MCP server or profile.

### Step 1b — Rebuild WSL binaries (Windows only)

Before running any scenarios, rebuild the native Linux `azd` and extension binaries from the
current repo source so the tester always exercises the latest local code. Execute
`setup-wsl.sh` inside WSL via the tester:

```text
start_session(command="bash /mnt/c/<path-to-scenarios>/setup-wsl.sh",
              cwd="/mnt/c/<path-to-repo>/cli/azd/extensions/azure.ai.agents",
              session_id="setup-wsl-<timestamp>",
              run_name="setup-wsl")
```

Wait for it to print "Done. WSL is ready for scenario testing." and then `finish_session`.
If the build fails, stop and report the build error — do not proceed with stale binaries.

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
   list_scenarios(root="<scenarios-dir>", tags=[<cmd:* tags>, ...])
   ```

   `list_scenarios` filtering is **OR across tags, case-sensitive, exact match**.

### Step 4 — Confirm the plan (cost gate)

Show the user the concrete scenario list grouped by tier, plus estimated cost/auth needs,
and confirm via `ask_user` before running:

- Always list the Tier 0 scenarios that will run (free).
- If the set includes **Tier 1**, confirm `az login` is done.
- If the set includes **Tier 2**, require an **explicit cost acknowledgement** ("Tier 2
  provisions real Azure resources and incurs cost — proceed?"). If the user declines Tier 2,
  drop it and run only Tier 0/1.

Pick one `<run-timestamp>` of the form `YYYYMMDD-HHMMSS` for the whole run. All artifacts go
under `<scenarios-dir>/.reports/<run-timestamp>/`.

### Step 5 — Run the scenarios

Drive each selected scenario per `running-scenarios.md`. Honor ordering:

- **Tier 0 / Tier 1** are `parallel-safe` — they may be run concurrently (small waves), each
  with its own `cwd` (no `instance_id` needed for distinct scenarios).
- **Tier 2** is `serial-only` and order-dependent: `20-setup-deploy-shared-agent` **first**,
  then the targeted `21-…2D-` scenarios **serially**, then `2Z-teardown-down` **last**.

Record per scenario: PASS/FAIL, wall-clock duration (`Hh Mm Ss`), and any `report_finding`
entries.

### Step 6 — Report

Aggregate results into `.reports/<run-timestamp>/FINAL-REPORT.md` and post a PR comment per
`reporting.md`. If a Tier 2 run started but was interrupted before `2Z-teardown`, run
`2Z-teardown-down` (or `20-setup`'s down hook) so no resources are orphaned, then report.

### Step 7 — Stop conditions

Stop and escalate to the user when:

- a required prerequisite is missing (Step 1),
- the diff touches a changed command with **no** scenario coverage (note it in the report so
  the user can author one — see the extension `AGENTS.md` guidance), or
- a scenario fails in a way that looks like a real product regression: report it as a FAIL
  with the finding and do **not** edit the scenario to make it pass.
