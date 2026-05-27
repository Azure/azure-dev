---
short: What a Foundry skill is, the versioning model, and the azd ai skill CLI surface.
order: 10
---
# Skill overview

A **Foundry skill** is a reusable behavioral guideline stored centrally on a Foundry project. A Hosted agent **downloads** the skill at build time and the agent runtime **injects** its instructions into every session, guiding the model's behavior without embedding the policy in agent code.

Use skills when behavioral guidance needs to be **versioned, audited, and shared** across multiple Hosted agents in the same project. When the policy changes, update the skill once on Foundry, run `azd ai skill download` again, and redeploy any consuming agent -- no code changes required.

Today, skills are managed through the `azd ai skill` CLI (from the `azure.ai.skills` extension). `azd deploy` does NOT auto-create or update skills on Foundry. You install the extension once, then drive the lifecycle explicitly.

For step-by-step CLI usage, see `manage`. For cross-project sharing, see `share`. For wiring skills into a deployed Hosted agent, see `consume`.

## Install the extension

```bash
azd extension install azure.ai.skills
```

Then `azd ai skill --help` to see the verbs.

## The CLI surface

| Command                                                                              | What it does                                                                                                 |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| `azd ai skill create <name> --description "..." --instructions "..."`                | Create a skill from inline metadata. Sends `inline_content` JSON. `has_blob=false` -- cannot be downloaded.  |
| `azd ai skill create <name> --file ./SKILL.md`                                       | Create a skill from a SKILL.md file (front-matter + body). Sends `inline_content` JSON.                       |
| `azd ai skill create <name> --file ./skill.zip`                                      | Create a skill from a ZIP package. Uploaded as `multipart/form-data`. `has_blob=true` -- can be downloaded.   |
| `azd ai skill create <name> --file <path> --force`                                   | Delete an existing skill (and all its versions) before creating. Refuses on positional/file name mismatch.   |
| `azd ai skill update <name> [--description ... \| --instructions ... \| --file *.md]` | Publishes a new immutable version and promotes it to default. `.zip` is rejected on update -- use `create --force`. |
| `azd ai skill update <name> --set-default-version <ver>`                             | Metadata-only update: re-points `default_version` at an existing immutable version. No new content uploaded.  |
| `azd ai skill show <name>`                                                           | Show `id`, `name`, `description`, `default_version`, `latest_version`, `created_at`.                          |
| `azd ai skill list [--top N] [--orderby <field>]`                                    | List skills in the current Foundry project.                                                                  |
| `azd ai skill download <name> [--version <ver>] [--output-dir <path>] [--raw] [--force]` | Download a skill into `.agents/skills/<name>/` (extracted) or as a single `.zip` (`--raw`).                  |
| `azd ai skill delete <name> [--force]`                                               | Delete a skill and ALL its versions. Irreversible.                                                            |

All commands accept the standard cross-cutting flags: `-p` / `--project-endpoint`, `--output table|json`, `--no-prompt`, and `--debug`.

## Versioning model

Skills are **versioned and immutable**:

* `create` uploads the first default version.
* `update` (with inline flags or `--file *.md`) uploads a **new** immutable version and promotes it to default.
* `update --set-default-version <ver>` re-points `default_version` at an existing version (rollback or fix a previous promotion). No new content is uploaded.
* `delete` removes the whole skill and ALL versions. There is no per-version delete.

The Skill resource itself carries `id`, `name`, `description`, `default_version`, `latest_version`, and `created_at`. Per-version content lives in `inline_content` (description + instructions) or, for `has_blob=true` skills, in the uploaded ZIP.

## Inline vs. package storage (the `has_blob` flag)

| How created                                            | `has_blob` | Downloadable | Use when                                                                                  |
| ------------------------------------------------------ | ---------- | ------------ | ----------------------------------------------------------------------------------------- |
| `create --description ... --instructions ...`          | `false`    | NO           | Quick experiments. JSON-only payload, no sibling assets.                                  |
| `create --file ./SKILL.md`                             | `false`    | NO           | Authoring locally in a SKILL.md but no sibling files yet.                                  |
| `create --file ./skill.zip`                            | `true`     | YES          | Production. ZIP can carry SKILL.md plus referenced files; only `has_blob=true` round-trips. |

`azd ai skill download` returns HTTP 404 for skills with `has_blob=false`. If you need a skill that consumers can pull down via `download`, you must create it from a `.zip`. See `share` for the round-trip recipe.

## Project endpoint resolution

The Foundry project endpoint is resolved in this order on every command:

1. `-p` / `--project-endpoint` flag on the command.
2. Active azd env value `AZURE_AI_PROJECT_ENDPOINT`.
3. Global config `extensions.ai-skills.project.context.endpoint` (falls back to `extensions.ai-agents.project.context.endpoint` so users who already configured the endpoint via the agents extension are not forced to re-run `set`).
4. Host environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error with an actionable suggestion.

## Naming rule

Skill names follow the agentskills.io spec: `^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`, max 64 characters. Validated client-side before any request is sent. Names are case-sensitive on Foundry and serve as the URL path segment for every per-skill request.

## Permissions

Foundry requires the **Foundry User** role on the project. The role was previously named "Azure AI User"; the rename is rolling out but the IDs and permissions are unchanged. Skill CRUD requires this role; reading skills from a deployed Hosted agent does not (the runtime uses the agent's identity).

## Where to go next

* "How do I create / update / download / delete a skill?" -> `manage`
* "How do I share a skill with a teammate or a different project?" -> `share`
* "How does my Hosted agent code load and apply a downloaded skill?" -> `consume`
