# Foundry Infrastructure Layers for `azd ai agent init --infra`

## Summary

`azd ai agent init --infra` currently cannot eject Foundry infrastructure into
an azd project that already has infrastructure in `infra/`. The command refuses
because both the existing project and the generated Foundry templates expect to
own the same directory and entry point.

This design preserves the project's existing infrastructure and adds generated
Foundry infrastructure as a separate provisioning layer under
`infra/foundry`. Foundry-only projects keep the existing simple layout under
`infra/`.

The user mental model becomes:

> `--infra` adds editable Foundry infrastructure to my project without merging
> into or overwriting the infrastructure I already own.

Source issue: [Azure/azure-dev#9126](https://github.com/Azure/azure-dev/issues/9126).

## Problem

### Who is affected

Developers who start with an existing azd application and later add a Foundry
agent are affected when the application already contains Bicep, Terraform, or a
custom provisioning provider.

### Current experience

Running:

```console
azd ai agent init --infra
```

against a project with an existing `infra/` directory fails with:

```text
ERROR: `./infra/` already exists
```

The user must then choose between deleting existing infrastructure, manually
merging generated resources, or abandoning IaC eject. None is a safe default:

- Deleting `infra/` loses application infrastructure.
- File-level merging conflicts at `infra/main.bicep`, the deployment entry
  point.
- Resource-level Bicep merging requires semantic understanding of arbitrary
  user-authored infrastructure.
- Keeping Foundry provisioning implicit prevents users from reviewing or
  customizing its IaC.

The current eject path treats one `infra/` tree as exclusively owned at
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_infra.go:89`.

## Goals

1. Let users add editable Foundry IaC to an azd project that already has
   infrastructure.
2. Preserve all existing infrastructure files and configuration.
3. Avoid semantic merging of Bicep or Terraform.
4. Support both Bicep and Terraform eject.
5. Produce an `azure.yaml` that `azd provision`, targeted layer provision, and
   `azd down` can understand.
6. Keep the existing Foundry-only experience simple and backward-compatible.
7. Fail before modifying the project when a safe migration cannot be proven.

## Non-Goals

### Out of scope

- Merging a sample's unified `azure.yaml` into an existing project. That is
  tracked by [Azure/azure-dev#8884](https://github.com/Azure/azure-dev/issues/8884).
- General resource-level Bicep composition or arbitrary IaC merge tooling.
- Terraform support for Foundry private networking. Terraform eject continues
  to reject a `network:` block because the generated module does not implement
  the equivalent private-network topology.
- Ejecting greenfield IaC for a brownfield Foundry service with `endpoint:`.
  The existing resource remains externally owned.

### Explicitly not in scope

- Overwriting generated infrastructure with `--force`.
- Automatically importing pre-existing resources into Terraform state.
- Converting Bicep infrastructure to Terraform or Terraform infrastructure to
  Bicep.
- Inferring dependencies from arbitrary extension provisioning providers.

## User Experience

### Command surface

The command surface is unchanged:

| Command | Result |
|---|---|
| `azd ai agent init --infra` | Eject Bicep |
| `azd ai agent init --infra=bicep` | Eject Bicep explicitly |
| `azd ai agent init --infra=terraform` | Eject Terraform |

The flag contract is registered at
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go:1587`.

### Behavior by project shape

| Project shape | Behavior |
|---|---|
| New or Foundry-only project, no existing IaC entry point | Generate the existing root layout under `infra/` |
| Existing single-layer Bicep project | Preserve it as a layer and add `foundry` at `infra/foundry` |
| Existing single-layer Terraform project | Preserve it as a layer and add `foundry` at `infra/foundry` |
| Existing custom/fileless provisioning provider | Preserve it as a layer and add `foundry` at `infra/foundry` |
| Existing `infra.layers` project | Append a `foundry` layer without rewriting existing layer bodies |
| Existing generated Foundry files at the target | Refuse without overwriting |
| Brownfield Foundry project with `endpoint:` | Refuse eject; continue using the existing-project provisioning path |
| Terraform request with `network:` | Refuse and recommend Bicep |

### Resulting configuration

Given an existing project:

```yaml
name: web-app
infra:
  provider: bicep
services:
  web:
    host: containerapp
    project: src/web
  ai-project:
    host: azure.ai.project
```

running `azd ai agent init --infra` produces:

```yaml
name: web-app
infra:
  layers:
    - name: infra
      path: infra
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: bicep
      dependsOn:
        - infra
services:
  web:
    host: containerapp
    project: src/web
  ai-project:
    host: azure.ai.project
```

The existing `infra/main.bicep` is unchanged. Generated Foundry files are
written to `infra/foundry/`.

### Resource-group ownership

The generated Foundry layer owns a separate resource group:

```text
rg-${AZURE_ENV_NAME}-foundry
```

Its canonical environment output is:

```text
AZURE_FOUNDRY_RESOURCE_GROUP
```

This avoids changing or deleting the resource group owned by the existing
application layer. Root, Foundry-only projects retain the established
`AZURE_RESOURCE_GROUP` contract.

## Architecture and Touch-Points

| Component | Responsibility |
|---|---|
| `azure.ai.agents/internal/cmd/init_infra.go` | Detect project shape, plan migration, generate files, atomically install files, and update `azure.yaml` |
| `azure.ai.agents/internal/synthesis/templates/` | Source Bicep/Terraform templates and canonical Foundry outputs |
| `azure.ai.projects/internal/provisioning/` | Preserve compatibility for the IaC-less `microsoft.foundry` provider and layer-aware environment handling |
| `schemas/v1.0/azure.yaml.json` and `schemas/alpha/azure.yaml.json` | Permit `provider` on individual `infra.layers[]` entries |
| azd core provisioning graph | Execute layers using `dependsOn` and provider-specific state |

### Data flow

```text
azure.yaml
    |
    v
validate Foundry service + requested provider
    |
    v
classify current infra shape
    |
    +--> Foundry-only ----------> generate infra/*
    |
    +--> existing single layer -> convert config to infra.layers[]
    |
    +--> existing layers -------> append foundry layer
                                      |
                                      v
                              generate in temp directory
                                      |
                                      v
                         validate target files do not exist
                                      |
                                      v
                         atomically install + update azure.yaml
```

## Mechanism

### 1. Project classification

The eject planner parses `azure.yaml` as a YAML node tree so it can preserve
unknown extension properties and existing layer configuration. The entry point
is `planInfraEject` at
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_infra.go:282`.

Classification uses:

- Root `infra.provider`, `infra.path`, and `infra.module`.
- Existing `infra.layers` entries.
- Provider-specific entry points such as `<module>.bicep`,
  `<module>.bicepparam`, or `.tf` files.
- Explicit custom providers, including providers with no on-disk entry point.

Single-layer migration is handled by `planSingleInfraEject` at
`init_infra.go:328`; existing layered projects are handled by
`planLayeredInfraEject` at `init_infra.go:449`.

### 2. Layer migration

For an existing single-layer project:

1. Copy the root infrastructure options into the first layer.
2. Materialize defaults (`path: infra`, `module: main`, default provider) so
   the migrated layer remains explicit.
3. Name the preserved layer `infra`.
4. Add `foundry` at `infra/foundry`.
5. Set the Foundry layer provider to the requested on-disk provider:
   `bicep` or `terraform`.
6. Add `dependsOn` from the Foundry layer to the preserved layer.

For an existing layered project, all existing layer nodes remain in place and
the new Foundry layer depends on the existing layer names. Existing hooks,
modules, providers, and dependency declarations are preserved.

### 3. Generated IaC ownership

Generated layers use azd core providers rather than the IaC-less
`microsoft.foundry` provider:

| Requested format | Layer provider | Generated entry point |
|---|---|---|
| Bicep | `bicep` | `infra/foundry/main.bicep` |
| Terraform | `terraform` | `infra/foundry/main.tf` |

This avoids sharing a mutable extension-provider instance between multiple
layers and delegates normal Bicep/Terraform state and teardown behavior to azd
core.

The generated layer owns `rg-${AZURE_ENV_NAME}-foundry`. Layer-specific Bicep
parameters are added by `writeParametersFile` at `init_infra.go:1041`.
Terraform values are generated by `writeTfvarsFile` at `init_infra.go:1241`.

For layered output, `AZURE_RESOURCE_GROUP` is omitted from the generated layer
artifact so it cannot overwrite the application's root resource-group value.
`AZURE_FOUNDRY_RESOURCE_GROUP` is emitted instead. Root eject retains both the
legacy root output and the Foundry-specific output for compatibility.

### 4. File installation and rollback

Generation occurs in a temporary directory under the project root. No user
file is modified during synthesis.

`installStagedInfra` at `init_infra.go:629`:

1. Enumerates every generated file.
2. Refuses if any destination file already exists.
3. Creates only missing directories.
4. Copies files atomically.
5. Records created files and directories for rollback.
6. Rolls back generated files if the subsequent atomic `azure.yaml` write
   fails.

The command never deletes, edits, or semantically merges an existing IaC file.

### 5. Layer-aware provider compatibility

The `microsoft.foundry` provider remains the default for IaC-less projects.
Compatibility changes ensure it honors provisioning options passed by azd,
including layer `path`, `module`, and virtual environment values. The provider
entry point is
`cli/azd/extensions/azure.ai.projects/internal/provisioning/foundry_provisioning_provider.go:122`;
on-disk module loading is generalized by `loadOnDiskTemplateAt` at
`internal/provisioning/ondisk_template.go:96`.

The provider also distinguishes root and layer resource-group outputs and
validation through `AZURE_FOUNDRY_RESOURCE_GROUP`.

## Validation

Validation occurs before generated files are installed, in this order:

1. **Provider value**: only `bicep` and `terraform` are accepted.
2. **Project manifest**: `azure.yaml` must exist and be a top-level YAML map.
3. **Foundry service**: exactly one supported Foundry provisioning service must
   exist.
4. **Service shape**: networking must be declared on `host: azure.ai.project`.
5. **Brownfield**: `endpoint:` projects reject eject.
6. **Terraform networking**: Terraform rejects non-public network modes.
7. **Layer shape**: layer names and paths must be present and non-conflicting.
8. **Provider compatibility**: an existing `foundry` layer cannot silently
   switch between Bicep and Terraform.
9. **Path safety**: generated paths must be project-relative, must not contain
   `..`, and must not escape through symlinks.
10. **Module safety**: module names cannot contain separators, traversal, or a
    file extension.
11. **File conflicts**: every generated destination must be absent.

Errors use stable extension error codes such as `infra_eject_exists`,
`infra_eject_no_foundry_service`, `infra_eject_network_unsupported`, and
`invalid_azure_yaml`.

## Edge Cases and Failure Modes

| Case | Behavior | Rationale |
|---|---|---|
| Empty `infra/` directory | Use the root layout | No existing IaC ownership to preserve |
| Existing IaC entry point | Create layers | Avoid entry-point collision |
| Fileless custom provider | Preserve it as a layer | Provider configuration is infrastructure ownership even without files |
| Existing generated Foundry root IaC | Refuse | Re-eject must not overwrite user-owned edits |
| Existing `foundry` layer with generated files | Refuse on file conflict | Reruns are non-destructive |
| Existing `foundry` layer with another provider | Refuse | No implicit provider conversion |
| Existing path is a file | Refuse | Cannot replace a user file with a directory |
| Target path is a symlink outside project | Refuse | Prevent project-root escape |
| Multiple Foundry project services | Refuse | Synthesis supports one project service |
| Brownfield `endpoint:` | Refuse | Existing resource is not represented by greenfield IaC |
| Terraform plus `network:` | Refuse | Avoid silently provisioning a public account |
| Failure after partial file install | Remove only files/directories created by this invocation | Preserve retryability and user files |
| Targeted `azd provision foundry` | Supported | Layer has complete parameters and isolated state |
| Targeted `azd down foundry` | Supported | Layer owns an isolated Foundry resource group |

## Decisions

### Decision 1: Use provisioning layers instead of file-level merge

**Chosen:** Preserve existing IaC as one layer and generate Foundry IaC in
`infra/foundry`.

**Rejected:** Copy generated files into the existing `infra/` tree and prompt
only on filename conflicts.

**Why:** The conflict is semantic, not only textual. Both systems normally own
the deployment entry point. A successful file copy could still produce an
invalid or incomplete deployment graph.

### Decision 2: Do not perform resource-level Bicep merging

**Chosen:** Keep separate deployment entry points.

**Rejected:** Parse and insert Foundry modules/resources into arbitrary
`main.bicep`.

**Why:** Resource-level transformation would need to preserve scope,
parameters, modules, outputs, dependencies, naming conventions, and user
formatting. It creates high blast radius for a convenience command.

### Decision 3: Use core Bicep/Terraform providers for ejected layers

**Chosen:** Set the generated layer provider to `bicep` or `terraform`.

**Rejected:** Keep the layer provider as `microsoft.foundry` and let the
extension compile on-disk IaC.

**Why:** Core providers already own file-based state, preview, and teardown.
It also avoids extension-provider instance caching conflicts when a project has
multiple layers.

### Decision 4: Give the Foundry layer an isolated resource group

**Chosen:** `rg-${AZURE_ENV_NAME}-foundry`, surfaced as
`AZURE_FOUNDRY_RESOURCE_GROUP`.

**Rejected:** Reuse `AZURE_RESOURCE_GROUP` from the existing application.

**Why:** Shared resource-group ownership makes targeted teardown unsafe and can
cause Terraform import conflicts. Isolation gives each layer a clear lifecycle.

### Decision 5: Refuse overwrite rather than support `--force`

**Chosen:** Fail when any generated destination exists.

**Rejected:** Regenerate or patch existing ejected files with `--force`.

**Why:** Once ejected, files are user-owned and may contain intentional
customizations. The command cannot distinguish generated content from user
changes safely.

### Decision 6: Keep Foundry-only projects on the root layout

**Chosen:** Continue writing `infra/main.bicep` or root Terraform when no
existing IaC entry point exists.

**Rejected:** Always create `infra.layers`, even for one layer.

**Why:** The simple project stays simple, existing behavior remains compatible,
and users only see layering when composition requires it.

## Rollout and Compatibility

- Existing projects are unchanged until the user explicitly runs `--infra`.
- Existing Foundry-only eject behavior remains compatible.
- The schema adds per-layer `provider` to both stable and alpha
  `azure.yaml` schemas at `schemas/v1.0/azure.yaml.json:94`.
- Existing root-provider inheritance remains valid for user-authored layers.
- Generated Foundry layers declare their provider explicitly.
- No extension or CLI version number is proposed in this spec; release
  packaging follows the normal extension release process.

## Product Metrics

No new telemetry is included in this implementation. Proposed follow-up:

- Count `--infra` ejects that use a new layer versus the root layout.
- Count Bicep versus Terraform Foundry layers.
- Count validation failures by existing structured error code.

Proposed privacy classification: system metadata only; do not emit layer names,
paths, project names, or resource-group names.

## Open Questions

### 1. Should rerun support regeneration into an existing Foundry layer?

**Proposed answer:** No for this release. Keep fail-closed behavior until there
is an explicit, reviewable regeneration workflow with backup/diff UX.

### 2. Should the resource-group name be configurable at eject time?

**Proposed answer:** Not as a new CLI flag. Users who need a custom name can
edit the generated Bicep parameter or Terraform variable file; avoid expanding
`init` with another infrastructure naming option.

### 3. Should all new agent projects use layers by default?

**Proposed answer:** No. Use layers only when composing with existing
infrastructure. This limits conceptual overhead for first-time users.

### 4. Should Terraform private networking be added to reach Bicep parity?

**Proposed answer:** Track separately. Until parity exists, reject rather than
silently weaken network security.

## Test Plan

### Unit tests

- Preserve existing root Bicep and migrate it into `infra.layers`.
- Preserve existing Terraform and custom/fileless providers.
- Append to an existing layer list without changing hooks or dependencies.
- Generate Bicep and Terraform under `infra/foundry`.
- Honor a predeclared Foundry layer's custom path and module.
- Emit complete non-interactive Bicep parameters for a generated layer.
- Emit layer-specific Terraform values and isolated resource-group naming.
- Reject brownfield, multiple Foundry services, unsupported providers, and
  Terraform private networking.
- Reject file, directory, module, path, and symlink conflicts without changing
  `azure.yaml`.
- Roll back generated files if the manifest update fails.
- Preserve root behavior and provider stamping for Foundry-only eject.

Primary coverage is in
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_infra_test.go:66` and
`init_infra_test.go:1273`.

### Provider and schema tests

- Validate layer-specific path/module handling.
- Validate root and Foundry-specific resource-group outputs.
- Validate layer-aware location checks.
- Validate parity between the agents/projects synthesis copies.
- Parse both stable and alpha JSON schemas.
- Compile Bicep and compare generated ARM JSON to checked-in artifacts.

### Integration tests

- Parse the migrated `azure.yaml` through azd core.
- Run targeted core project/provisioning/gRPC tests.
- Verify command completion snapshots remain stable.

### Manual/E2E validation

The following scenarios should be run before release:

1. Existing Bicep application -> add Bicep Foundry layer -> `azd provision` ->
   `azd down foundry`.
2. Existing Terraform application -> add Terraform Foundry layer ->
   `azd provision` -> `azd down foundry`.
3. Existing mixed-layer project -> append Bicep Foundry layer -> targeted
   preview/provision.
4. Foundry-only project -> root Bicep eject -> provision/down regression.
5. Rerun eject after editing generated files -> confirm non-destructive refusal.

Live Azure provisioning is not covered by unit tests and remains a release
validation requirement.

## References

- Issue: [Unable to generate Bicep into existing project #9126](https://github.com/Azure/azure-dev/issues/9126)
- Original Bicep-less/eject RFC: [#8065](https://github.com/Azure/azure-dev/issues/8065)
- Layered provisioning implementation: [#5492](https://github.com/Azure/azure-dev/pull/5492)
- Parallel layer dependencies: [#6291](https://github.com/Azure/azure-dev/issues/6291)
- Unified manifest adoption boundary: [#8884](https://github.com/Azure/azure-dev/issues/8884)
- Private networking behavior: `cli/azd/extensions/azure.ai.agents/docs/private-networking.md`
