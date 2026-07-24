# PM Review: Explicit agent initialization and addition

**Status:** Proposed, awaiting approval

**Tracking issue:** [Azure/azure-dev#8383](https://github.com/Azure/azure-dev/issues/8383)

## Problem statement

`azd ai agent init` currently performs different operations depending on the directory where it runs.

The same command can:

- create a new azd project and its first agent;
- add an agent to an existing project found in the current or a parent directory;
- initialize from local code, an agent manifest, or a project template;
- generate Bicep or Terraform when `--infra` is present.

This contextual behavior makes the command difficult to predict and debug. In particular, a user can intend to create a new project but unknowingly modify a parent project's `azure.yaml`.

## Solution

Separate new-project initialization from adding an agent:

```text
azd ai agent init    Create a new agent project
azd ai agent add     Add an agent to an existing project
```

Keep `--infra` on `init` for backward compatibility:

```text
azd ai agent init <source> --infra       # Initialize, then generate Bicep
azd ai agent init <source> --infra=terraform
azd ai agent init --infra                # Generate IaC for an existing project
```

The command name determines whether agent setup creates or modifies a project. Filesystem detection may improve interactive choices, but it cannot silently switch `init` into `add`.

## Goals

- Make project creation versus modification explicit.
- Prevent `init` from silently adding an agent to a current or parent project.
- Preserve the existing `--infra` workflows.
- Make interactive and non-interactive behavior predictable.
- Return actionable replacement commands when users invoke the wrong operation.
- Preserve existing project configuration when adding another agent.

## Scope

### In scope

- Add `azd ai agent add`.
- Restrict agent setup through `init` to new projects.
- Retain standalone and post-init `--infra` behavior.
- Support explicit sources: template, local code, agent manifest, existing definition, and container image.
- Use one visible source-selection prompt in interactive mode.
- Require an explicit source in non-interactive setup.
- Detect current and parent projects consistently.
- Preserve existing services and shared Foundry configuration when adding an agent.
- Provide compatibility guidance for existing `init` scripts and arguments.

### Non-scope

- Merging a complete sample `azure.yaml` into an existing project. This remains tracked by [#8884](https://github.com/Azure/azure-dev/issues/8884).
- Moving agent addition into core `azd add`.
- Moving `--infra` to a third command.
- Automatically modifying existing Bicep or Terraform to add Foundry resources.
- Changing deployment behavior or when a remote Foundry agent is created.
- Supporting unsafe project shapes such as Aspire projects, multiple Foundry project services, or conflicting shared resources.

## Current behavior

| Context | Current `azd ai agent init` behavior | User risk |
|---|---|---|
| Empty directory | Starts from a template | Mode is inferred from directory state. |
| Non-empty directory | Offers local code or template | The same command follows a different flow. |
| Existing project in current/parent directory | Adds the agent to that project | A parent project can be modified unintentionally. |
| Agent manifest/definition detected | Prompts to reuse it; may auto-accept in no-prompt mode | Adding a file changes command semantics. |
| Existing project plus `--infra` | Generates IaC only | `init` performs no initialization. |
| New project plus `--infra` | Initializes and then generates IaC | Useful workflow that should remain supported. |

## Command contract

### Commands

```text
azd ai agent init [source] [--infra[=bicep|terraform]] [options]
azd ai agent add  [source] [options]
```

### Sources

```text
--template
--from-code <directory>
--manifest <path-or-url>
--definition <path>
--image <registry/image[:tag]>
```

Interactive use may omit the source and select one from a prompt. Non-interactive agent setup must supply a source. Existing-project `init --infra --no-prompt` is the exception because `--infra` completely identifies the requested operation.

### Behavioral rules

| Invocation | Contract |
|---|---|
| `init <source>` with no project | Create a project and its initial agent. |
| `init <source> --infra` with no project | Create the project/agent, then generate Bicep. |
| `init <source> --infra=terraform` with no project | Create the project/agent, then generate Terraform. |
| `init --infra` in an existing compatible project | Generate Bicep only; do not add an agent. |
| `init --infra=terraform` in an existing compatible project | Generate Terraform only; do not add an agent. |
| `init <source>` in an existing project | Fail before mutation and suggest the equivalent `add` command. |
| `init <source> --infra` in an existing project | Fail and suggest `add <source>`, followed by standalone `init --infra`. |
| `add <source>` in an existing project | Add the agent without initializing a project. |
| `add <source>` with no project | Fail and suggest the equivalent `init` command. |
| `add --infra` | Fail because infrastructure is project-wide and remains an init option. |

## Decision table

| Command | Project found | Source supplied | `--infra` | Result |
|---|---:|---:|---:|---|
| `init` | No | Yes | No | Initialize project and agent. |
| `init` | No | Yes | Yes | Initialize, then generate IaC. |
| `init` | No | No | No | Interactive: choose source; non-interactive: fail. |
| `init` | No | No | Yes | Interactive: choose source, initialize, generate IaC; non-interactive: fail. |
| `init` | Yes | No | Yes | Generate IaC only. |
| `init` | Yes | Yes | No | Fail with `add` guidance. |
| `init` | Yes | Yes | Yes | Fail with `add`, then standalone `init --infra` guidance. |
| `add` | Yes | Yes | No | Add agent. |
| `add` | Yes | No | No | Interactive: choose source; non-interactive: fail. |
| `add` | No | Any | No | Fail with `init` guidance. |
| `add` | Any | Any | Yes | Fail; `--infra` is init-only. |

## Architecture

The implementation separates project setup from agent registration:

```text
init + source  -> validate no project -> create project -> register agent
                                                     -> optional --infra

init --infra   -> validate existing project -> generate IaC only

add + source   -> validate existing project -> merge/register agent
```

Both `init` and `add` share source resolution and agent-registration logic. Registration merges the complete service graph instead of replacing existing Foundry services one at a time. The detailed engineering design defines concurrency, retry, and compatibility mechanics.

## Decisions

1. **Add a separate `add` command.** Project modification should be explicit instead of inferred from a parent `azure.yaml`.
2. **Keep `init` as the new-project workflow.** New users still initialize a project and first agent in one command.
3. **Retain `--infra` on init.** It is an explicit, established workflow and avoids a larger breaking change.
4. **Do not automatically redirect `init` to `add`.** Automatic routing would preserve contextual mutation and misleading telemetry.
5. **Require explicit sources in non-interactive setup.** Directory contents must not silently select code, manifest, or template behavior.
6. **Preserve shared project resources.** Adding another agent must not erase existing project settings, deployments, connections, or toolboxes.
7. **Reject unsupported existing-project infrastructure changes.** Existing Bicep/Terraform projects may use already provisioned Foundry resources, but setup will not pretend to modify their IaC.

## Review questions

1. **Is `azd ai agent add` the right command name and location?** Proposed: Yes. It clearly communicates project modification while keeping the change inside the agents extension.

2. **Should `--infra` remain on `init`?** Proposed: Yes. Preserve both post-init and standalone behavior with strict argument validation.

3. **Should `init` ever automatically call `add` when it finds a project?** Proposed: No. Fail before mutation and print the exact replacement command.

4. **Should existing positional and `--src` forms remain temporarily?** Proposed: Yes, with deprecation warnings and deterministic translation to the new source options.

5. **Should non-interactive setup require an explicit source?** Proposed: Yes, except standalone existing-project `init --infra`.

6. **Should adding to existing Bicep/Terraform projects create new Foundry infrastructure?** Proposed: No. Support existing/brownfield Foundry resources only; automatic IaC composition needs a separate design.

7. **Should complete unified-project adoption ship in the first implementation?** Proposed: Yes, for new projects only. Existing-project merge remains out of scope.
