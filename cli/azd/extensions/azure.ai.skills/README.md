# Azure Developer CLI (azd) Skills Extension

Manage [Microsoft Foundry](https://learn.microsoft.com/azure/ai-services/) **skills**
(reusable behavioral guidelines an agent can attach at runtime) directly from your
terminal.

## Commands

```bash
azd ai skill create <name> [--description "..." --instructions "..."]
azd ai skill create <name> --file ./SKILL.md
azd ai skill create <name> --file ./skill.zip
azd ai skill create <name> --file ./skill-src/

azd ai skill update <name> [--description "..."] [--instructions "..."] [--file ./SKILL.md]
azd ai skill update <name> --set-default-version <version>
azd ai skill show <name>
azd ai skill list [--top N] [--orderby <field>]
azd ai skill download <name> [--version <ver>] [--output-dir <path>] [--raw] [--force]
azd ai skill delete <name> [--force]
```

Skills are **versioned and immutable**. `create` uploads the first default
version; `update` uploads a new default version (or, with
`--set-default-version`, just repoints `default_version` at an existing
version). Names follow the agentskills.io spec
(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`, max 64 chars).

`create` accepts inline content (`--description` / `--instructions`), a
single `SKILL.md` file, a `.zip` package, or a directory whose root contains
a `SKILL.md`. Directory mode is the round-trip inverse of
`azd ai skill download`: the CLI packages the directory as a zip in memory
and uploads it as multipart/form-data, identical to the `.zip` path.

`update` is inline-only: a `.zip` or a directory is rejected with a pointer
to `azd ai skill create --force` (a destructive delete-then-create), since
swapping the entire package shape is a `create` concern, not an `update`.

All commands accept the standard cross-cutting flags: `-p` / `--project-endpoint`,
`--output table|json`, `--no-prompt`, and `--debug`.

## Project endpoint resolution

The Foundry project endpoint is resolved in this order:

1. `-p` / `--project-endpoint` flag on the command.
2. Active azd env value `AZURE_AI_PROJECT_ENDPOINT`.
3. Global config `extensions.ai-projects.context.endpoint` (written by
   `azd ai project set`). Falls back to the legacy
   `extensions.ai-skills.project.context.endpoint` and
   `extensions.ai-agents.project.context.endpoint` keys so users who
   configured the endpoint via earlier extensions are not forced to re-run
   `set`.
4. Host environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error with an actionable suggestion.

## Local Development

### Prerequisites

1. **Install developer kit extension** (if not already installed):

   ```bash
   azd ext install microsoft.azd.extensions
   ```

### Building and installing locally

1. **Navigate to the extension directory**:

   ```bash
   cd cli/azd/extensions/azure.ai.skills
   ```

2. **Initial setup** (first time only):

   ```bash
   azd x build
   azd x pack
   azd x publish
   ```

3. **Install the extension**:

   ```bash
   azd ext install azure.ai.skills
   ```

4. **For subsequent development** (after initial setup):

   ```bash
   azd x watch
   ```

   This automatically watches for file changes, rebuilds, and installs updates
   locally.
