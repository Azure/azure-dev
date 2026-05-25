<!-- cspell:ignore foundry exterrors -->

# Design Spec: `azd ai agent init` Reuse of Existing `agent.yaml`

Issue: [Azure/azure-dev#7268](https://github.com/Azure/azure-dev/issues/7268).

## 1. Summary

The from-code path of `azd ai agent init` today treats a pre-existing `agent.yaml` in the target directory as a write conflict: interactively it prompts the user to overwrite, and in `--no-prompt` mode it fails fast with `CodeInvalidAgentManifest: "agent.yaml already exists at ..."` (added in #8266).

This change adds **definition reuse**: when the user has a bare `agent.yaml` (a YAML file without a top-level `template:` wrapper) in the target directory and runs `azd ai agent init`, the command treats that file as the source of truth, skips the from-code prompts, and writes only the `azure.yaml` service entry, matching the issue's wording: *"if it's a definition, then we have less to ask and just setup azure.yaml."*

Manifest reuse (the other half of the issue) is already handled by upstream `detectLocalManifest` in `init.go`, which scans the four candidate filenames (`agent.manifest.yaml`, `agent.manifest.yml`, `agent.yaml`, `agent.yml`) and accepts whichever one parses as a valid manifest. This spec does **not** touch that path; it only fills the bare-definition gap.

## 2. Scope and Non-Goals

In scope:

- Detection of `agent.manifest.yaml` / `agent.manifest.yml` / `agent.yaml` / `agent.yml` in the resolved source directory at the top of `RunE`, *after* `detectLocalManifest` has had the first chance to claim the file.
- A new reuse path that validates the file as a bare `AgentDefinition`, ensures an azd environment exists, and calls the existing from-code `addToProject` helper to write the `azure.yaml` service entry.
- Structured error when the file is present but does not parse as a valid bare definition (covers malformed YAML and manifest-shaped files that failed upstream validation).
- Confirmation prompt symmetric to `detectLocalManifest` ("An existing agent definition was found at ... Use it?") that auto-confirms under `--no-prompt`.

Out of scope:

- **Manifest reuse.** Already covered by upstream `detectLocalManifest`.
- **Foundry project resolution.** The issue limits the definition path to "just setup azure.yaml". Users who need a Foundry project bound for `azd deploy` must either delete the definition and rerun init interactively, set `AZURE_AI_PROJECT_ID` in their azd environment by hand, or use the manifest pipeline instead. Project resolution for definitions is tracked as a follow-up.
- **Model deployment selection** (same reason).
- **Editing or rewriting the existing file.** The on-disk `agent.yaml` is authoritative.
- Schema migration, recursion into subdirectories, changes to `-m` semantics, or changes to the scaffolded-template branches.

## 3. Code Touch Points

All paths inside `cli/azd/extensions/azure.ai.agents/internal/`.

| Concern | Location | Disposition |
| --- | --- | --- |
| Top-level dispatch | `cmd/init.go` `RunE` | **Modified**: a new detection+reuse block is inserted alongside the existing `detectLocalManifest` block. Triggers before the init-mode prompt, so users with a local `agent.yaml` skip both the mode prompt and the from-code scaffolding sequence. |
| In-action overwrite guard | `cmd/init_from_code.go` (`InitFromCodeAction.Run`) | **Modified**: the original "agent.yaml already exists" guard (#8266) is retained but uses `findExistingAgentYaml` so it covers all four candidate filenames. The guard now only fires when the scaffold path was entered despite a definition being on disk — e.g. when the user declined the reuse prompt and then picked "Use the code in the current directory". |
| Upstream manifest detection | `cmd/init_from_templates_helpers.go` (`detectLocalManifest`, called from `cmd/init.go` `RunE`) | **Unchanged**. |
| Manifest pipeline | `cmd/init.go` (`runInitFromManifest`) | **Unchanged**. |
| Definition reuse helpers | `cmd/init_from_code_reuse.go` (new file) | `findExistingAgentYaml`, `runReuseDefinition`, `loadAgentDefinitionFile`. |
| `azure.yaml` writer | `cmd/init_from_code.go` (`addToProject`) | **Reused as-is** by `runReuseDefinition`. Language detection inside it is limited to the bare-definition filenames so a leftover manifest cannot short-circuit detection. |
| Project / environment bootstrap | `cmd/init.go` (`ensureProject`, `getExistingEnvironment`); `cmd/init_foundry_resources_helpers.go` (`createNewEnvironment`) | **Reused as-is**: the reuse path runs the same setup `runInitFromManifest` does. |
| Definition types | `pkg/agents/agent_yaml/yaml.go` (`ContainerAgent`, `CodeConfiguration`) | Unchanged. |
| Definition validation | `pkg/agents/agent_yaml/parse.go` (`ValidateAgentDefinition`) | Reused. Catches missing/invalid `kind`, name format, and kind-specific structural checks. |

The feature is primarily wiring. No new exported APIs.

## 4. Behavior

### 4.1 Top-level dispatch order in `RunE`

After auth and project resolution, when no `-m` was passed:

1. `detectLocalManifest(srcDir)`: existing helper, runs first. If it returns a valid manifest path, `flags.manifestPointer` is set (with an optional confirmation prompt in interactive mode) and the manifest pipeline runs as today.
2. **If `detectLocalManifest` returned no valid manifest at all** (not merely "user declined the prompt"), the new step runs: `findExistingAgentYaml(srcDir)` does a shallow `os.Stat` against the four candidate filenames. Any hit at this point is either a bare definition (rejected by `detectLocalManifest` for lacking `template:`) or a malformed manifest (rejected for failing manifest validation).
   - **Edge case**: when `detectLocalManifest` found a *valid* manifest but the user declined the reuse prompt, the reuse scan is skipped. Otherwise we would mis-classify the declined manifest as an "invalid definition" and block init with `CodeInvalidAgentManifest`, contradicting the user's choice to start fresh. The implementation tracks this with a `manifestDetectedButDeclined` flag set in the `detectLocalManifest` branch.
3. On a hit, a confirmation prompt ("An existing agent definition was found at ... Use it?") is shown; in `--no-prompt` mode the answer auto-defaults to yes.
4. On confirmation, `runReuseDefinition(ctx, flags, azdClient, httpClient, srcDir, existingPath)` is called and the command returns. The init-mode prompt and the from-code scaffolding sequence are both skipped.

### 4.2 `runReuseDefinition`

The new free function lives in `init_from_code_reuse.go`. It performs:

1. `loadAgentDefinitionFile(path)`: reads the file, rejects anything with a top-level `template:` (manifest-shaped but invalid, produces a targeted error), runs `agent_yaml.ValidateAgentDefinition` on the bytes (catches missing/invalid `kind`, name format, kind-specific structural checks), then unmarshals to `agent_yaml.ContainerAgent` so `CodeConfiguration` is preserved.
2. Prints `Detected existing agent definition: <relative-path> (name: <def.Name>).`
3. Bootstraps project + env using the same helpers `runInitFromManifest` uses: `ensureProject`, then `getExistingEnvironment` / `createNewEnvironment` (the env is named `sanitizeAgentName(<def.Name> + "-dev")` when none was supplied via `-e`).
4. Mirrors `InitFromCodeAction.Run`'s absolute-`--src` handling: when `flags.src` is absolute, converts it to a path relative to the azd project root so `azure.yaml`'s `RelativePath` stays portable.
5. Builds a thin `*InitFromCodeAction` with the bootstrapped pieces and calls `action.addToProject(ctx, srcDir, def.Name, def.CodeConfiguration != nil)`.
6. Prints `Reusing existing <displayPath> (name: <def.Name>).` where `displayPath` is the actual detected filename (so users with `agent.yml` see `agent.yml`, not a hardcoded `agent.yaml`).
7. Calls `validatePostInit(srcDir, def.CodeConfiguration)` for the same advisory warnings the scaffold path emits.

The function deliberately does **not** call `configureModelChoice` or any Foundry resource resolution. The on-disk file is never rewritten.

### 4.3 Invalid file

When `loadAgentDefinitionFile` returns any error (broken YAML, manifest-shaped file that did not route upstream, missing/invalid kind or name), `runReuseDefinition` wraps it in a structured error:

```go
exterrors.Validation(
    exterrors.CodeInvalidAgentManifest,
    fmt.Sprintf("agent definition in %s is invalid: %s", displayPath, err),
    fmt.Sprintf("Fix %s and retry, or remove the file to start a fresh init.", displayPath),
)
```

`azure.yaml` is **not** mutated.

### 4.4 Service-name collision in `azure.yaml`

Handled by the existing collision logic inside `addToProject`. No new behavior.

### 4.5 `--no-prompt`

- `--no-prompt` + valid bare `agent.yaml` -> succeeds, runs `addToProject`. The confirmation prompt auto-defaults to yes (matches `detectLocalManifest` behavior).
- `--no-prompt` + invalid file -> returns the structured `exterrors.Validation` above.
- `--no-prompt` + no `agent.yaml` -> unchanged.

The original `#8266` fail-fast guard inside `InitFromCodeAction.Run` is retained as a safety net for paths that bypass the `RunE`-level reuse dispatch (see § 4.6).

### 4.6 Decline-reuse fallback into the from-code scaffold

When the user declines the `RunE` reuse prompt and then picks "Use the code in the current directory" from the init-mode prompt, `InitFromCodeAction.Run` is entered with the existing `agent.yaml` still on disk. Without a guard, the from-code scaffold would silently overwrite it.

The guard inside `InitFromCodeAction.Run` therefore:

- In `--no-prompt` mode (defensive — not normally reached because `--no-prompt` auto-accepts the reuse upstream): returns `exterrors.Validation(CodeInvalidAgentManifest, "<displayPath> already exists at \"<path>\"", "delete or move the existing <displayPath>, or run interactively to confirm overwrite")`.
- Interactively: prompts `An agent definition already exists at "<displayPath>". Overwrite?` with default-no. Declining returns `exterrors.Cancelled`.

This restores the safety invariant from #8266 while keeping the happy path (file gets reused) lossless.

## 5. Interactions with Existing Flags

| Scenario | Behavior |
| --- | --- |
| `-m <pointer>` provided **and** local `agent.yaml` exists | `-m` wins. The new reuse block runs only when `flags.manifestPointer == ""`. |
| Local `agent.manifest.yaml` (or any agent yaml that parses as a valid manifest) exists, no `-m` | Caught by upstream `detectLocalManifest`. The reuse block in this spec is not entered. |
| Local bare `agent.yaml` (no `template:`) exists, no `-m` | This spec's reuse block runs. |
| `--src <dir>` set | Detection runs against that directory. Absolute paths are normalized to project-relative before `addToProject` is called. |
| `--no-prompt` + bare `agent.yaml` | Auto-yes on the confirmation, proceeds with reuse. |
| User declines reuse prompt, then picks "Use the code in the current directory" | Hits the `InitFromCodeAction.Run` overwrite guard (§ 4.6); interactive overwrite prompt defaults no, `--no-prompt` returns `CodeInvalidAgentManifest`. |

## 6. Errors

No new error codes. The reuse path uses existing codes from `internal/exterrors/codes.go`:

| Failure | Code |
| --- | --- |
| File present but unparseable as a bare definition (broken YAML, manifest-shaped, missing/invalid kind, invalid name) | `exterrors.Validation(CodeInvalidAgentManifest, "agent definition in <displayPath> is invalid: <err>", "Fix <displayPath> and retry, or remove the file to start a fresh init.")` |
| Existing definition present when the scaffold path is entered, `--no-prompt` mode (§ 4.6 fallback) | `exterrors.Validation(CodeInvalidAgentManifest, "<displayPath> already exists at \"<path>\"", "delete or move the existing <displayPath>, or run interactively to confirm overwrite")` |
| User declines the interactive overwrite prompt (§ 4.6) | `exterrors.Cancelled("<displayPath> already exists; overwrite declined")` |

Per the extension's error-handling rule, the structured error is created at the boundary inside `runReuseDefinition` (where category, code, and suggestion are known). Lower-level helpers (`findExistingAgentYaml`, `loadAgentDefinitionFile`) return plain `fmt.Errorf` wraps.

## 7. Test Plan

- **Unit tests** in `cli/azd/extensions/azure.ai.agents/internal/cmd/init_from_code_reuse_test.go`:
  - `TestFindExistingAgentYaml`: filename precedence (manifest before definition), empty dir, directory-entries-ignored, shallow-scan.
  - `TestLoadAgentDefinitionFile`: happy path (bare definition + `CodeConfiguration` preserved), rejects manifest-shaped file, rejects missing `kind`, rejects invalid name, rejects broken YAML.
  - `TestRunReuseDefinition_InvalidFileReturnsStructuredError` and `TestRunReuseDefinition_RejectsManifestShapedFile`: cover the failure paths that short-circuit before any azd gRPC calls and so don't need a full client mock.
- **Pre-existing unit tests** in the same package continue to pass; they cover helpers (`sanitizeAgentName`, `writeDefinitionToSrcDir`, etc.) that this change does not modify.
- **Manual e2e** against the locally-built `azd` + extension. Three scenarios:
  - **Definition reuse**: write a bare `agent.yaml`, run `azd ai agent init --no-prompt`; assert no init-mode prompt, `Detected existing agent definition: ...` printed, `azure.yaml` contains the agent's `name:`, on-disk `agent.yaml` byte-identical to input.
  - **Manifest reuse**: write `agent.manifest.yaml`, run with `AZURE_SUBSCRIPTION_ID` and `AZURE_AI_PROJECT_ID` set; assert the existing upstream manifest path runs unchanged and `azure.yaml` ends up with the manifest's `template.name`.
  - **Invalid yaml**: write broken YAML; assert non-zero exit, error references the actual filename, `azure.yaml` not mutated.

## 8. Impact on Existing Commands

Only `azd ai agent init` changes:

- Users with a bare `agent.yaml` no longer see the init-mode prompt or the from-code prompt sequence.
- Users in `--no-prompt` mode with a bare `agent.yaml` no longer get the `agent.yaml already exists at ...` failure from #8266 (the reuse path auto-accepts and proceeds).
- Users with a valid manifest are unaffected (upstream `detectLocalManifest` handles them as before).
- Users with no `agent.yaml` are unaffected.
- The #8266 safety guard inside `InitFromCodeAction.Run` is preserved as a fallback for the decline-reuse path (§ 4.6).

No flags added or removed; no `--help` snapshot changes.

## 9. Decisions

1. **Dispatch at `RunE`, not inside `InitFromCodeAction.Run`.** Placing the detection alongside `detectLocalManifest` keeps the manifest and definition reuse paths symmetric, and means users with a local `agent.yaml` are not asked to pick an init mode first. An earlier draft placed the dispatch inside `InitFromCodeAction.Run`, which forced users through the mode prompt before the reuse could fire.
2. **No Foundry project resolution in the definition path.** The issue explicitly limits this case to "just setup azure.yaml". Adding project resolution would either duplicate the manifest pipeline's logic or require a non-trivial refactor of `runInitFromManifest` to support an in-memory manifest input; it is tracked as a follow-up.
3. **No new error code.** `CodeInvalidAgentManifest` covers both shapes from the user's perspective. Splitting the code would add telemetry noise without distinguishing actionable user remedies.
4. **No recursion into subdirectories.** Discovery is limited to the resolved source directory.
5. **Definition path does not re-write the file.** The on-disk `agent.yaml` is authoritative. If `def.Name` collides with an existing service entry in `azure.yaml`, the existing collision logic resolves it by renaming the service entry, not the file.
6. **Manifest reuse is left to upstream `detectLocalManifest`.** Discovered during implementation: valid local manifests are already intercepted upstream and routed through `runInitFromManifest`. A duplicate dispatch would be dead code. `findExistingAgentYaml` still includes the manifest filenames in its scan so a malformed manifest-shaped file produces a targeted error instead of silently scaffolding.
7. **Full schema validation, not just name validation.** `loadAgentDefinitionFile` calls `agent_yaml.ValidateAgentDefinition` (not just `ValidateAgentName`) so missing/invalid `kind`, name format, and kind-specific structural checks fail fast with the same error the manifest pipeline produces.
8. **Retain the #8266 fail-fast guard inside `InitFromCodeAction.Run`.** An earlier draft removed it on the assumption that the `RunE`-level dispatch would always handle the reuse case. That assumption fails when the user declines the reuse prompt and then explicitly picks "Use the code in the current directory" — without the guard, the from-code scaffold would silently overwrite the user's file. The guard now uses `findExistingAgentYaml` so it covers all four candidate filenames.

## 10. Reference: Decision Tree

```text
azd ai agent init (no -m, no positional manifest)
  |
  +-- detectLocalManifest(srcDir)                                  [unchanged]
  |     |
  |     +-- valid manifest found
  |     |     +-- user accepts (or --no-prompt)
  |     |     |     +-- flags.manifestPointer = <local path>
  |     |     |           +-- runInitFromManifest                  [existing path]
  |     |     +-- user declines (interactive only)
  |     |           +-- set manifestDetectedButDeclined; fall through
  |     +-- nothing valid -> fall through
  |
  +-- findExistingAgentYaml(srcDir)                                [new, skipped if manifestDetectedButDeclined]
        |
        +-- not found -> init-mode prompt
        |                  |
        |                  +-- "Use the code in the current directory"
        |                  |     +-- InitFromCodeAction.Run
        |                  |           +-- agent.yaml exists? -> overwrite guard (§ 4.6)
        |                  |           +-- no -> scaffold prompts -> writeDefinitionToSrcDir -> addToProject
        |                  +-- "Start new from a template" -> existing scaffold flow
        |
        +-- found -> reuse prompt ("Use it?", auto-yes under --no-prompt)
              |
              +-- accepted -> runReuseDefinition
              |     +-- loadAgentDefinitionFile (ValidateAgentDefinition)
              |           +-- invalid -> exterrors.Validation(CodeInvalidAgentManifest, ...)
              |           +-- valid   -> ensureProject + env -> normalize abs --src -> addToProject
              +-- declined -> init-mode prompt (same as "not found" branch)
```
