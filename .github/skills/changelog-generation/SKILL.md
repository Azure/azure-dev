---
name: changelog-generation
description: >-
  **WORKFLOW SKILL** — Generates release changelog entries and bumps versions for azd core
  or azd extensions. Identifies merged PRs since the last release, fetches PR details via
  GitHub MCP, classifies changes, and writes user-facing changelog entries with proper
  formatting, attribution, and spell checking.

  INVOKES: GitHub MCP tools, git CLI, gh CLI, cspell CLI, ask_user.

  USE FOR: generate changelog, update changelog, release notes, changelog for extension,
  changelog for azd core, bump version, prepare release notes, write changelog entries,
  extension release, core release, update CHANGELOG.md.

  DO NOT USE FOR: code review (use code-review), creating PRs (use pull-request),
  deploying releases, publishing extensions to registry.
---

# changelog-generation

**WORKFLOW SKILL** — Generates release changelog entries for azd core or extensions.

INVOKES: GitHub MCP tools, `git` CLI, `gh` CLI, `cspell` CLI, `ask_user`.

## Workflow

### Step 1 — Determine Scope

Auto-detect scope from the current working directory:

1. Check if the cwd is inside a `cli/azd/extensions/<name>/` directory.
   - If **yes** → infer **extension** scope, with `<name>` as the target extension.
   - If **no** → infer **core** scope.
2. Present the detected scope to the user for confirmation via `ask_user`:

   > I detected you're in `<cwd>`, so I'll generate changelog for **[core azd CLI | extension `<name>`]**. Is that correct?

   Choices:
   - **Yes, that's correct** *(Recommended)*
   - **No, switch to [core | an extension]**

3. If **extension** and not auto-detected: list folders under `cli/azd/extensions/` and ask the user to select the target.

4. After extension scope is confirmed (whether auto-detected or manually selected), verify the target extension contains `CHANGELOG.md`, `version.txt`, and `extension.yaml`. If any are missing, stop and list which files are absent.

### Step 2 — Determine Version & Update Files

**Files to update (core):** `cli/azd/CHANGELOG.md`, `cli/version.txt`, `cli/azd/pkg/azdext/version.go`
**Files to update (extension):** `<extension>/CHANGELOG.md`, `<extension>/version.txt`, `<extension>/extension.yaml`

For version derivation rules, see [references/scope-rules.md](references/scope-rules.md) § Version Files.

- **Core**: derive version from the existing unreleased header (strip `-beta.*` and `(Unreleased)`), use today's date. Update `cli/version.txt` and `cli/azd/pkg/azdext/version.go` (`Version` constant) to the released version.
- **Extension**: ask the user for the new version number via `ask_user`. Update both `version.txt` and `extension.yaml` — they must match exactly.

Present the version and date to the user for confirmation before writing any files.

### Step 3 — Find Cutoff & List Commits

Per [references/scope-rules.md](references/scope-rules.md) § Commit Discovery.

1. Inspect changelog git history to find the cutoff commit SHA.
2. List commits from cutoff to origin/main (extensions: scoped to extension path).
3. If >30 commits in the range, display the count and ask the user if they want to see the full list or proceed directly to PR processing.
4. Present the commit list to the user. This is a manual pre-filter opportunity — ask if any commits should be explicitly included or excluded before automated processing.

### Step 4 — Process Each PR

For **each** commit remaining after the user's pre-filter in Step 3, extract its PR number using the rules in `references/pr-processing.md` (each commit maps to exactly one canonical PR). Apply the automated exclusion rules and complete the full sub-workflow before moving to the next. **Do not batch or abbreviate — and do not skip a PR unless an explicit error-handling rule applies and the user confirms skipping.**

Track processed PR numbers. If a PR number was already processed in a previous commit, skip it. If the commit subject starts with `Revert`, skip both the revert commit and note the original PR number to avoid including the reverted change.

Per [references/pr-processing.md](references/pr-processing.md):

1. Extract PR number from commit subject (`(#1234)`) or locate by SHA.
2. Fetch PR details via GitHub MCP (owner: `Azure`, repo: `azure-dev`).
3. Detect external contributors per [references/pr-processing.md](references/pr-processing.md) § External Contributor Detection.
4. Fetch linked issues from the PR body when relevant to understanding user impact.
5. Apply exclusion rules per [references/pr-processing.md](references/pr-processing.md) § Exclusion Rules.
6. **Cross-release deduplication**: before writing an entry, search all existing sections in CHANGELOG.md for the PR number **and** any linked issue numbers from the PR body. If a match is found in a prior release, skip the entry and log a note. Entries must always use PR numbers (`pull/NNNN`), not issue numbers (`issues/NNNN`), to maintain consistency with the `[[#PR]]` format convention.
7. Categorize and write the entry per [references/pr-processing.md](references/pr-processing.md) § Category Classification and § Entry Format.

### Step 5 — Assemble & Review

1. **Remove empty category sections** — scan the new release entry and delete any `### <Category>` heading that has no bullet entries beneath it (i.e., the next line is blank followed by another `###` heading or `##` heading or end of section). This is mandatory — never leave empty `### Breaking Changes`, `### Features Added`, `### Bugs Fixed`, or `### Other Changes` sections in the final output.
2. For extensions using flat bullet lists (no category headings), match the existing style.
3. **Validate PR links**: verify that every `- ` bullet under a category heading contains a `[[#NNNN]]` link. If any entry is missing a link, flag it and require either adding the PR reference or explicitly justifying the omission. When a single large PR warrants multiple changelog bullets, each must reference the same PR number.
4. **Cross-reference commit range**: verify every `[[#NNNN]]` PR number against the commit list from Step 3. Flag any PR that does not appear in the commit range — it may be a duplicate from a prior release or an incorrectly attributed entry.
5. Present the **complete changelog entry** to the user for review via `ask_user`.

   Choices:
   - **Approve** — accept and proceed to spell check
   - **Edit** — provide feedback for revisions (loop back)
   - **Abort** — cancel the entire workflow

### Step 6 — Spell Check

```bash
cspell lint "<changelog-path>" --relative --config "$(git rev-parse --show-toplevel)/cli/azd/.vscode/cspell.yaml" --no-progress
```

If new names or handles trigger errors, add them to `.vscode/cspell-github-user-aliases.txt`.

---

## Error Handling

- GitHub MCP unavailable → fall back to `gh` CLI: `gh pr view {number} --json title,author,body,labels,files`
- No new commits since cutoff → inform user, no changelog entry needed
- PR deleted or inaccessible → log warning, skip with user confirmation
- cspell not installed → warn, skip spell check, note in summary
- Extension missing required files → stop, list which files are missing
- Version mismatch between `version.txt` and `extension.yaml` → warn, ask which is correct
- Cannot determine cutoff → ask user for the cutoff commit SHA or previous release version
