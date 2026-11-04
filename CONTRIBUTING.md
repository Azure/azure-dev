# Contributing to `azd`

Thanks for helping improve the Azure Developer CLI. Start with the path that fits your work.

## Choose a starting point

- Core contributors: read the [guiding principles](cli/azd/docs/style-guidelines/guiding-principles.md) and [architecture overview](docs/architecture/system-overview.md) to understand where your change fits. The [CLI contribution guide](cli/azd/CONTRIBUTING.md) covers building, testing, and submitting changes.
- Extension authors: start with [creating an extension](docs/guides/creating-an-extension.md). You can develop an external extension in your own repository. First-party extensions live in `cli/azd/extensions/` and follow this repository's contribution instructions.
- People writing or updating docs: use the [documentation index](docs/README.md) to find existing guidance and choose a home for your changes.
- People learning the CLI: use the [architecture overview](docs/architecture/system-overview.md) and [glossary](docs/concepts/glossary.md) to understand the codebase. For using `azd`, start with the [user documentation](https://aka.ms/azd) and [template examples](https://azure.github.io/awesome-azd/).

## Working with coding agents

Humans and coding agents can use the same contributor guides. If you use an agent, start with [AGENTS.md](AGENTS.md) and read the instructions that apply to the files you plan to change. If your checkout shows only a path in that file, open the linked [CLI handbook](cli/azd/AGENTS.md) directly. Review the agent's changes and validation results before submitting them.

## Proposing and reviewing a design

Use the [extension-versus-core decision framework](cli/azd/docs/style-guidelines/guiding-principles.md#choose-between-core-and-an-extension) before choosing an implementation. Record the decision in the issue or design discussion and use its [design and PR review checklist](cli/azd/docs/style-guidelines/guiding-principles.md#design-and-pr-review-checklist) when preparing or reviewing a PR. Keep build, test, and submission details in the [CLI contribution guide](cli/azd/CONTRIBUTING.md).
