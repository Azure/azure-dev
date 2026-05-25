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
| Existing in-action overwrite guard | `cmd/init_from_code.go` (`InitFromCodeAction.Run`) | **Modified**: the "agent.yaml already exists" guard at the top of the action is removed. By the time `InitFromCodeAction.Run` is reached, the RunE-level dispatch has already handled (or declined) the reuse. |
| Upstream manifest detection | `cmd/init_from_templates_helpers.go` (`detectLocalManifest`, called from `cmd/init.go` `RunE`) | **Unchanged**. |
| Manifest pipeline | `cmd/init.go` (`runInitFromManifest`) | **Unchanged**. |
| Definition reuse helpers | `cmd/init_from_code_reuse.go` (new file) | `findExistingAgentYaml`, `runReuseDefinition`, `loadAgentDefinitionFile`. |
| `azure.yaml` writer | `cmd/init_from_code.go` (`addToProject`) | **Reused as-is** by `runReuseDefinition`. |
| Project / environment bootstrap | `cmd/init.go` (`ensureProject`, `getExistingEnvironment`); `cmd/init_foundry_resources_helpers.go` (`createNewEnvironment`) | **Reused as-is**: the reuse path runs the same setup `runInitFromManifest` does. |
| Definition types | `pkg/agents/agent_yaml/yaml.go` (`ContainerAgent`, `CodeConfiguration`) | Unchanged. |
| Name validation | `pkg/agents/agent_yaml/parse.go` (`ValidateAgentName`) | Reused. |

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

1. `loadAgentDefinitionFile(path)`: reads the file, rejects anything with a top-level `template:` (manifest-shaped but invalid, produces a targeted error), unmarshals to `agent_yaml.ContainerAgent` so `CodeConfiguration` is preserved, and validates the name via `agent_yaml.ValidateAgentName`.
2. Prints `Detected existing agent definition: <relative-path> (name: <def.Name>).`
3. Bootstraps project + env using the same helpers `runInitFromManifest` uses: `ensureProject`, then `getExistingEnvironment` / `createNewEnvironment` (the env is named `sanitizeAgentName(<def.Name> + "-dev")` when none was supplied via `-e`, matching the existing from-code path).
4. Builds a thin `*InitFromCodeAction` with the bootstrapped pieces and calls `action.addToProject(ctx, srcDir, def.Name, def.CodeConfiguration != nil)`, the existing from-code service-entry writer.
5. Prints `Reusing existing agent.yaml (name: <def.Name>).`
6. Calls `validatePostInit(srcDir, def.CodeConfiguration)` for the same advisory warnings the scaffold path emits.

The function deliberately does **not** call `configureModelChoice` or any Foundry resource resolution. The on-disk `agent.yaml` is never rewritten.

### 4.3 Invalid file

When `loadAgentDefinitionFile` returns any error (broken YAML, manifest-shaped file that did not route upstream, missing/invalid name), `runReuseDefinition` wraps it in a structured error:

```go
exterrors.Validation(
    exterrors.CodeInvalidAgentManifest,
    fmt.Sprintf("agent definition in %s is invalid: %s", displayPath, err),
    "Fix the agent.yaml and retry, or remove the file to start a fresh init.",
)
```

`azure.yaml` is **not** mutated.

### 4.4 Service-name collision in `azure.yaml`

Handled by the existing collision logic inside `addToProject`. No new behavior.

### 4.5 `--no-prompt`

- `--no-prompt` + valid bare `agent.yaml` -> succeeds, runs `addToProject`. The confirmation prompt auto-defaults to yes (matches `detectLocalManifest` behavior).
- `--no-prompt` + invalid file -> returns the structured `exterrors.Validation` above.
- `--no-prompt` + no `agent.yaml` -> unchanged.

