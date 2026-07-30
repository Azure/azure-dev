# Foundry Infrastructure Layers for `azd ai agent init --infra`

## Summary

`azd ai agent init --infra` cannot currently generate Foundry IaC in an azd
project that already has an `infra/` directory. Both the existing project and
the generated Foundry templates expect to own the same deployment entry point.

This design preserves the project's existing infrastructure and adds Foundry
infrastructure as a separate provisioning layer under `infra/foundry`.
Foundry-only projects keep the existing simple `infra/` layout.

> `--infra` adds editable Foundry infrastructure without merging into or
> overwriting infrastructure the user already owns.

Source issue: [Azure/azure-dev#9126](https://github.com/Azure/azure-dev/issues/9126).

## Problem

Developers commonly add a Foundry agent to an existing web application or API.
When that application already contains Bicep or Terraform, eject fails:

```text
ERROR: `./infra/` already exists
```

Deleting `infra/` loses application infrastructure. File-level merging is not
safe because both trees normally contain `main.bicep` or Terraform entry-point
files. Resource-level merging would require azd to understand and rewrite
arbitrary user-authored IaC.

## Goals

1. Add editable Foundry IaC to projects with existing infrastructure.
2. Preserve existing files and configuration.
3. Support Bicep and Terraform eject.
4. Avoid semantic IaC merging and silent overwrites.
5. Keep Foundry-only projects backward-compatible.

## Proposed Experience

The command surface is unchanged:

| Command | Result |
|---|---|
| `azd ai agent init --infra` | Eject Bicep |
| `azd ai agent init --infra=bicep` | Eject Bicep explicitly |
| `azd ai agent init --infra=terraform` | Eject Terraform |

### Behavior by project shape

| Project shape | Behavior |
|---|---|
| New or Foundry-only project | Generate the existing root layout under `infra/` |
| Existing single-layer Bicep or Terraform project | Preserve it as a layer and add `foundry` at `infra/foundry` |
| Existing custom/fileless provider | Preserve it as a layer and add `foundry` |
| Existing layered project without `foundry` | Append a `foundry` layer; preserve existing layer bodies |
| Existing compatible `foundry` layer with empty target | Generate into its declared `path` and `module` |
| Existing `foundry` layer with a different provider | Refuse; do not convert providers implicitly |
| Existing `infra/foundry` directory without a layer | Add only non-conflicting files; refuse generated-file collisions |
| Existing files in the declared `foundry` target | Refuse without overwriting |
| Brownfield project with `endpoint:` | Refuse eject; existing resource remains externally owned |
| Terraform request with private `network:` | Refuse and recommend Bicep |

A folder and a layer are separate signals. An `infra/foundry` folder does not
authorize azd to overwrite its contents. An existing `foundry` layer is reused
only when its provider matches the requested format and generated files do not
already exist.

### Example migration

Before:

```yaml
infra:
  provider: bicep
```

After `azd ai agent init --infra`:

```yaml
infra:
  layers:
    - name: infra
      path: infra
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
      dependsOn:
        - infra
```

The existing `infra/main.bicep` remains unchanged. Foundry files are generated
under `infra/foundry/`.

## Ownership and Lifecycle

Generated Bicep continues to use `microsoft.foundry`, with the layer's `path`
and `module` telling the provider where to load the ejected templates.
Generated Terraform uses the core `terraform` provider because Terraform state
and lifecycle are owned by azd core.

The Foundry layer owns an isolated resource group:

```text
rg-${AZURE_ENV_NAME}-foundry
```

It publishes `AZURE_FOUNDRY_RESOURCE_GROUP` rather than replacing the existing
application's `AZURE_RESOURCE_GROUP`. This keeps targeted `azd down foundry`
from deleting sibling-layer resources.

## Safety Rules

The command validates the full plan before updating the project:

- Exactly one Foundry provisioning service must exist.
- Brownfield `endpoint:` projects cannot eject greenfield IaC.
- Terraform cannot eject private networking until it reaches Bicep parity.
- Layer names and paths must not conflict.
- Paths must stay inside the project and cannot escape through symlinks.
- Module names cannot contain traversal, separators, or file extensions.
- Existing generated destinations are never overwritten.
- Files are generated in a temporary directory, installed atomically, and
  removed if the `azure.yaml` update fails.

## Key Decisions

### 1. Layers instead of file merging

We preserve existing IaC as one layer and generate Foundry IaC as another.
File-level merging was rejected because deployment entry-point conflicts are
semantic, not only textual.

### 2. Isolated resource-group ownership

The Foundry layer receives its own resource group instead of sharing
`AZURE_RESOURCE_GROUP`. This avoids Terraform import conflicts and destructive
cross-layer teardown.

### 3. Fail closed on rerun

If generated files already exist, eject refuses rather than regenerating or
using `--force`. Once ejected, IaC is user-owned and may contain intentional
customizations.

### 4. Preserve the simple project shape

Foundry-only projects continue using root `infra/`. Layers are introduced only
when composition requires them.

## Scope

### In scope

- Bicep and Terraform Foundry layers
- Migration from a root provider to `infra.layers`
- Appending to existing layered projects
- Path, module, provider, and file-conflict validation
- Isolated Foundry resource-group ownership

### Out of scope

- Semantic Bicep/Terraform merging
- Overwriting or regenerating user-owned IaC
- Terraform private networking
- Brownfield IaC generation
- Merging a unified sample `azure.yaml` into an existing project
  ([#8884](https://github.com/Azure/azure-dev/issues/8884))

## Rollout and Validation

Existing projects are unchanged until users explicitly run `--infra`. The
stable and alpha `azure.yaml` schemas add per-layer `provider` support.

Automated coverage includes:

- Root Bicep/Terraform migration
- Existing layered and custom-provider projects
- Existing `foundry` layer and folder conflicts
- Bicep/Terraform generation and complete non-interactive parameters
- Path traversal, symlink, module, brownfield, and networking failures
- Atomic install and rollback behavior
- Bicep compilation and agents/projects template parity

Before release, manually validate:

1. Existing Bicep app -> eject -> provision -> `azd down foundry`.
2. Existing Terraform app -> eject -> provision -> `azd down foundry`.
3. Existing mixed-layer project -> eject -> targeted preview/provision.
4. Foundry-only project -> root eject regression.
5. Rerun after editing generated files -> non-destructive refusal.

## Open Questions

1. **Should rerun support regeneration?** Proposed: no, until there is an
   explicit backup/diff workflow.
2. **Should resource-group naming become an init flag?** Proposed: no; users
   can edit generated parameters without expanding the init surface.
3. **Should all agent projects use layers?** Proposed: no; introduce layers
   only when composing with existing infrastructure.

## References

- [Issue #9126](https://github.com/Azure/azure-dev/issues/9126)
- [Bicep-less/eject RFC #8065](https://github.com/Azure/azure-dev/issues/8065)
- [Layered provisioning PR #5492](https://github.com/Azure/azure-dev/pull/5492)
- [Unified manifest adoption boundary #8884](https://github.com/Azure/azure-dev/issues/8884)
