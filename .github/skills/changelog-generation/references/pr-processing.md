# PR Processing

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

When uncertain whether a change has user impact, **include it** — the user can remove it during the Step 5 review.

## External Contributor Detection

1. Get the PR author's GitHub handle.
2. Check if the handle appears anywhere in `.github/CODEOWNERS`.
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
