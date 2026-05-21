<!-- cspell:ignore foundry exterrors -->

# Design Spec: `azd ai agent init` Reuse of Existing `agent.yaml`

## 1. Summary

`azd ai agent init` today has two main paths into the existing `azure.ai.agents` extension:

- `-m <manifest>` — pull a manifest pointer and scaffold from it (clones a remote repo when the pointer is remote).
- From existing code — assume the target directory is a blank slate, prompt for model / instructions / entry point, then synthesize an `agent.yaml` and wire `azure.yaml`.

When a user already has an `agent.yaml` (or `agent.manifest.yaml`) sitting in the target directory and runs `azd ai agent init`, the file is treated as a write conflict to overwrite rather than as a source of truth to reuse. The from-code path immediately prompts to overwrite the file (`init_from_code.go:84-104`) and then runs the full interactive scaffold regardless of its contents.

This spec adds detection and reuse so the command short-circuits when an `agent.yaml` is already present:

- If the file is an agent **manifest** (has a top-level `template:`), route through the existing `runInitFromManifest` flow using the local file directly — no clone.
- If the file is an agent **definition** (no `template:`), skip the model/instruction prompts and only wire `azure.yaml`.

Issue: [Azure/azure-dev#7268](https://github.com/Azure/azure-dev/issues/7268).

## 2. Scope and Non-Goals

In scope:

- Detection of `agent.manifest.yaml` / `agent.manifest.yml` / `agent.yaml` / `agent.yml` in the resolved `srcDir` at the start of the from-code action.
- Reuse via the existing manifest pipeline when classified as a manifest.
- Reuse via the existing `addToProject` path when classified as a definition.
- Preserving today's overwrite confirmation when the user explicitly intends to discard the file (covered by the same prompt that already exists today, with a new "reuse" option as the default).
- Behavior under `--no-prompt`.

Out of scope:

- Schema migration of older `agent.yaml` files to the current shape.
- Editing or rewriting the existing file's contents.
- Walking parent directories or sibling dirs to discover `agent.yaml` (only the resolved `srcDir` is scanned).
- Changes to the `-m` remote-clone behavior.
- Changes to the scaffolded-template branches (`init.go:451-484`) — those already call `findAgentManifest` post-scaffold and are unaffected.

## 3. Code Touch Points

All paths are inside `cli/azd/extensions/azure.ai.agents/internal/`.

| Concern | Location | Disposition |
| --- | --- | --- |
| From-code action entry | `cmd/init_from_code.go:49` (`InitFromCodeAction.Run`) | **Modified** — new detect-and-reuse step inserted at the top, replacing the current "overwrite?" branch at `:84-104`. |
| Existing-file overwrite prompt | `cmd/init_from_code.go:84-104` | **Removed** — superseded by the new branching logic. |
| Manifest-vs-definition detection | `cmd/init.go:1057` (`findAgentManifest`), `cmd/init.go:1084` (`looksLikeManifest`) | **Reused as-is**, made package-visible to `init_from_code.go` (they are already in the same package). |
| Manifest path runner | `cmd/init.go:212` (`runInitFromManifest`) | **Reused as-is** — already handles a local file path via `downloadAgentYaml` at `init.go:1213-1215`. |
| Initial flags struct | `cmd/init.go:49` (`initFlags`) | **Reused as-is** — `manifestPointer` is set in-process for the reuse path. |
| Definition load + validate | `pkg/agents/agent_yaml/parse.go:51` (`ExtractAgentDefinition`), `parse.go:361` (`ValidateAgentDefinition`), `parse.go:419` (`ValidateAgentName`) | **Reused as-is**. |
| `azure.yaml` writer (from-code) | `cmd/init_from_code.go:822` (`addToProject`) | **Reused as-is** for the definition path. |
| `azure.yaml` writer (manifest) | `cmd/init.go:1495` (`InitAction.addToProject`) | **Reused as-is** for the manifest path. |
| Definition types | `pkg/agents/agent_yaml/yaml.go:142` (`AgentDefinition`), `yaml.go:211` (`AgentManifest`) | Unchanged. |

The feature is primarily wiring — no new exported APIs.

## 4. Behavior

### 4.1 Detection

When `InitFromCodeAction.Run` starts, immediately after `srcDir` is resolved (currently lines `:76-79`):

1. Call `findAgentManifest(srcDir)` (already iterates `agent.manifest.yaml`, `agent.manifest.yml`, `agent.yaml`, `agent.yml` in that order).
2. If nothing is found → continue with today's from-code scaffold flow (no behavior change).
3. If a file is found → call `looksLikeManifest(path)` to classify and dispatch.

The `os.Stat(filepath.Join(srcDir, "agent.yaml"))` block at `init_from_code.go:84-104` is replaced by this detection step — the same on-disk file that today triggers the "Overwrite?" prompt is now classified and reused.

### 4.2 Manifest classification → reuse `runInitFromManifest`

When `looksLikeManifest` returns `true`:

1. Inform the user: `Detected local agent manifest: <relative-path>. Using local manifest (no clone required).`
2. Set `a.flags.manifestPointer = <absolute-path-to-file>`.
3. Delegate to `runInitFromManifest(ctx, a.flags, a.azdClient, a.httpClient)`.

`downloadAgentYaml` (called inside `runInitFromManifest`) already detects a local filesystem path at `init.go:1213-1215` and reads the bytes directly instead of issuing an HTTP request, so the clone step is automatically skipped. No new branch is needed inside `runInitFromManifest`.

Resources, toolboxes, and connections declared in the manifest are projected into `azure.yaml` by the existing `InitAction.addToProject` (`init.go:1495`).

### 4.3 Definition classification → reuse `addToProject` only

When `looksLikeManifest` returns `false`:

1. Read the file bytes.
2. Unmarshal into `agent_yaml.AgentDefinition` (the same shape parsed by `ExtractAgentDefinition` on the bare-definition fallback branch).
3. Validate via `agent_yaml.ValidateAgentDefinition` and `agent_yaml.ValidateAgentName`.
4. Inform the user: `Detected existing agent definition: <relative-path> (name: <def.Name>).`
5. Skip the entire `createDefinitionFromLocalAgent` prompt sequence (model / instructions / entry-point detection that today runs at `init_from_code.go:108`).
6. Call `a.addToProject(ctx, srcDir, def.Name, def.CodeConfiguration != nil)` — the same call the from-code path makes today at `init_from_code.go:123`.
7. Run `validatePostInit(srcDir, def.CodeConfiguration)` for the same advisory warnings the from-code path emits today.

The reuse path **does not rewrite** the existing `agent.yaml`. The "+ agent.yaml" green-add line printed at `init_from_code.go:128-131` is replaced with a single info line: `Reusing existing agent.yaml (name: <def.Name>).`

### 4.4 Service-name collision in `azure.yaml`

Handled by the existing collision logic inside `addToProject` (manifest path: `init.go:1830`; from-code path: `init_from_code.go:822` and downstream). No new behavior — if `def.Name` (definition path) or the manifest-derived name collides with an existing service entry in `azure.yaml`, the user is prompted today and is still prompted under this change.

### 4.5 `--no-prompt`

The new path is fully non-interactive — detection + classification + reuse require no prompts. Therefore:

- `--no-prompt` + detected manifest → succeeds, runs `runInitFromManifest` with the local path. `runInitFromManifest` itself already honors `--no-prompt` (no new contract).
- `--no-prompt` + detected definition → succeeds, runs `addToProject` and exits with the same "Next steps" message the from-code path prints today (`init_from_code.go:141-146`).
- `--no-prompt` + invalid `agent.yaml` → returns the validation error as a structured `exterrors.Validation(CodeInvalidAgentManifest, ...)` (see § 6).

The current "overwrite declined in no-prompt mode" branch at `init_from_code.go:85-87` is removed — there is no overwrite to decline anymore.

## 5. Interactions with Existing Flags

| Scenario | Behavior |
| --- | --- |
| `-m <pointer>` provided **and** local `agent.yaml` exists | `-m` wins. The from-code action does not run at all when `flags.manifestPointer != ""` (see the routing at `init.go:373-380`). No change to that routing. |
| `--src <dir>` set to a directory that contains `agent.yaml` | Detection runs against that directory (same `srcDir` already used by the from-code flow). |
| Positional arg resolved to a manifest pointer | Same as `-m` — does not enter the from-code action. |
| Scaffolded-template branches (`init.go:451-484`) | Unchanged. Those branches already call `findAgentManifest` post-scaffold and run `runInitFromManifest`. |
| `detectLocalManifest` at `init.go:340` | Unchanged. That helper runs **before** the user picks an init mode and is independent of the from-code action. |

## 6. Errors

No new error codes. The reuse path uses existing codes from `internal/exterrors/codes.go`:

| Failure | Code |
| --- | --- |
| `agent.yaml` exists but parses as neither valid manifest nor valid definition | `exterrors.Validation(CodeInvalidAgentManifest, "<path>: <yaml error>", "Fix the file or remove it to start a fresh init.")` |
| Manifest classification but downstream `LoadAndValidateAgentManifest` fails | Already handled inside `runInitFromManifest` — same code as today. |
| Definition classification but `ValidateAgentDefinition` / `ValidateAgentName` fails | `exterrors.Validation(CodeInvalidAgentManifest, ..., "Fix the agent.yaml and retry.")` (the existing manifest-invalid code is reused for definition validation because the user-facing surface — "your agent.yaml is wrong" — is the same; the `Op` is unchanged). |

Per the extension's error-handling rule, the structured error is created at the from-code action boundary (where category, code, and suggestion are known). Lower-level helpers (`findAgentManifest`, `looksLikeManifest`, the YAML unmarshal) return plain `fmt.Errorf` wraps.

## 7. Test Plan

Unit tests (table-driven, no network) added to `cli/azd/extensions/azure.ai.agents/internal/cmd/init_from_code_test.go`:

- **Manifest detected** — `srcDir` contains an `agent.manifest.yaml` with a `template:` block. Asserts `runInitFromManifest` is invoked with `flags.manifestPointer` set to the absolute local path; no HTTP client calls observed (via a fake `httpClient`).
- **Definition detected** — `srcDir` contains an `agent.yaml` without `template:`. Asserts `createDefinitionFromLocalAgent` is **not** called (today's prompt sequence is skipped), `addToProject` is called once with `agentName == def.Name`, the existing file on disk is byte-identical after the run.
- **No agent yaml** — `srcDir` is empty. Asserts today's path still runs (i.e. `createDefinitionFromLocalAgent` is called) — regression guard.
- **Invalid agent yaml** — `srcDir` contains a syntactically broken `agent.yaml`. Asserts the returned error has code `CodeInvalidAgentManifest` and a non-empty `Suggestion`.
- **Filename precedence** — both `agent.manifest.yaml` and `agent.yaml` present in `srcDir`. Asserts the manifest file wins (matches existing `findAgentManifest` order); a debug log records which file was chosen.
- **`--no-prompt` + definition** — no prompts issued (the fake prompt client errors if any call is made), action returns successfully.

Snapshot regression (existing): `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'` — no `--help` changes expected because no new flags are added; this run is purely a verification step.

End-to-end coverage is not in scope for this change — the from-code action has no functional E2E test today, and adding one solely for this branch would be a larger investment than the feature warrants.

## 8. Impact on Existing Commands

Only `azd ai agent init` changes. The change is additive on a path that today either prompts to overwrite the file or proceeds with redundant prompts that discard the user's existing work. No flag is added or removed. `--help` snapshots do not change.

## 9. Decisions

1. **The existing "Overwrite agent.yaml?" prompt at `init_from_code.go:84-104` is removed, not extended.** Reuse is the new default behavior whenever a parseable `agent.yaml` is present. An overwrite escape hatch via `--force` was considered and rejected: a user who wants to start over can delete the file (one command) rather than learn a flag. Re-running with `-m <pointer>` also bypasses the from-code action entirely.
2. **No new error code.** `CodeInvalidAgentManifest` covers both shapes (manifest and definition) from the user's perspective — they edited `agent.yaml` and got the file wrong. Splitting the code would add telemetry noise without distinguishing actionable user remedies.
3. **No recursion into subdirectories.** Discovery is limited to `srcDir`. A nested `agent.yaml` (e.g. `./src/agent.yaml` when `srcDir == "."`) is invisible to detection; the user must point `--src` at the right directory. Walking would conflict with how `findAgentManifest` is used elsewhere in the codebase and is out of scope.
4. **Definition path does not re-write the file.** The existing on-disk `agent.yaml` is treated as authoritative. If `def.Name` happens to collide with an existing service entry in `azure.yaml`, the collision is resolved by renaming the service entry, not the file.

## 10. Reference: Decision Tree

```text
azd ai agent init (no -m, no positional manifest)
  └── resolve srcDir
        └── findAgentManifest(srcDir)
              ├── not found
              │     └── existing from-code scaffold flow (unchanged)
              ├── found, looksLikeManifest == true
              │     └── flags.manifestPointer = <local path>
              │           └── runInitFromManifest  (no clone; existing path)
              └── found, looksLikeManifest == false
                    └── ValidateAgentDefinition
                          ├── invalid → exterrors.Validation(CodeInvalidAgentManifest, ...)
                          └── valid   → addToProject(srcDir, def.Name, def.CodeConfiguration != nil)
```