The pre-existing fail-fast guard for "agent.yaml already exists" in `--no-prompt` mode (added in #8266) is removed: there is no overwrite to fail.

## 5. Interactions with Existing Flags

| Scenario | Behavior |
| --- | --- |
| `-m <pointer>` provided **and** local `agent.yaml` exists | `-m` wins. The new reuse block runs only when `flags.manifestPointer == ""`. |
| Local `agent.manifest.yaml` (or any agent yaml that parses as a valid manifest) exists, no `-m` | Caught by upstream `detectLocalManifest`. The reuse block in this spec is not entered. |
| Local bare `agent.yaml` (no `template:`) exists, no `-m` | This spec's reuse block runs. |
| `--src <dir>` set | Detection runs against that directory. |
| `--no-prompt` + bare `agent.yaml` | Auto-yes on the confirmation, proceeds with reuse. |

## 6. Errors

No new error codes. The reuse path uses existing codes from `internal/exterrors/codes.go`:

| Failure | Code |
| --- | --- |
| File present but unparseable as a bare definition (broken YAML, manifest-shaped, invalid name) | `exterrors.Validation(CodeInvalidAgentManifest, "agent definition in <path> is invalid: <err>", "Fix the agent.yaml and retry, or remove the file to start a fresh init.")` |

Per the extension's error-handling rule, the structured error is created at the boundary inside `runReuseDefinition` (where category, code, and suggestion are known). Lower-level helpers (`findExistingAgentYaml`, `loadAgentDefinitionFile`) return plain `fmt.Errorf` wraps.

## 7. Test Plan

- **Pre-existing unit tests** in `cli/azd/extensions/azure.ai.agents/internal/cmd/*_test.go` continue to pass. They cover helpers (`sanitizeAgentName`, `writeDefinitionToSrcDir`, etc.) that this change does not modify; we are not adding new unit tests because the new code paths are exercised end-to-end below.
- **Manual e2e** against the locally-built `azd` + extension. Three scenarios:
  - **Definition reuse**: write a bare `agent.yaml`, run `azd ai agent init --no-prompt`; assert no init-mode prompt, `Detected existing agent definition: ...` printed, `azure.yaml` contains the agent's `name:`, on-disk `agent.yaml` byte-identical to input.
  - **Manifest reuse**: write `agent.manifest.yaml`, run with `AZURE_SUBSCRIPTION_ID` and `AZURE_AI_PROJECT_ID` set; assert the existing upstream manifest path runs unchanged and `azure.yaml` ends up with the manifest's `template.name`.
  - **Invalid yaml**: write broken YAML; assert non-zero exit, error references `agent.yaml`, `azure.yaml` not mutated.

## 8. Impact on Existing Commands

Only `azd ai agent init` changes:

- Users with a bare `agent.yaml` no longer see the init-mode prompt or the from-code prompt sequence.
- Users in `--no-prompt` mode with a bare `agent.yaml` no longer get the `agent.yaml already exists at ...` failure from #8266.
- Users with a valid manifest are unaffected (upstream `detectLocalManifest` handles them as before).
- Users with no `agent.yaml` are unaffected.

No flags added or removed; no `--help` snapshot changes.

## 9. Decisions

1. **Dispatch at `RunE`, not inside `InitFromCodeAction.Run`.** Placing the detection alongside `detectLocalManifest` keeps the manifest and definition reuse paths symmetric, and means users with a local `agent.yaml` are not asked to pick an init mode first. An earlier draft placed the dispatch inside `InitFromCodeAction.Run`, which forced users through the mode prompt before the reuse could fire.
2. **No Foundry project resolution in the definition path.** The issue explicitly limits this case to "just setup azure.yaml". Adding project resolution would either duplicate the manifest pipeline's logic or require a non-trivial refactor of `runInitFromManifest` to support an in-memory manifest input; it is tracked as a follow-up.
3. **No new error code.** `CodeInvalidAgentManifest` covers both shapes from the user's perspective. Splitting the code would add telemetry noise without distinguishing actionable user remedies.
4. **No recursion into subdirectories.** Discovery is limited to the resolved source directory.
5. **Definition path does not re-write the file.** The on-disk `agent.yaml` is authoritative. If `def.Name` collides with an existing service entry in `azure.yaml`, the existing collision logic resolves it by renaming the service entry, not the file.
6. **Manifest reuse is left to upstream `detectLocalManifest`.** Discovered during implementation: valid local manifests are already intercepted upstream and routed through `runInitFromManifest`. A duplicate dispatch would be dead code. `findExistingAgentYaml` still includes the manifest filenames in its scan so a malformed manifest-shaped file produces a targeted error instead of silently scaffolding.

## 10. Reference: Decision Tree

```text
azd ai agent init (no -m, no positional manifest)
  |
  +-- detectLocalManifest(srcDir)                                  [unchanged]
  |     |
  |     +-- valid manifest found
  |     |     +-- flags.manifestPointer = <local path>
  |     |           +-- runInitFromManifest                        [existing path]
  |     |
  |     +-- nothing valid -> fall through
  |
  +-- findExistingAgentYaml(srcDir)                                [new]
        |
        +-- not found -> existing init-mode prompt / scaffold flow
        |
        +-- found -> runReuseDefinition
              |
              +-- loadAgentDefinitionFile
                    |
                    +-- invalid -> exterrors.Validation(CodeInvalidAgentManifest, ...)
                    |
                    +-- valid   -> ensureProject + env -> addToProject(srcDir, def.Name, isCodeDeploy)
```
