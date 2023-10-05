# Provision state

The Azure Developer CLI introduced the `provision state` on version `1.4.0`. Remote state is specific for provisioning providers which use ARM templates for deploying resources, like the `bicep provider`.

## Specification

Provision state is enabled by default (You don't need to opt-in). Azd creates a `hash` from the ARM template (template and input parameters), which is persisted on Azure as part of the `deployment` and it is used to find out if the template changes. When running `azd provision`, azd creates the `hash` for the current template and checks if there is a previous deployment on Azure with provision-state. Azd will only submit the deployment if the `current hash` is different thant the provision-state, or if there is no provision-state.

You can use the flag `--no-state` when running `azd provision`, to provision your infrastructure regardless of any provision-state.

Provision state is not aware of changes made to your infrastructure without azd. For example, using the Azure portal or the Azure CLI (az). When such external updates happens, azd would still find the last `hash` equal to the current template's hash and skip provisioning.

### Scenarios

The next table describes some common cases where provision state is either used or ignored.

|Scenario | Result | Notes |
|-|-|-|
| Run `provision` twice | Second run is skipped ||
| Run `provision`, then change IaC, then `provision` again | no-skip | No external changes to IaC |
| Run `provision`, then update infrastructure externally, then `provision` again | Second run is skipped | Azd will not detect external changes |
| Run provision with flag: `azd provision --no-state` | no-skip | regardless of any previous provision |

### Running on CI/CD

You can take advantage of `provision state` running on any continuous integration pipeline like GitHub or Azure DevOps