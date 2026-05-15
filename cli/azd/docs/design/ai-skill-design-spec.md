<!-- cspell:ignore exterrors foundry gzip tarball zipslip orderby foundrysdk -->

# Design Spec: `azd ai agent skill` Commands

## 1. Summary

This spec covers the Foundry Skill commands that ship in the `azure.ai.agents`
extension:

- `azd ai agent skill create <name>` (three mutually exclusive input modes).
- `azd ai agent skill update <name>` (in-place metadata or body update).
- `azd ai agent skill show <name>` (metadata only).
- `azd ai agent skill list` (paginated).
- `azd ai agent skill download <name>` (default extracts the server gzip; `--raw` keeps it).
- `azd ai agent skill delete <name>` (confirmation by default; `--force` to skip).

These commands are pure CLI integration on top of the existing Foundry Skills
data plane. No new server work is required.

A Skill on the Foundry platform is a reusable behavioral guideline that an
agent can attach at runtime. The Skill payload is either inline JSON
(description + Markdown instructions) or a packaged gzip tarball that bundles
`SKILL.md` plus any sibling assets the skill ships.

## 2. Scope and Non-Goals

In scope:

- The six subcommands above and their flag surface.
- File-input handling for `--file`: `SKILL.md` and packaged archives on
  `create`, `SKILL.md` on `update`.
- Gzip extraction behavior for `download`, including safe extraction guarantees.
- Reuse of the existing endpoint-resolution cascade and cross-cutting flags.

Out of scope:

- Any change to the Foundry Skill REST surface.
- Wiring a skill into an agent build. `download` writes files to disk; what
  the developer does with them is their responsibility.
- Tracking `--file` as a manifest. The file is read at invocation time only.

## 3. Extension Placement and Surface

The skill subtree lives inside the existing `azure.ai.agents` extension (no
new module, no change to `registry.json`). Commands surface as
`azd ai agent skill <verb>`. The layout is designed so that a future move to a
standalone `azd ai skill` extension is registration-only with no behavior diff.

Internally, files land under
`cli/azd/extensions/azure.ai.agents/internal/cmd/skill_*.go`, mirroring the
`project_*.go` layout. A new typed client lives at
`internal/pkg/agents/skill_api/` so the agent client is not overloaded with
skill operations.

## 4. Endpoint Resolution and Cross-Cutting Flags

All skill commands resolve the Foundry project endpoint via the standard
5-level cascade already implemented for the extension:

1. `-p` / `--project-endpoint` flag on the invoked command.
2. Active azd env value (`AZURE_AI_PROJECT_ENDPOINT`).
3. Global config under `extensions.ai-agents.project.context.endpoint`
   in `~/.azd/config.json`.
4. Host environment variable `FOUNDRY_PROJECT_ENDPOINT`.
5. Structured error with an actionable suggestion.

Each skill command accepts the cross-cutting flag set: `-p`, `--output`,
`--no-prompt`, and `--debug`. Resource-returning commands default to `json`
and allow `table` as an opt-in view. Verb-specific flags layer on top.

## 5. Data-Plane Surface

| Verb | HTTP method | Path | Notes |
|------|-------------|------|-------|
| Create (inline / parsed) | POST | `/skills` | JSON body |
| Create (package) | POST | `/skills:import` | `application/gzip` body |
| Show | GET | `/skills/{name}` | Metadata only |
| Update (inline / parsed) | POST | `/skills/{name}` | JSON body |
| List | GET | `/skills` | Paginated; supports `top`, `orderby`, `skip`, etc. |
| Delete | DELETE | `/skills/{name}` | |
| Download | GET | `/skills/{name}:download` | Returns `application/gzip` |

Auth uses the same bearer-token policy the existing agent client uses
(scope `https://ai.azure.com/.default`). The User-Agent header carries the
extension version, consistent with `agent_api`.

## 6. Command Behavior

### 6.1 `azd ai agent skill create <name>`

Three mutually exclusive input modes. The CLI rejects (does not silently
merge) any combination that supplies more than one mode.

