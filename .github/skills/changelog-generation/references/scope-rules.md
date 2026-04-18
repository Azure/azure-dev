# Scope Rules

## Version Files

### Core Scope

Files to update:
- `cli/azd/CHANGELOG.md` — add release entry
- `cli/version.txt` — set to released version
- `cli/azd/pkg/azdext/version.go` — update `Version` constant to match `cli/version.txt`
- `.vscode/cspell-github-user-aliases.txt` — if spell check additions needed

**Version derivation:**
1. Find the top-most section in `cli/azd/CHANGELOG.md` (e.g., `## X.Y.Z-beta.N (Unreleased)`).
2. Strip the `-beta.N` suffix and `(Unreleased)` marker.
3. Format as: `## X.Y.Z (YYYY-MM-DD)` using **today's date** (the date the changelog is being authored/committed, not a future planned ship date).
4. Set `cli/version.txt` to `X.Y.Z`.
5. Set the `Version` constant in `cli/azd/pkg/azdext/version.go` to `X.Y.Z`.
6. If no unreleased header is found in the changelog, ask the user for the release version number via `ask_user`.

**Unreleased placeholder after release:**

After converting the top section from `X.Y.Z-beta.N (Unreleased)` to `X.Y.Z (YYYY-MM-DD)`, add a new `(Unreleased)` placeholder at the top using the **next minor version**, not the next patch:

- If releasing a **patch** (`X.Y.Z` where `Z > 0`), the new placeholder is `X.(Y+1).0-beta.1 (Unreleased)`.
- If releasing a **minor** (`X.Y.0`), the new placeholder is `X.(Y+1).0-beta.1 (Unreleased)`.

Example: after releasing `1.24.1`, add `## 1.25.0-beta.1 (Unreleased)` at the top — not `## 1.24.2-beta.1`.

This matches the behavior of `eng/scripts/Update-CliVersion.ps1`, which increments the minor version (not the patch) when creating the post-release development placeholder.

**Do NOT** update any extension files.

### Extension Scope

Files to update:
- `<extension>/CHANGELOG.md` — add release entry
- `<extension>/version.txt` — set to new version
- `<extension>/extension.yaml` — update `version:` field

**Version derivation:**
1. Ask the user for the new version (SemVer, optional `-preview` suffix).
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
