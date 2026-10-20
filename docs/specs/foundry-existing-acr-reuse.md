<!-- cspell:ignore ABAC -->

## Summary

Teams creating a new Microsoft Foundry project need to build and publish hosted-agent container
images through an Azure Container Registry (ACR) they already manage. Today, greenfield source-build
provisioning creates a new registry even when the azd environment identifies an existing one,
duplicating infrastructure and overwriting the intended registry configuration. See
[#10022](https://github.com/Azure/azure-dev/issues/10022).

This draft contains only a proposal, with no implementation changes. Implementation is tracked
separately in [#10517](https://github.com/Azure/azure-dev/pull/10517); the flag name and command
placement below are not implemented there.

## Changes

- Allow a new Foundry project to reuse an existing ACR without switching to a prebuilt image or
  creating the project separately.
- Accept a registry resource ID during setup so users can continue directly to `azd up`, without a
  separate `azd env set` step, as suggested in
  [Travis's feedback](https://github.com/Azure/azure-dev/pull/10517#pullrequestreview-5551835659).
- Connect the project to that registry and automatically configure its identity's image-pull access,
  following the existing registry-reuse flow. Preserve existing grants and create only missing access.
  Use the selected registry for subsequent builds, publishing, and deployment.

## Command and flag experience

| Surface | Proposed behavior |
| --- | --- |
| `azd ai agent init --acr-resource-id "<registry-arm-id>"` | Accept the registry selection for hosted-container source builds and pass it to project setup. |
| `azd ai project add --new-project --acr-resource-id "<registry-arm-id>"` | Own registry configuration and save the selection in the active azd environment. |
| `azd env set AZURE_CONTAINER_REGISTRY_RESOURCE_ID "<registry-arm-id>"` | Continue supporting environment-based configuration for scripts and existing projects. |
| `azd provision` / `azd up` | Honor the saved selection across repeated runs, without manual configuration cleanup or treating an azd-created registry as externally owned. |
| `azd down` | Preserve the external registry and its images; apply the ownership rules below to cleanup. |

An explicit setup flag takes precedence over environment configuration. With neither input, azd
continues to create a registry when source builds need one, without adding a required prompt. Show
the selected registry and whether it is externally owned. The first release does not add a registry
picker; retain existing connection-selection and login-server input flows where applicable.

Existing flags keep their meanings:

| Existing option | Why it does not cover this scenario |
| --- | --- |
| `--acr-connection` | Selects an ACR connection in an existing Foundry project, not a registry for a new project. |
| `--image` / `--registry-connection` | Select a prebuilt image / its private-registry pull connection, not where source builds publish images. |
| `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` | Identifies a registry, but greenfield provisioning currently still creates a new one. |

For interactive use and `--no-prompt`/CI alike, reject empty or malformed selectors, conflicting
selectors, and unsupported combinations before changing local state or creating Azure resources.
This includes using the source-build selector for code deployment or a prebuilt-image-only flow.
Provide actionable errors rather than silently ignoring the selection or creating another registry.

## Compatibility and ownership

The external ACR remains user-owned. azd must not change its networking or take ownership of it.
Under the current resource-group teardown model, require the registry to sit outside the resource
group this deployment deletes. Track azd-created access grants separately from administrator-managed
grants, and clean up only grants and related resources azd can establish it owns.

The deployer needs permission to establish missing project access; the image publisher needs push
permission. ABAC-enabled registries require appropriate repository roles: support that mode or
reject it clearly. Customers remain responsible for private endpoints, firewall rules, and network
reachability.

The first release covers registry selection for new projects, not registry migration. Existing
environments retain their registry choice; reject attempts to switch registries on an already
provisioned project with guidance explaining that limitation.

The proposed initial scope is embedded Bicep and Bicep eject. The implementation in #10517 currently
rejects greenfield Terraform eject in reuse mode; document that limitation explicitly.

## Open question

Can the first release ship with Bicep-only reuse and add Terraform support later, or is Terraform
parity required before release?