| Mode | Flags | Wire format |
|------|-------|-------------|
| Inline | `--description <text> --instructions <markdown>` | `POST /skills` (JSON) |
| File: `SKILL.md` | `--file ./path/to/SKILL.md` | `POST /skills` (JSON, CLI parses YAML front matter + body) |
| File: gzip package | `--file ./path/to/skill.tar.gz` | `POST /skills:import` (`application/gzip`) |

Flag and argument shape:

| Flag | Type | Required | Notes |
|------|------|----------|-------|
| `<name>` | positional | yes | Validated per §6.7. |
| `--description` | string | inline mode only | Plain text; max length matches server limit. |
| `--instructions` | string | inline mode only | Markdown body. |
| `--file` | path | file modes only | Mode picked from extension (`.md`, `.tar.gz`, or `.tgz`). Must exist and be readable; validated before any network call. |
| `--force` | bool | no | When the name already exists, replace it (delete-then-create). |
| `-p`, `--output`, `--no-prompt`, `--debug` | | no | Cross-cutting (§4). |

Mode selection logic:

1. If both `--file` and (`--description` or `--instructions`) are supplied,
   exit non-zero with a validation error pointing at the conflicting flags.
2. If only `--file` is supplied, branch on extension:
   - `.md`: parse as `SKILL.md`, build the JSON body locally, send to
     `POST /skills`.
   - `.tar.gz` / `.tgz`: stream the file bytes to `POST /skills:import` with
     `Content-Type: application/gzip`. The CLI does not inspect the contents of
     the archive before upload, so server-side validation owns the package contents.
   - Any other extension is rejected with a validation error before any
     network call.
3. If only inline flags are supplied, both `--description` and `--instructions`
   are required. Missing either is a validation error.
4. If neither mode is supplied:
   - With prompting enabled (TTY, `--no-prompt` not set): prompt for description
     and instructions interactively.
   - With `--no-prompt` set: exit non-zero with a structured error listing the
     missing inputs.

`SKILL.md` parsing:

- The file must start with a YAML front matter block delimited by `---`.
- The CLI extracts `name`, `description`, and any other metadata fields the
  Skills service recognizes. The remaining Markdown body becomes
  `instructions`.
- If the front matter `name` is present and differs from the positional
  `<name>`, the positional wins and a one-line warning is printed to stderr.
  Suppressed under `--no-prompt` or `--output json`.
- If the front matter is missing or unparsable, the command fails with a
  validation error that points to the problematic line.

`--force` semantics on create:

- Without `--force`: a name collision returns a `409`-shaped structured error
  with the suggestion to use `--force` or `update`.
- With `--force`: the CLI issues a `DELETE` followed by the appropriate
  create call. The two requests are not transactional; if the delete succeeds
  but the create fails, the original skill is gone and the error message says so.

### 6.2 `azd ai agent skill update <name>`

Flags:

| Flag | Type | Notes |
|------|------|-------|
| `<name>` | positional, required | The skill on the server. |
| `--description` | string | Optional. |
| `--instructions` | string | Optional. |
| `--file` | path | Mutually exclusive with `--description` / `--instructions`. |

Behavior:

1. The CLI GETs the current skill, merges omitted fields locally, then POSTs
   the merged payload to `/skills/{name}`.
2. `--file` accepts `.md` only. The CLI parses front matter and body into the
   JSON update payload. `.tar.gz` / `.tgz` on `update` is rejected with a
   validation error suggesting `create --force`.
3. If no field flags and no `--file` are supplied, the command exits non-zero
   with a validation error.
4. If the skill does not exist, the initial GET returns 404 and the command
   fails with a "not found" error.

`--force` is not available on `update`; the target skill must already exist.

### 6.3 `azd ai agent skill show <name>`

Returns metadata only. The Skill body lives behind `download`; `show` keeps
its output focused so that humans and coding agents can scan it without
streaming kilobytes of Markdown to the terminal.

JSON output is the verbatim metadata returned by the service. Table output
prints a fixed key/value layout (`Name`, `Description`, `Created`, `Updated`,
plus any additional first-class fields the service exposes).

### 6.4 `azd ai agent skill list`

Flags:

| Flag | Type | Notes |
|------|------|-------|
| `--top` | int | Maximum number of results to return. |
| `--orderby` | string | Forwarded to the service. |
| Cross-cutting | | See §4. |

Behavior:

1. Without `--top`, the CLI iterates all pages transparently into one flat list.
   With `--top`, it fetches up to that many items and stops.
