# Documentation Maintenance

This repository uses a structured documentation system under `docs/` at the repo root.
When making changes to the codebase, keep the documentation current.

## Documentation Structure

```text
docs/
├── README.md          — Documentation index and navigation
├── concepts/          — Core mental models, terminology, feature lifecycle
├── guides/            — Task-oriented how-tos for contributors
├── reference/         — Schemas, flags, environment variables, feature status
└── architecture/      — System overviews, design context, ADRs
```

## When to Update Documentation

- **New command or flag:** Update `docs/guides/adding-a-new-command.md` if the pattern changes; update `docs/reference/feature-status.md` with the new feature's stage.
- **New environment variable:** Add it to `docs/reference/environment-variables.md`.
- **New extension capability:** Update `docs/architecture/extension-framework.md` and `docs/guides/creating-an-extension.md`.
- **Feature stage change:** Update `docs/reference/feature-status.md` when a feature graduates (alpha → beta → stable).
- **New concept or term:** Add it to `docs/concepts/glossary.md`.
- **Architecture decision:** Create a new ADR using the template at `docs/architecture/adr-template.md`.
- **New hosting target or language:** Update `docs/reference/feature-status.md` and `docs/concepts/glossary.md`.

## Documentation Placement Guide

| Content type | Location |
|---|---|
| Term or concept definition | `docs/concepts/glossary.md` |
| Contributor how-to | `docs/guides/` |
| Configuration reference | `docs/reference/` |
| System design or ADR | `docs/architecture/` |
| Implementation design details | `cli/azd/docs/design/` |
| Extension development details | `cli/azd/docs/extensions/` |
| Code style standards | `cli/azd/docs/style-guidelines/` |

## Documentation Standards

- Use clear, concise language
- Link to detailed implementation docs in `cli/azd/docs/` rather than duplicating content
- Keep tables and lists scannable
- Include code examples where they aid understanding
