# Scope Rules

## Version Files

### Core Scope

Files to update:
- `cli/azd/CHANGELOG.md` — add release entry
- `cli/version.txt` — set to released version
- `cli/azd/.vscode/cspell-github-user-aliases.txt` — if spell check additions needed

**Version derivation:**
1. Find the top-most section in `cli/azd/CHANGELOG.md` (e.g., `## X.Y.Z-beta.N (Unreleased)`).
2. Strip the `-beta.N` suffix and `(Unreleased)` marker.
3. Format as: `## X.Y.Z (YYYY-MM-DD)` using today's date.
4. Set `cli/version.txt` to `X.Y.Z`.

**Do NOT** update any extension files.

### Extension Scope

Files to update:
- `<extension>/CHANGELOG.md` — add release entry
- `<extension>/version.txt` — set to new version
- `<extension>/extension.yaml` — update `version:` field

**Version derivation:**
1. Ask the user for the new version (SemVer, optional `-preview` suffix).
2. Update `version.txt` and `extension.yaml` — they **must** match exactly.
3. Add new top entry: `## {version} ({YYYY-MM-DD})` using today's date.

**Do NOT** update `cli/azd/CHANGELOG.md`, `cli/version.txt`, or `registry.json`.

## Commit Discovery

### Core

1. Find the cutoff commit:
   ```bash
   git --no-pager log -n 3 --follow -p -- cli/azd/CHANGELOG.md
   ```
   Identify the commit SHA that added the **previous released version's** changelog entries — the last section with actual content (not just an empty placeholder). Ignore bot commits that only add unreleased headers with empty categories.

2. List all commits from cutoff to HEAD:
   ```bash
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short {cutoff_sha}..origin/main
   ```

### Extension

1. Find the cutoff commit:
   ```bash
   git --no-pager log -n 3 --follow -p -- <extension>/CHANGELOG.md
   ```
   Identify the commit that added the previous version's entries.

2. List commits scoped to the extension path:
   ```bash
   git --no-pager log --oneline --pretty=format:"%h (%ad)%d %s" --date=short {cutoff_sha}..origin/main -- <extension>/
   ```

Note: using `{cutoff_sha}..origin/main` (range syntax) instead of a fixed `-N` limit ensures all commits are captured regardless of release train size.