2. JSON output emits a top-level array (pagination hidden). Table output
   prints columns `NAME`, `DESCRIPTION`, `UPDATED` (long descriptions truncated).
3. Pagination errors mid-iteration surface as a normal error; partial results
   are not printed.

### 6.5 `azd ai agent skill download <name>`

Flags:

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `<name>` | positional, required | | |
| `--output-dir` | path | `./.agents/skills/<name>/` (resolved against the current working directory) | Where to write the extracted files (or the raw archive). |
| `--raw` | bool | `false` | Skip extraction; write the gzip archive as-is. |
| `--force` | bool | `false` | Overwrite existing files in the output directory. |

Default behavior (no `--raw`):

1. Stream the gzip response body to a temp file inside the OS temp directory.
2. Verify the response Content-Type starts with `application/gzip` (or the
   equivalent server-confirmed content type). Otherwise, fail with a clear
   error.
3. Walk the tar entries once to validate the complete archive and build an
    extraction plan before writing to `--output-dir`. For each entry:
   - Reject absolute paths and any path containing `..` components (zip-slip guard).
   - Reject symlinks, hard links, and entries other than regular files and directories.
   - Without `--force`, refuse to overwrite existing files (exit non-zero,
     listing the colliding path).
4. Extract into a staging directory first; copy to `--output-dir` only after
   full validation. A failed extraction must not write any files to `--output-dir`.
5. Files use the user's umask. Executable bits from the tar are dropped.
6. Decompression is bounded at 10,000 entries / 512 MB uncompressed.
   Exceeding either limit aborts with `CodeSkillArchiveUnsafe`.

`--raw` behavior:

1. The CLI ensures `--output-dir` exists.
2. The gzip archive is written to `<output-dir>/<name>.tar.gz`. Without
   `--force`, an existing file of the same name causes a clean error.

`--output-dir` resolution:

- Relative paths resolve against cwd, not the azd project root.
- Default `./.agents/skills/<name>/` groups downloads under one directory.
  Projects can add `.agents/` to `.gitignore`.

### 6.6 `azd ai agent skill delete <name>`

Flags:

| Flag | Default | Notes |
|------|---------|-------|
| `--force` | `false` | Skip the confirmation prompt. |
| Cross-cutting | | See §4. |

Behavior:

1. Interactive mode: prompt `Delete skill "<name>"? [y/N]:`. Declining returns
   exit 0 (not `exterrors.Cancelled`). JSON mode emits the cancelled shape from §7.
2. `--no-prompt` without `--force`: refuse to delete (exit non-zero).
3. `--force`: skip the prompt regardless of TTY state.
4. Non-existent skill: 404 surfaced as a "not found" error.

### 6.7 Skill Name Validation

The CLI validates `<name>` on every command that takes it:

- Non-empty after trim.
- Matches the service-documented regex, or the conservative fallback
  `^[a-zA-Z][a-zA-Z0-9-_]{0,62}$`. The service makes the final decision.

## 7. Output Contracts

JSON shapes for all commands with `--output json` are part of the public
contract and must not change without a deprecation step. The shapes:

- `create`: passthrough of the created `Skill` resource returned by the service.
- `update`: passthrough of the updated `Skill` resource returned by the service.
- `show`: passthrough of the service `Skill` resource.
- `list`: a top-level array of `Skill` resources. Pagination is hidden.
- `download`: `{ "skill": "<name>", "outputDir": "<absolute path>",
   "files": ["<relative path>", ...], "raw": false }` on extraction;
   `{ "skill": "<name>", "outputDir": "<absolute path>",
   "archive": "<relative path>", "raw": true }` with `--raw`.
- `delete`: `{ "deleted": true, "name": "<name>" }`. Cancelled (interactive
  `n`) emits `{ "deleted": false, "cancelled": true, "name": "<name>" }`
  with exit code `0`.

Errors use `exterrors` so the azd host renders them consistently with the
rest of the extension. Reuse existing codes such as `CodeConflictingArguments`
and `CodeInvalidParameter` for generic flag problems. New skill-specific error
codes added by this work:

