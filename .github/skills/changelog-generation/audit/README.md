# Changelog Audit Tool

Retroactively applies the updated changelog generation rules to past releases
and produces a side-by-side comparison report showing the live changelog entries
vs issues the new rules would have caught.

## Usage

```bash
cd .github/skills/changelog-generation/audit
go run . -n 20 -output report.md
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-n` | 20 | Number of releases to audit |
| `-changelog` | auto-detected | Path to `CHANGELOG.md` |
| `-tag-prefix` | `azure-dev-cli_` | Git tag prefix |
| `-repo-root` | auto-detected | Git repository root |
| `-output` | stdout | Output file path |

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

## Output

The tool generates a markdown report with:

- **Summary table** — per-release error/warning/info counts
- **Findings by rule** — aggregate counts across all releases
- **Per-release detail** — specific findings with context and recommended changes
