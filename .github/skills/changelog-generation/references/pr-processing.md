# PR Processing

## PR Number Extraction

Extract the PR number from the commit subject using these patterns (in order):

1. **Dual PR numbers** (check first): if the commit subject contains **two or more** `(#NNNN)` patterns (e.g., `Fix auth error (#7233) (#7235)`), use the **last** one as the canonical reference — it is typically the merge/backport PR. Record the first as an alias to prevent re-processing.
2. **Squash merge** (single match): if exactly one `(#1234)` pattern is found in the commit subject line, use it as the PR number.
3. **Merge commit**: look for `Merge pull request #1234 from user/branch`.
4. **Fallback** (if no pattern matches): query by commit SHA:
   ```bash
   gh api repos/Azure/azure-dev/commits/<SHA>/pulls --jq '.[0].number'
   ```

## Exclusion Rules

Exclude changes that are **primarily**:

- Tests or test infrastructure only
- Documentation-only changes (`*.md`, `CODEOWNERS`, etc.) with no functional impact
- Pure refactors, cleanup, or renames with no user-visible impact
- CI/build/release infrastructure changes
- Automated dependency bumps that are purely maintenance

**Keep in changelog** even if they look like dependency updates:
- Updates to user-facing tools (Bicep CLI, GitHub CLI, Terraform provider versions)

**Core-only additional exclusions:**
- Extension-only changes under `cli/azd/extensions/` (these belong in the extension's own changelog)

**Alpha/beta-gated features**: features that require an alpha feature flag (`pkg/alpha`) at the time of release are excluded from the changelog. Include them in the release where they are promoted to public preview (beta) or GA. When processing a "promote to beta/GA" PR, write the entry as a new feature under `### Features Added` — not as an `### Other Changes` item.

When uncertain whether a change has user impact, **include it** — the user can remove it during the Step 5 review. Specifically, these borderline categories should default to **included**:
- Bug fixes that change observable CLI behavior (even if the bug was in flag parsing or output formatting)
- Changes to help text, error messages, or CLI output visible to users
- UX improvements that reduce noise or improve readability of output

## External Contributor Detection

1. Get the PR author's GitHub handle.
2. Check if `@{handle}` appears as a whitespace-delimited token in `.github/CODEOWNERS`.
3. If **not found** in CODEOWNERS, treat as an external contributor.

This is approximate — CODEOWNERS maps file ownership, not team membership. Err on the side of attributing (a core member getting a "thanks" is harmless; an external contributor not getting one is not).

## Entry Format

Each entry uses this exact format:

```
- [[#PR]](https://github.com/Azure/azure-dev/pull/PR) User-facing description.
```

**Writing guidelines:**
- Start with a verb: **Add**, **Fix**, **Update**, **Improve**, **Remove**
- Describe the user-visible impact, not the implementation detail
- Keep entries to one concise sentence
- For bugs: prefer "Fix \<user-visible problem\>" phrasing (e.g., "Fix panic when deploying without Docker installed")

**Attribution** — append for external contributors only:

```
- [[#PR]](https://github.com/Azure/azure-dev/pull/PR) Description. Thanks @handle for the contribution!
```

## Category Classification

Assign each entry to exactly one category:

| Category | When to Use |
|----------|-------------|
| `### Features Added` | New capabilities, commands, options, integrations |
| `### Breaking Changes` | Removed/renamed commands, changed defaults, behavior changes requiring user action |
| `### Bugs Fixed` | Corrections to incorrect behavior, crash fixes, error handling improvements |
| `### Other Changes` | Performance improvements, dependency updates with user impact, telemetry, minor UX polish |

**Extension style adaptation**: if the target extension's existing `CHANGELOG.md` uses flat bullet lists without category headings, omit categories and list all entries as simple bullets under the version header. Inspect the most recent 2-3 entries to determine the style.