- `CodeInvalidSkillName`
- `CodeInvalidSkillFile` (front matter parse failure, unsupported extension)
- `CodeSkillArchiveUnsafe` (rejected tar entry on extraction)
- `CodeSkillOutputCollision` (file exists, `--force` not set)

These plug into the existing `exterrors.Validation` factory.

## 8. Test Plan

Unit tests (no network):

- Mode-selection matrix for `create`: every combination of inline flags and
  `--file` produces the expected outcome (success, conflict error, missing
  field error).
- `SKILL.md` parsing: valid front matter, missing front matter, invalid
  YAML, name-mismatch warning suppression under `--no-prompt` and
  `--output json`.
- Pagination flattening for `list`: multi-page server response collapses into
  one array in JSON output and one continuous table in table output.
- `download` extraction safety: tar entries containing `..`, absolute paths,
  symlinks, and oversized entries are all rejected and leave the output
  directory untouched.
- `download --raw` writes exactly the bytes returned by the service.
- `delete` cancellation paths: interactive `n`, `--no-prompt` without
  `--force`, and `--force` all produce the documented exit codes and output;
  interactive `n` is a successful no-op, not an `exterrors.Cancelled` error.
- Endpoint resolver: skill commands inherit the resolver and produce the
  documented suggestion when nothing resolves.

End-to-end (against a recorded fixture):

- `create --file ./SKILL.md` then `show` then `download` round-trip; assert
  the downloaded `SKILL.md` content equals the uploaded content.
- `create --file ./skill.tar.gz` then `download` (extract) then `download
  --raw`; assert both forms are consistent.
- `list` with more than one page of results.

## 9. Impact on Existing Commands

No existing command's behavior, flags, or resolver logic changes. The new
`skill_api` package is a sibling of `agent_api`; the existing `AgentCardSkill`
type (agent-card capability) is unrelated and has no symbol conflict.

## 10. Telemetry

One event per command, reusing the extension's existing telemetry surface:

- `azd.ai.agent.skill.create` (`mode`: `inline` / `file-md` / `file-gzip`,
  `forced`: bool).
- `azd.ai.agent.skill.update` (`mode`: as above, `fieldsTouched`: count).
- `azd.ai.agent.skill.show` / `azd.ai.agent.skill.list` (`resolvedSource`).
- `azd.ai.agent.skill.download` (`raw`: bool, `forced`: bool, `extractedFileCount`).
- `azd.ai.agent.skill.delete` (`forced`: bool, `cancelled`: bool).

No skill names, no descriptions, no instructions, no file paths, no project
endpoint values are emitted. Project-endpoint hostnames, if needed for
debugging, are hashed.

## 11. Security Considerations

- **Tar extraction.** Full rejection rules and decompression limits in §6.5.
- **File write permissions.** User's umask; executable bits dropped.
- **Auth.** Reuses the existing bearer-token pipeline; no new secret writes.
- **Argument echoing.** Debug logger sanitizes request bodies (same as `agent_api`).

## 12. Reference: Command Summary

```bash
# Create (three mutually exclusive modes)
azd ai agent skill create <name> --description "..." --instructions "..." \
  [-p <url>] [--output table|json] [--no-prompt] [--debug] [--force]
azd ai agent skill create <name> --file ./SKILL.md \
  [-p <url>] [--output table|json] [--no-prompt] [--debug] [--force]
azd ai agent skill create <name> --file ./skill.tar.gz \
  [-p <url>] [--output table|json] [--no-prompt] [--debug] [--force]

# Update (any subset of fields; --file accepts .md only, mutually exclusive with inline flags)
azd ai agent skill update <name> [--description "..."] [--instructions "..."] \
  [--file <path>] [-p <url>] [--output table|json] [--no-prompt] [--debug]

# Show / list / delete
azd ai agent skill show <name>   [-p <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent skill list          [--top N] [--orderby <field>] \
                                 [-p <url>] [--output table|json] [--no-prompt] [--debug]
azd ai agent skill delete <name> [--force] [-p <url>] [--output table|json] \
                                 [--no-prompt] [--debug]

# Download (default extracts; --raw keeps the gzip archive)
azd ai agent skill download <name> [--output-dir <path>] [--raw] [--force] \
                                   [-p <url>] [--output table|json] [--no-prompt] [--debug]
```

Resolution cascade: see §4.
