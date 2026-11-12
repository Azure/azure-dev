# Scope Rules

## Version Files

### Core Scope

Files to update:
- `cli/azd/CHANGELOG.md` — add release entry
- `cli/version.txt` — set to released version
- `cli/azd/pkg/azdext/version.go` — update `Version` constant to match `cli/version.txt`
- `.vscode/cspell-github-user-aliases.txt` — if spell check additions needed

**Version derivation:**
1. Use the version specified in the triggering issue or user request when explicit.
2. Otherwise, assess the changes included during PR processing and select an appropriate patch or minor version.
3. Find the top-most `(Unreleased)` section in `cli/azd/CHANGELOG.md`. Treat its preview version as context only; it does not determine the release version.
4. Replace that section's header with `## <release-version> (YYYY-MM-DD)` using **today's date**. Do not add a new unreleased placeholder above it because release-readiness checks require the first changelog entry to match `cli/version.txt`.
5. Set `cli/version.txt` and the `Version` constant in `cli/azd/pkg/azdext/version.go` to the release version.

**Do NOT** update any extension files.

### Extension Scope

Files to update:
- `<extension>/CHANGELOG.md` — add release entry
- `<extension>/version.txt` — set to new version
- `<extension>/extension.yaml` — update `version:` field

**Version derivation:**
1. Use the version specified in the triggering issue or user request when explicit. Otherwise, after PR processing, select the next version by following the extension's own convention in its `CHANGELOG.md` history, such as its bump pattern and any `-preview` suffix, and assessing the included changes.
2. Update `version.txt` and `extension.yaml` — they **must** match exactly.
3. Add new top entry: `## {version} (YYYY-MM-DD)` using today's date.

**Do NOT** update `cli/azd/CHANGELOG.md`, `cli/version.txt`, or `cli/azd/extensions/registry.json` (CI-generated; editing by hand can break install checksums).

## Commit Discovery

### Core

1. Find the cutoff commit:
   ```bash
   git --no-pager log -n 3 --follow -p -- cli/azd/CHANGELOG.md
   ```
   Identify the commit SHA that added the **previous released version's** changelog entries — the last section with actual content (not just an empty placeholder). Ignore bot commits that only add unreleased headers with empty categories.

2. Ensure refs are up to date, then list all commits from cutoff to `origin/main`:
   ```bash
   git fetch origin main
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short {cutoff_sha}..origin/main
   ```

   If no previous version section exists in CHANGELOG.md (first release), treat all commits on `origin/main` as in-scope.

### Extension

1. Find the cutoff commit:
   ```bash
   git --no-pager log -n 3 --follow -p -- <extension>/CHANGELOG.md
   ```
   Identify the commit that added the previous version's entries.

2. Ensure refs are up to date, then list commits scoped to the extension path:
   ```bash
   git fetch origin main
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short {cutoff_sha}..origin/main -- <extension>/
   ```

   If no previous version section exists in CHANGELOG.md (first release), treat all commits on `origin/main` scoped to the extension path as in-scope.

Note: using `{cutoff_sha}..origin/main` (range syntax) instead of a fixed `-N` limit ensures all commits are captured regardless of release train size.
