# Provision Microsoft Foundry for Content Understanding

This guide covers creating or reusing a Microsoft Foundry resource, deploying
supported models, configuring Content Understanding defaults, and connecting
`azd ai cu`. For analysis and analyzer commands, return to the [README](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md)
or [CLI usage guide](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md).

## Understand what is required

Content Understanding is accessed through a Microsoft Foundry resource in a
[supported region](https://learn.microsoft.com/azure/ai-services/content-understanding/language-region-support#region-support).
The Azure resource is separate from your local azd environment.

Generative workflows need supported language and embeddings model deployments.
A deployment makes a model available under a deployment name. Resource-level
Content Understanding defaults map model names and prebuilt analyzer aliases
to those deployment names.

The `prebuilt-digitalParse`, `prebuilt-read`, and `prebuilt-layout` analyzers do
not require language models, embeddings models, or model defaults. Other
prebuilt and custom analyzers need the deployments supported by that analyzer.

`azd ai cu infra generate` writes a self-contained azd/Bicep project. It performs
subscription and resource discovery but does not deploy resources. Run
`azd up` separately from the generated directory.

## Sign in

Install the extension as described in the [README](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md#install), then
sign in through Azure Developer CLI:

```bash
azd auth login
```

Both the extension and `azd up` use azd authentication. Python, a CU profile,
and API-key authentication are not required or used by the extension.

## Azure permissions

The generated Bicep uses a subscription-scope deployment for both new and
existing resource paths. Deployment permissions and Content Understanding
data-plane access are separate.

| Scenario | Required access |
| --- | --- |
| Create a resource without assigning roles | Contributor on the selected subscription, or equivalent permissions to deploy the resource group, Foundry resource, project, and models. |
| Create a resource and assign a data-plane role | Deployment permissions plus permission to create role assignments, such as Owner, or Contributor combined with Role Based Access Control Administrator or User Access Administrator. |
| Deploy models to an existing resource | Permission for the subscription-scope deployment and the required model deployment actions on that resource. |
| Call Content Understanding | Cognitive Services User on the Foundry resource, or equivalent data-plane permissions. Contributor alone does not grant this access. |

Role assignment is disabled by default. Pass `--assign-roles` to opt in when
creating a resource and when your identity has the required permissions.
Otherwise, have an administrator grant data-plane access once the resource
exists, then rerun `azd up` to complete Content Understanding setup.

The existing-resource path creates no role assignments. Grant the signed-in
identity access through the resource's **Access control (IAM)** page.
`--assign-roles` is supported only for new resources and cannot be combined with
`--foundry-endpoint`, including when explicitly set to `--assign-roles=false`.
Omit that flag when targeting an existing resource.
There is no API-key fallback.

See [Azure built-in privileged roles](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles/privileged)
and [Cognitive Services User](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles/ai-machine-learning#cognitive-services-user)
for role definitions.

## Create a new Microsoft Foundry resource

Generate a project and deploy it:

```bash
azd ai cu infra generate --output-dir provision
cd provision
azd up
```

Generation selects a subscription through azd. Defaults are environment `dev`,
region `eastus2`, and interactive model selection. The model prompt runs during
post-provision setup, after the resource exists.

The new-resource path creates a resource group, a Microsoft Foundry resource,
a Foundry project, and selected model deployments. The azd environment identifies
the deployment stack; its state is stored under `provision/.azure/<environment>/`.
It is not a CU CLI profile.

For explicit choices with recommended models:

```bash
azd ai cu infra generate \
  --output-dir provision \
  --environment dev \
  --location eastus2 \
  --models recommended
cd provision
azd up
```

Choose a supported region with appropriate model availability and quota.
Use `--foundry-prefix my-cu` to customize the new account's naming prefix.
For a model-free resource suitable for `prebuilt-layout`, use `--models none`.
Data-plane access is still required.

For unattended generation, configure the azd default subscription first:

```bash
azd config set defaults.subscription "<subscription-id>"
azd ai cu infra generate --models none --output-dir provision --no-prompt
```

Generation honors that configured subscription even when the host's subscription
list does not mark it as the default. If the configured subscription is
inaccessible, generation fails rather than selecting another one. Without a
configured default, it uses a host-marked default or the sole accessible
subscription; ambiguous choices require configuration or interactive generation.
This command writes project files only; it does not provision resources.

Recognized generated projects preserve editable assets and environment settings
unless `--force` is supplied. Force replaces known generated assets and resets
model/configuration setup; unrelated files are not deleted.

## Deploy models and configure defaults

Model availability and quota vary by region. Check the current
[supported generative models](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits#supported-generative-models).

For a new resource, use `--models recommended` above or omit `--models` for
interactive selection. To deploy models to an existing resource:

```bash
azd ai cu infra generate \
  --foundry-endpoint "https://<resource-name>.services.ai.azure.com/" \
  --models recommended \
  --output-dir provision
cd provision
azd up
```

The existing-resource path reuses the resource and its resource group. It does
not create another Foundry resource, project, or role assignment. The generated
hook deploys the selected models and configures defaults.

`--models` also accepts comma-separated `model@version` selectors. The live
CU-supported catalog validates selections during model setup.
`--foundry-endpoint` and `--foundry-prefix` cannot be combined.

## Generated post-provision behavior

The generated Bash and PowerShell hooks invoke the extension's versioned
`azd ai cu infra _postprovision-v1` contract. Keep the extension installed when
running `azd up`.

Both hooks pass the provisioned `AZURE_ENV_NAME` explicitly as `--environment`,
so provisioning a nondefault environment does not configure the project default
instead. A missing or empty `AZURE_ENV_NAME` fails before invoking the extension.

After resource provisioning, the hook:

1. Sets up selected model deployments, unless model setup already completed.
2. Saves `CU_ENDPOINT` and `CU_DEFAULT_ANALYZER=prebuilt-layout` in the selected
   azd environment, never in global CU user defaults.
3. Saves discovered model mappings and configures Content Understanding defaults
   when deployments exist.
4. Reports model readiness, warnings, and available workflows.

Optional model or defaults failures are reported as warnings and do not
necessarily fail resource provisioning. Inspect the hook output and run
`doctor` before using generative workflows. Endpoint or environment configuration
failures report incomplete setup and return a nonzero status.

Existing CU environment configuration is preserved unless generation used
`--force`. `CU_DISABLE_AUTO_CONFIG=true` skips automatic CU configuration,
not model setup.

Verify the selected environment from the generated directory:

```bash
azd ai cu doctor --environment dev
azd ai cu defaults show --environment dev
```

Replace `dev` if you generated a different environment. No separate CU profile
is created.

## Configure an existing ready resource

If the resource, required deployments, and defaults already exist, skip
infrastructure generation. Connect directly:

```bash
azd auth login
export CU_ENDPOINT="https://<resource-name>.services.ai.azure.com/"
azd ai cu doctor
```

In PowerShell, use
`$env:CU_ENDPOINT = "https://<resource-name>.services.ai.azure.com/"`.
Normal endpoint-only use delegates tenant selection to azd authentication.
For saved environment settings and explicit tenant selection, see
[connection guidance](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#connect-to-microsoft-foundry-and-check-setup).

## Configure defaults manually

When model deployments exist but Content Understanding defaults are missing,
connect as above and map supported model names to deployment names:

```bash
azd ai cu defaults set \
  --model "<supported-language-model>=<language-deployment>" \
  --model "<supported-embedding-model>=<embedding-deployment>"
azd ai cu defaults show
azd ai cu doctor
```

Replace the placeholders with supported model names and existing deployment
names. For supported models, the extension also derives the service aliases
used by prebuilt analyzers.

If mappings are already saved in the selected azd environment's
`cu.modelDeployments`, use `azd ai cu defaults set --from-environment`.
`doctor --fix-defaults` also explicitly applies environment model mappings;
it does not deploy models or provision resources.

Defaults belong to the Azure resource, not the local CLI. See
[defaults update semantics](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#manage-analyzers-and-defaults)
before changing shared resource settings.

## Diagnose readiness

Run `azd ai cu doctor` to check configuration, authentication, service access,
and model defaults. Add `--environment dev` to check a specific azd environment.

Authentication, configuration, and service failures return nonzero. Missing
generative requirements can instead produce a successful command with
`ready: false`, because model-free analyzers may still work. For automation,
inspect the report as well as the exit code.

If model setup failed, check the hook's warnings, model availability, quota,
and data-plane access. Correct the issue, rerun `azd up` from the generated
directory, then check readiness again. If deployments already exist and only
defaults are missing, use the manual defaults workflow above.

## Further reading

- [CLI getting started](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md)
- [CLI usage and configuration](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md)
- [Create a Microsoft Foundry resource](https://learn.microsoft.com/azure/ai-services/content-understanding/how-to/create-multi-service-resource)
- [Content Understanding regions and languages](https://learn.microsoft.com/azure/ai-services/content-understanding/language-region-support)
- [Supported models and service limits](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits)
- [Model deployment options for analyzers](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/models-deployments)
