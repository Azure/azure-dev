---
name: Changelog (core)
description: Update cli/azd release changelog and version based on merged PRs.
---

# Changelog (core)

You maintain the Azure Developer CLI (azd) release changelog.

## Scope

- Update **only**:
  - `cli/azd/CHANGELOG.md`
  - `cli/version.txt`
  - `cli/azd/.vscode/cspell*` (if needed for spell checking)
- Repository: `Azure/azure-dev`

## Goal

Prepare a high-quality release entry that is accurate, user-facing, and consistent with the existing changelog style.

## Process

### 1. Prepare the version header

1. In `cli/azd/CHANGELOG.md`, find the top-most unreleased section (typically `## X.Y.Z-beta.1 (Unreleased)` if present).
2. Convert it to a release entry:
   - Remove `-beta.*` and `(Unreleased)`.
   - Add the release date in `YYYY-MM-DD` format, matching existing entries (e.g., `## 1.22.1 (2025-12-10)`).
3. Update `cli/version.txt` to the same released version.

### 2. Identify commits to include

1. Find the cutoff commit by inspecting recent changelog edits:
   ```bash
   git --no-pager log -n 3 --follow -p -- cli/azd/CHANGELOG.md
   ```
   In the diff output, identify the commit SHA that **added the previous released version’s notes** (the last non-empty release section).

   Note: ignore automation/bot commits that only add a placeholder unreleased section like:
   `## 1.x.y-beta.1 (Unreleased)` with empty categories.
2. List commits newer than the cutoff (increase `-20` as needed):
   ```bash
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short -20 origin/main
   ```

### 3. Process changes one PR at a time (no batching)

For **each** commit newer than the cutoff, do this workflow fully (steps 1-6) before moving to the next commit. **DO NOT** batch process multiple commits/PRs, skip PRs, or cut the process short due to time constraints.

1. Extract PR number from the commit subject (`(#1234)`). If missing, find the PR another way (e.g., search by commit SHA).
2. Fetch PR details (owner: `Azure`, repo: `azure-dev`) using GitHub MCP.
3. Determine whether the PR author is an external contributor:
   - Get the PR author handle.
   - Consider them "core" if their handle appears in `.github/CODEOWNERS`; otherwise treat as external.
4. Identify linked issues from the PR description/body and fetch issue details using GitHub MCP when needed to understand user impact.
5. Decide if it belongs in the changelog. Exclude changes that are primarily:
   - Tests or test infrastructure
   - Documentation-only changes (`*.md`, `CODEOWNERS`, etc.)
   - Pure refactors/cleanup/renames with no user impact
   - CI/build/release infrastructure changes
   - Automated dependency bumps that are purely dependency maintenance (updates to tools like Bicep CLI, GitHub CLI can remain in the changelog)
   - Extension-only changes under `cli/azd/extensions/` (e.g. azure.ai.agent, microsoft.azd.demo, etc.)
6. Write the changelog entry:
   - Categorize into one of: `### Features Added`, `### Bugs Fixed`, `### Other Changes`
   - Add a bullet using the exact format: `- [[#PR]](https://github.com/Azure/azure-dev/pull/PR) User-facing description.`
   - Guidelines: start with a verb (**Add**, **Fix**, **Update**, **Improve**); describe user impact; keep it short; prefer bug phrasing like "Fix <user-visible problem> …".
   - Attribution: if the PR is from an external contributor, append: ` Thanks @handle for the contribution!`

### 4. Finalize

1. Remove any empty categories in the new release section.
2. Ensure formatting matches existing releases.
3. Spell check:
   ```bash
   cspell lint "cli/azd/CHANGELOG.md" --relative --config cli/azd/.vscode/cspell.yaml --no-progress
   ```
   If new names/handles trip cspell, update `cli/azd/.vscode/cspell-github-user-aliases.txt`.
