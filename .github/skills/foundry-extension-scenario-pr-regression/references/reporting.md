# Reporting

Two outputs: a local `FINAL-REPORT.md` artifact and a PR comment.

## FINAL-REPORT.md

Write to `<scenarios-dir>/.reports/<run-id>/FINAL-REPORT.md` (the `.reports/` tree is
git-ignored). Include:

- Run header: run ID, PR number/URL, branch, base ref, the derived tag set, and the
  tiers actually run.
- A per-tier table of scenarios with columns: `Scenario | Tier | Result | Duration | Findings`.
- A short "Coverage gaps" section listing any changed command(s) with no scenario (from
  `impact-mapping.md` §2), so the author knows to add one.
- Links to the per-scenario `tester-reports/<run_name>/` folders for screenshots/HTML.

## PR comment

Post with `gh pr comment <number> --body-file <path>` (use a temp file to preserve
formatting). Keep it scannable — full detail lives in the artifact. Suggested shape:

```markdown
## 🧪 Agent scenario regression check

**Branch:** `<headRef>` → `<baseRef>` · **Run:** `<run-id>`
**Impacted tags:** `cmd:init`, `cmd:invoke` · **Tiers run:** 0, 1, 1b, 2

| Scenario | Tier | Result | Duration |
| --- | --- | --- | --- |
| 0.01-version | 0 | ✅ PASS | 4s |
| 1.01-init-template-python | 1 | ✅ PASS | 22s |
| 1b.01-deploy-template-python | 1b | ✅ PASS | 3m 45s |
| 1b.02-deploy-template-dotnet | 1b | ⏭️ SKIPPED | — |
| 2.03-invoke-remote | 2 | ✅ PASS | 1m 12s |
| 2.04-invoke-new-session | 2 | ❌ FAIL | 1m 40s |

**Findings**
- `1b.02-deploy-template-dotnet`: ⏭️ SKIPPED — prerequisite `tier1/1.02-init-template-dotnet.yaml` failed.
- `2.04-invoke-new-session`: `--new-conversation` still recalled the prior name — memory
  was not reset. (screenshot: …)

**Coverage gaps:** this PR also touches `mcp.go`, which has no scenario — consider adding one.

<sub>Run locally via the `foundry-extension-scenario-pr-regression` skill (or the `foundry-extension-scenario-orchestrator` agent). Not run in CI.</sub>
```

Rules:

- Use ✅ PASS / ❌ FAIL / ⏭️ SKIPPED (and ⚠️ for a scenario that completed but raised a
  non-fatal finding).
- **SKIPPED** means the scenario's `requires:` prerequisite did not PASS — include the reason
  in the Findings section (e.g. "prerequisite `tier1/1.01-…` failed"). SKIPPED scenarios
  have no duration (`—`).
- **Never** soften a real regression to make the table green. A scenario that failed because
  of the PR's change is a FAIL — report it and recommend fixing the code, not the scenario.
- If the user opted out of posting (or there's no PR), write only the artifact and print the
  summary to the user instead.
- Mention any Tier 2 teardown status explicitly (e.g. "`2.99-teardown-down` ran — no resources
  left provisioned") so the reader knows nothing is still costing money.
