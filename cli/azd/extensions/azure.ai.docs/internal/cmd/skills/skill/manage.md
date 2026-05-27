---
short: Imperative CLI reference for create, update, show, list, download, delete (with Foundry-specific input rules).
order: 20
---
# Skill management (imperative CLI)

`azd ai skill <verb>` commands target the Foundry project directly. They do **not** touch `azure.yaml` and `azd deploy` does **not** create or update skills. Drive the lifecycle explicitly with the commands below.

For mental model and the `has_blob` / versioning story, see `overview`. For sharing skills across projects, see `share`. For wiring a downloaded skill into a Hosted agent, see `consume`.

## Foundry-specific input rules

These bite when you author a SKILL.md or a ZIP. Read them once; the CLI validates most of them client-side before sending a request.

* **YAML front-matter must use unquoted scalars** for `name` and `description`. Quoted values like `name: 'greeting'` cause an HTTP 500 on import. The `azd ai skill create --file *.md` path passes your values through verbatim.
* **Skill name regex**: `^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`, max 64 chars. Validated client-side before any request.
* **SKILL.md size cap**: 1 MiB. The CLI rejects oversized files with `CodeInvalidSkillFile` and suggests splitting content into a `.zip` package instead.
* **ZIP layout for `create --file ./skill.zip`**: `SKILL.md` must be at the **archive root**, not in a subdirectory. The downloaded ZIP returned by `azd ai skill download --raw` follows the same convention. (The `consume` topic covers the different layout used inside a deployed agent's project tree.)
* **Token classes**: only fine-grained PAT (`github_pat_*`) is accepted by the GitHub Copilot SDK sample referenced in `consume`. Classic `ghp_*` tokens are unsupported.

## Create

Three mutually exclusive modes selected by the supplied flags:

```bash
# Inline metadata. has_blob=false; cannot be downloaded.
azd ai skill create greet-user \
  --description "Welcomes a new user" \
  --instructions "Greet the user by name; keep it under two sentences."

# From a SKILL.md (YAML front-matter + Markdown body). has_blob=false.
azd ai skill create greet-user --file ./SKILL.md

# From a ZIP package. has_blob=true; supports download / round-trip.
azd ai skill create greet-user --file ./skill.zip
```

Mode selection:

| `--description` / `--instructions` set | `--file` set | Mode             |
| -------------------------------------- | ------------ | ---------------- |
| Yes                                    | No           | Inline           |
| No                                     | `*.md`       | SKILL.md (inline content uploaded as JSON) |
| No                                     | `*.zip`      | Package          |
| Yes                                    | Yes          | Rejected (mutually exclusive) |
| No                                     | No           | Interactive prompts (text mode) or `CodeMissingRequiredField` error (with `--no-prompt`) |

`--file` extensions other than `.md` or `.zip` are rejected with `CodeInvalidSkillFile`.

### `--force` on create

`--force` deletes any existing skill of the same name (and ALL its versions) before creating. To protect against typos that would wipe an unrelated skill, the CLI **inspects the supplied file's embedded `name`** and refuses `--force` when it disagrees with the positional argument:

* `.md` files: front-matter `name:` is compared to the positional argument.
* `.zip` files: the archive's `SKILL.md` is peeked (without full extraction) and its front-matter `name:` is compared.

The check is skipped (always allowed) in inline mode (no `--file`) and when the file omits `name:` from front-matter.

## Update

Skills are versioned and immutable. `update` either publishes a NEW version or repoints `default_version` at an EXISTING version. It never mutates an existing version's content.

```bash
# New version from inline flags. New version becomes the default.
azd ai skill update greet-user --description "Welcomes a returning user"

# New version from a SKILL.md. New version becomes the default.
azd ai skill update greet-user --file ./SKILL.md

# Metadata-only repoint. No new content uploaded.
azd ai skill update greet-user --set-default-version v3
```

`.zip` is **rejected on update**. Re-uploading a full package requires the `create --force` workaround:

```bash
azd ai skill create greet-user --file ./skill.zip --force
```

This is destructive: it deletes the existing skill and ALL its versions before re-creating. Use it deliberately. The `--force` file-name guard described above also applies here.

## Show

```bash
azd ai skill show greet-user --output json
```

Returns the Skill envelope:

```json
{
  "id": "skill_abc123",
  "object": "skill",
  "name": "greet-user",
  "description": "Welcomes a new user",
  "default_version": "v3",
  "latest_version": "v3",
  "created_at": 1741305600
}
```

`default_version` and `latest_version` only diverge after an `update --set-default-version` repoint to an older version. Per-version content (`inline_content`, `has_blob`) lives on the SkillVersion resource, not on the parent Skill -- if you need it, follow up with the SDK or the raw REST API.

## List

```bash
azd ai skill list --output json
azd ai skill list --top 50 --orderby name --output json
```

Returns `{ object: "list", data: [skill, ...], has_more, first_id, last_id }`. Use `first_id` / `last_id` with the REST API's `after` / `before` query parameters for cursor-based pagination (not currently surfaced by `azd ai skill list`).

## Download

See `share` for the full recipe and safe-extract details. The short form:

```bash
# Default: extract into .agents/skills/greet-user/
azd ai skill download greet-user

# Specific version
azd ai skill download greet-user --version v2

# Single .zip archive (don't extract)
azd ai skill download greet-user --raw --output-dir ./downloads

# Overwrite existing files at the destination
azd ai skill download greet-user --force
```

`download` returns HTTP 404 for skills with `has_blob=false` (created from inline JSON or SKILL.md). Only `has_blob=true` skills round-trip.

## Delete

```bash
azd ai skill delete greet-user --force
```

`--force` skips the confirmation prompt; required for `--no-prompt` runs. Delete removes the skill and **all its versions** -- there is no per-version delete.

## Output formats

Every verb accepts `--output table|json` (default: `json`, except for `show` which defaults to `table` for human consumption). JSON output is the recommended form when piping to a coding agent or to scripting tools.

## Authentication and project endpoint

All requests use `Bearer` tokens from `DefaultAzureCredential` with scope `https://ai.azure.com/.default`. The CLI handles token acquisition transparently; you only need to be signed in via `azd auth login`.

The project endpoint comes from `-p` / `--project-endpoint`, then `AZURE_AI_PROJECT_ENDPOINT`, then global config (skills + agents fallback), then `FOUNDRY_PROJECT_ENDPOINT` -- see `overview` for the full list.

## Debug logging

`--debug` writes to `azd-ai-skills-<date>.log` in the current working directory. The CLI deliberately opts OUT of HTTP body logging until a sanitizer is in place that redacts user-authored `description` and `instructions` fields, so the log shows headers and status codes but never the JSON payload.
