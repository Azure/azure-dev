# Azure Developer CLI Documentation

> Internal documentation for contributors and AI coding agents working on the Azure Developer CLI (`azd`).

For end-user documentation, see [aka.ms/azd](https://aka.ms/azd). For template examples, see [awesome-azd](https://azure.github.io/awesome-azd/).

---

## Concepts

Core mental models, terminology, and feature lifecycle.

- [Glossary](concepts/glossary.md) — Key terms and concepts used throughout the codebase
- [Feature Stages](concepts/feature-stages.md) — How features graduate from alpha → beta → stable
- [Alpha Features](concepts/alpha-features.md) — How experimental features are gated and discovered

## Guides

Task-oriented how-tos for common contributor workflows.

- [Contributing](guides/contributing.md) — How to build, test, lint, and submit changes
- [Adding a New Command](guides/adding-a-new-command.md) — End-to-end walkthrough for new CLI commands
- [Creating an Extension](guides/creating-an-extension.md) — How to build and publish an azd extension
- [Observability and Tracing](guides/observability.md) — Adding telemetry, traces, and debugging

## Reference

Schemas, flags, environment variables, and configuration details.

- [Environment Variables](reference/environment-variables.md) — All environment variables that configure azd behavior
- [azure.yaml Schema](reference/azure-yaml-schema.md) — Project configuration file reference
- [Feature Status](reference/feature-status.md) — Current maturity status of all features

## Architecture

System overviews, design context, and decision records.

- [System Overview](architecture/system-overview.md) — High-level architecture and code organization
- [Command Execution Model](architecture/command-execution-model.md) — How commands are registered, resolved, and run
- [Extension Framework](architecture/extension-framework.md) — gRPC-based extension system architecture
- [Provisioning Pipeline](architecture/provisioning-pipeline.md) — How infrastructure provisioning works
- [ADR Template](architecture/adr-template.md) — Template for lightweight architecture decision records

---

## Where do new docs go?

| You want to document… | Put it in… |
|---|---|
| A new term or concept | `docs/concepts/glossary.md` |
| A how-to for contributors | `docs/guides/` |
| A configuration reference | `docs/reference/` |
| A system design or ADR | `docs/architecture/` |
| Detailed implementation design | `cli/azd/docs/design/` |
| Extension development details | `cli/azd/docs/extensions/` |
| Style and coding standards | `cli/azd/docs/style-guidelines/` |

## Related documentation

- [AGENTS.md](../AGENTS.md) — AI agent coding instructions
- [cli/azd/docs/](../cli/azd/docs/) — Detailed implementation-level documentation
- [cli/azd/docs/style-guidelines/](../cli/azd/docs/style-guidelines/) — Code style guide
- [cli/azd/docs/extensions/](../cli/azd/docs/extensions/) — Extension framework details
