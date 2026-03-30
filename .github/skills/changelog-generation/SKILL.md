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

## Usage

**USE FOR:**
- Generate core azd release changelog
- Generate extension release changelog
- Bump version for a release
- Write changelog entries from merged PRs

**DO NOT USE FOR:**
- Code review (use `code-review`)
- Creating pull requests (use `pull-request`)
- Publishing extensions to registry
- Deploying releases to production

---

## Workflow

### Step 1 — Determine Scope

If not clear from context, ask via `ask_user`:

> What are you releasing?

Choices:
- **Core azd CLI** — updates `cli/azd/CHANGELOG.md` and `cli/version.txt`
- **An extension** — updates extension `CHANGELOG.md`, `version.txt`, and `extension.yaml`

If **extension**: list folders under `cli/azd/extensions/` and ask the user to confirm the target. Verify it contains `CHANGELOG.md`, `version.txt`, and `extension.yaml`.

### Step 2 — Determine Version & Update Files

Per [references/scope-rules.md](references/scope-rules.md) § Version Files.

- **Core**: derive version from the existing unreleased header (strip `-beta.*` and `(Unreleased)`), use today's date.
- **Extension**: ask the user for the new version number.

Present the version and date to the user for confirmation before writing any files.

### Step 3 — Find Cutoff & List Commits

Per [references/scope-rules.md](references/scope-rules.md) § Commit Discovery.

1. Inspect changelog git history to find the cutoff commit SHA.
2. List commits from cutoff to HEAD (extensions: scoped to extension path).
3. Present the commit list to the user. Ask if any should be explicitly included or excluded before processing.

### Step 4 — Process Each PR

For **each** commit in scope, complete the full sub-workflow before moving to the next. **Do not batch, skip, or abbreviate — even for large release trains.**

Per [references/pr-processing.md](references/pr-processing.md):

1. Extract PR number from commit subject (`(#1234)`) or locate by SHA.
2. Fetch PR details via GitHub MCP (owner: `Azure`, repo: `azure-dev`).
3. Detect external contributors per [references/pr-processing.md](references/pr-processing.md) § External Contributor Detection.
4. Fetch linked issues from the PR body when relevant to understanding user impact.
5. Apply exclusion rules per [references/pr-processing.md](references/pr-processing.md) § Exclusion Rules.
6. Categorize and write the entry per [references/pr-processing.md](references/pr-processing.md) § Category Classification and § Entry Format.

### Step 5 — Assemble & Review

1. Remove any empty category sections from the new release entry.
2. For extensions using flat bullet lists (no category headings), match the existing style.
3. Present the **complete changelog entry** to the user for review via `ask_user`. Loop until approved.

### Step 6 — Spell Check

```bash
cspell lint "<changelog-path>" --relative --config cli/azd/.vscode/cspell.yaml --no-progress
```

If new names or handles trigger errors, add them to `cli/azd/.vscode/cspell-github-user-aliases.txt`.

---

## Error Handling

- GitHub MCP unavailable → fall back to `gh` CLI: `gh pr view {number} --json title,author,body,labels`
- No new commits since cutoff → inform user, no changelog entry needed
- PR deleted or inaccessible → log warning, skip with user confirmation
- cspell not installed → warn, skip spell check, note in summary
- Extension missing required files → stop, list which files are missing
- Version mismatch between `version.txt` and `extension.yaml` → warn, ask which is correct
- Cannot determine cutoff → ask user for the cutoff commit SHA or previous release version
