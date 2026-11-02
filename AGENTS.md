# Agent guidance

Start here for repository work. Follow the shared instructions and the local
`AGENTS.md` files that apply to your task. Local instructions add area-specific
rules and exceptions.

## Security and privacy

- Protect credentials and customer data. Do not expose them in output, logs,
  issues, or artifacts.
- Validate paths derived from user input or template paths.
- Quote paths in user-facing shell examples and suggestions.

## Repository-wide practices

- Make focused changes, preserve existing behavior unless intentionally changing
  it, and add or update appropriate tests. Validate the affected behavior.
- Keep documentation and user-facing help current. Follow the
  [documentation maintenance instructions](.github/instructions/documentation.instructions.md)
  for code and documentation changes.
- Read the whole relevant file and surrounding context before changing or
  reviewing it. Load additional task-specific guidance when it applies.

## Choose guidance for the work

| Work | Required starting point |
|---|---|
| Core CLI (`cli/azd/`) | Read the entire [CLI handbook](cli/azd/AGENTS.md), including conditional UX, telemetry, release, and testing guidance. |
| Microsoft-authored extensions (`cli/azd/extensions/`) | Read the entire [CLI handbook](cli/azd/AGENTS.md), the [shared extension guide](cli/azd/extensions/AGENTS.md), and any extension-local `AGENTS.md`. |
| VS Code extension (`ext/vscode/`) | Read the [VS Code guide](ext/vscode/AGENTS.md). |
| Go code | Follow the [Go review instructions](.github/instructions/go.instructions.md) and the [Go conventions](cli/azd/AGENTS.md#modern-go) in the CLI handbook. |
| Documentation | Follow the [documentation maintenance instructions](.github/instructions/documentation.instructions.md) and the relevant area guide. |

For **every PR review**, inspect the full changed-file list first. If any file
under `cli/azd/extensions/` changes, including in a fork PR, apply the
[extension user contract review](.github/skills/extension-user-contract-review/SKILL.md)
before general review. Run this pass even when you do not expect a contract
change. Follow the skill's coverage and reporting requirements.

## How instructions are organized

[Path-specific Copilot instructions](.github/instructions/) apply to matching
files. Follow them even when a client does not load them automatically. Check
the affected directory and its parents for local `AGENTS.md` files.

For CLI UX, telemetry, and release work, follow the relevant sections of the
[CLI handbook](cli/azd/AGENTS.md). Use the [skills](.github/skills/) and
[custom agents](.github/agents/) that fit the task. Do not load every procedure.
Some tasks require a skill, such as the extension review pass above.
