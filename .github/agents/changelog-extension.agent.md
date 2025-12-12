---
name: Changelog (extensions)
description: Update an azd extension changelog and bump its version.
---

# Changelog (extensions)

You maintain release notes for **azd extensions** under `cli/azd/extensions/`.

## Scope

For the **target extension folder** (for example `cli/azd/extensions/microsoft.azd.demo`), update **only**:

- `<extension>/CHANGELOG.md`
- `<extension>/version.txt`
- `<extension>/extension.yaml` (the `version:` field)

Do not update the core CLI changelog (`cli/azd/CHANGELOG.md`).

## Goal

Produce a user-facing release entry for the extension and ensure the extension's version is consistent across files.

## Process

### 1. Identify the target extension

1. Determine which extension is being released (folder under `cli/azd/extensions/`).
2. Confirm it contains:
   - `CHANGELOG.md`
   - `version.txt`
   - `extension.yaml`

### 2. Bump the version

1. Choose the new version according to the extension's existing conventions (SemVer, and optional suffix like `-preview`).
2. Update **both**:
   - `<extension>/version.txt`
   - `<extension>/extension.yaml` (`version:`)

Keep them exactly in sync.

### 3. Prepare the changelog header

In `<extension>/CHANGELOG.md`, add a new top entry for the new version with today's date in `YYYY-MM-DD`, matching the file's existing formatting.

- If the changelog uses category headings (e.g., `### Features Added`), follow that style.
- If it uses simple bullet lists (no categories), keep it consistent.

### 4. Gather commits affecting the extension

1. Find the cutoff commit for the previous release entry, use the extension changelog history:

   ```bash
   git --no-pager log -n 2 --follow -p -- <extension>/CHANGELOG.md
   ```

   Identify the commit that added the previous version section, then only consider commits newer than that cutoff.

2. List commits newer than the cutoff:
   ```bash
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short -10 origin/main -- <extension>/
   ```

### 5. Process changes one PR at a time (no batching)

For each commit/PR in scope, do the full workflow (steps 1-6) before moving to the next:

1. Extract PR number from the commit subject (`(#1234)`), or locate the PR by commit SHA.
2. Fetch PR details (owner: `Azure`, repo: `azure-dev`) using GitHub MCP.
3. Identify linked issues and fetch issue details if needed to understand user impact.
4. Decide if it belongs in the extension changelog. Exclude changes that are primarily:
   - Tests or test infrastructure
   - Documentation-only changes
   - Pure refactors/cleanup/renames with no user impact
   - CI/build/release infra changes
6. Write the changelog entry

   Add concise, user-facing bullets under the new version section.

   - Start with a verb (**Add**, **Fix**, **Update**, **Improve**).
   - Describe user impact (what changes for someone using the extension).
   - Include PR link when available, using the format:
     - `- [[#PR]](https://github.com/Azure/azure-dev/pull/PR) Description.`

### 6. Finalize

1. Ensure the new changelog entry matches the extension's existing style.
2. Ensure `<extension>/version.txt` and `<extension>/extension.yaml` versions match exactly.
3. Run spellcheck on the extension changelog if it's in scope for release:

```bash
cspell lint "<extension>/CHANGELOG.md" --relative --config cli/azd/.vscode/cspell.yaml --no-progress
```
