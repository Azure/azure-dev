# Changelog Audit Tool

Retroactively applies the updated changelog generation rules to past releases
and produces per-release comparison files showing the published changelog
vs the corrected version under the new rules.

## Usage

```bash
cd .github/skills/changelog-generation/audit
go run .
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-n` | 20 | Number of releases to audit |
| `-changelog` | auto-detected | Path to `CHANGELOG.md` |
| `-tag-prefix` | `azure-dev-cli_` | Git tag prefix |
| `-repo-root` | auto-detected | Git repository root |
| `-output` | `findings.md` | Findings report path |

## Output

The tool generates three outputs:

### `findings.md` — Audit findings report

Summary table, per-rule aggregates, and per-release findings with context.
No embedded diffs — just clean, readable findings.

### `published/<version>.md` — Verbatim changelog sections

Each file contains the exact changelog section as published for that release.
These are extracted verbatim from `CHANGELOG.md`.

### `corrected/<version>.md` — Deterministic corrections

Each file contains the same changelog section with **only deterministic
corrections** applied:

| Rule | Correction | Deterministic? |
|------|-----------|----------------|
| F1 | Swap wrong PR number with canonical (last) PR | Yes |
| F2b | Change `/issues/` to `/pull/` in link URL | Yes |
| F3 | Remove cross-release duplicate entry | Yes |
| F6 | Remove phantom entry (PR not in commit range) | Yes |
| F6b | Fix link text to match URL number | Yes |
| F2 | Missing PR link — **reported only, not corrected** | No |
| F5 | Borderline excluded commit — **reported only** | No |

### Comparing published vs corrected

```bash
# Diff a single release
diff -u published/1.23.12.md corrected/1.23.12.md

# Diff all releases with corrections
diff -ru published/ corrected/

# On Windows (PowerShell)
Compare-Object (Get-Content published\1.23.12.md) (Get-Content corrected\1.23.12.md)
```

## What It Checks

| Rule | Finding | Severity |
|------|---------|----------|
| F1 | Dual PR numbers — commit has multiple `(#N)` patterns | warning |
| F2 | Missing `[[#N]]` PR link on a changelog entry | error |
| F2b | Entry uses `/issues/` link instead of `/pull/` | warning |
| F3 | Same PR/issue number appears in a prior release | warning |
| F3b | Same PR number appears multiple times in one release | warning |
| F4 | Alpha/feature-flag mention in commit subject | info |
| F5 | Excluded commit matches borderline user-facing keywords | warning |
| F6 | Changelog entry references PR not in the commit range | warning |
| F6b | Link text `[[#N]]` doesn't match URL number | error |
