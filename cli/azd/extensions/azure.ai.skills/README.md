# Azure Developer CLI (azd) Skills Extension

Manage [Microsoft Foundry](https://learn.microsoft.com/azure/ai-services/) **skills**
(reusable behavioral guidelines an agent can attach at runtime) directly from your
terminal.

## Commands

```bash
azd ai skill add <name> [--description "..." --instructions "..."]
azd ai skill add <name> --file ./SKILL.md
azd ai skill add <name> --file ./skill.zip
azd ai skill add <name> --file ./skill-src/

azd ai skill create <name> [--description "..." --instructions "..."]
azd ai skill create <name> --file ./SKILL.md
azd ai skill create <name> --file ./skill.zip
azd ai skill create <name> --file ./skill-src/

azd ai skill update <name> [--description "..."] [--instructions "..."] [--file ./SKILL.md]
azd ai skill update <name> --file ./skill.zip
azd ai skill update <name> --file ./skill-src/
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

`add` is declarative. It adds or updates a
`host: azure.ai.skill` service in the current project's `azure.yaml` without
mutating the remote skill. Run `azd deploy <name>` or `azd up` afterward to
reconcile it. Existing `uses:`, `project:`, and unowned service fields are
preserved.

`create` accepts inline content (`--description` / `--instructions`), a
single `SKILL.md` file, a `.zip` package, or a directory whose root contains
a `SKILL.md`. Directory mode is the round-trip inverse of
`azd ai skill download`: the CLI packages the directory as a zip in memory
and uploads it as multipart/form-data, identical to the `.zip` path.

`update` is non-destructive and accepts the same four input shapes as
`create` (inline content, `SKILL.md`, `.zip`, or a directory whose root
contains `SKILL.md`). Every upload creates a new immutable version and
promotes it to `default_version`; prior versions remain reachable via
`--set-default-version <ver>` or `azd ai skill download --version <ver>`.
For directories, the CLI packages the folder as a zip in memory and
uploads it as multipart/form-data, so the output of
`azd ai skill download` round-trips back through `update` without a
manual zip step.

All commands accept the standard cross-cutting flags: `-p` / `--project-endpoint`,
`--output table|json`, `--no-prompt`, and `--debug`.

## Composing skills in `azure.yaml`

Use the owning extension to add a skill service, then declare the dependency
from each consuming agent:

```bash
azd ai skill add triage-rules \
  --description "Rules for triaging incoming issues" \
  --instructions "Classify the issue, identify its owner, and recommend next steps."
```

```yaml
services:
  triage-rules:
    host: azure.ai.skill
    description: Rules for triaging incoming issues
    instructions: Classify the issue, identify its owner, and recommend next steps.
    license: MIT
    compatibility: gpt-5
    metadata:
      owner: support
    tools:
      - web_search

  support-agent:
    host: azure.ai.agent
    kind: hosted
    name: support-agent
    project: ./agents/support-agent
    image: ghcr.io/example/support-agent:latest
    uses:
      - triage-rules
```

The skill command does not infer which agents consume the skill. Add the skill
service name to each consuming agent's `uses:` list to declare deployment
ordering explicitly.

`instructions` can also reference a `.md` or `.txt` file. To preserve a
complete skill package, use `archive` instead of the inline fields:

```yaml
services:
  triage-rules:
    host: azure.ai.skill
    archive: ./skills/triage-rules
```

`archive` accepts a `.zip` file or a directory with `SKILL.md` at its root.
Relative instruction and archive paths resolve from the service's `project`
path when set, otherwise from the directory containing `azure.yaml`. Parent
traversal (`..`) is rejected.

When `add` receives a ZIP or directory, the source must be inside that service
directory. The command stores a portable forward-slash relative reference and
rejects host-name collisions instead of overwriting another service type.

Deploying the skill creates a new immutable default version and publishes
readiness markers for dependent agent services. A consuming agent must list
the skill service in `uses:` so azd deploys it first. Removing the service from
`azure.yaml` stops azd managing the skill but does not delete it; use
`azd ai skill delete` for deletion.

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
