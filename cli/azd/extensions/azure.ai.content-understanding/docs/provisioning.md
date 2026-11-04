# Provision Microsoft Foundry for Content Understanding

This guide covers creating or reusing a Microsoft Foundry resource, deploying
supported models, configuring Content Understanding defaults, and connecting
`azd ai cu`. For analysis and analyzer commands, return to the [README](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md)
or [CLI usage guide](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md).

Choose the workflow that matches how you run commands:

- [Interactive user workflow](#interactive-user-workflow): answer the generation
  wizard, then deploy the generated project.
- [Coding agent unattended workflow](#coding-agent-unattended-workflow): provide
  explicit inputs and disable prompts for generation, deployment, and hooks.

Both workflows use the same templates and saved environment state. The
[reference sections](#saved-project-settings) explain that state, deployment
outputs, advanced controls, and compatibility with older generated projects.

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

Infrastructure commands, postprovision setup, and `azd up` use azd / Entra ID
authentication. They do not use `CU_API_KEY`. Separate CU data-plane commands
can optionally use a process-only resource key as described in the
[usage guide](usage.md#connect-to-microsoft-foundry-and-check-setup).

## Azure permissions

The generated Bicep uses a subscription-scope deployment for both new and
existing resource paths. Deployment permissions and Content Understanding
data-plane access are separate.

| Scenario | Required access |
| --- | --- |
| Create a resource without assigning roles | Contributor on the selected subscription, or equivalent permissions to deploy the resource group, Foundry resource, project, and models. |
| Create a resource and assign a data-plane role | Deployment permissions plus permission to create role assignments, such as Owner, or Contributor combined with Role Based Access Control Administrator or User Access Administrator. |
| Deploy models to an existing resource | Permission for the subscription-scope deployment and the required model deployment actions on that resource. |
| Call Content Understanding with Entra ID | Cognitive Services User on the Foundry resource, or equivalent data-plane permissions. Contributor alone does not grant this access. |

Role assignment defaults to No. Interactive generation asks when neither role
flag is supplied for a new resource. Pass `--assign-roles` to opt in when
creating a resource and when your identity has the required permissions, or
`--no-assign-roles` to skip the question.
Otherwise, have an administrator grant data-plane access once the resource
exists, then rerun `azd up` to complete Content Understanding setup.

The existing-resource path creates no role assignments. Grant the signed-in
identity access through the resource's **Access control (IAM)** page.
`--assign-roles` is supported only for new resources and cannot be combined with
`--foundry-endpoint`, including when explicitly set to `--assign-roles=false`.
Omit that flag when targeting an existing resource.

See [Azure built-in privileged roles](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles/privileged)
and [Cognitive Services User](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles/ai-machine-learning#cognitive-services-user)
for role definitions.

<a id="create-a-new-microsoft-foundry-resource"></a>

## Interactive user workflow

Sign in first, then use two steps: **generate files**, followed by **deploy
resources**. Only the second step creates or changes Azure resources and can
incur charges. Have your subscription and intended region ready, and check
the [permissions](#azure-permissions) above.

### Step 1: Generate the project

For a first deployment without optional models:

```bash
azd ai cu infra generate --output-dir provision --models none
```

| Question | What to provide |
| --- | --- |
| Subscription | An accessible subscription for the deployment. Pass `--subscription "<name-or-id>"` to skip this question. |
| azd environment name | A name such as `dev`, `test`, or `prod` for the azd environment, not a model deployment. azd stores its configuration and outputs under `.azure/<env>/` and uses the name to derive Azure resource names such as `rg-<env>` and `proj-<env>`. Separate environments keep stacks apart. Default: `dev`, or inherited `AZD_ENVIRONMENT`. |
| Region | The Azure region where the Microsoft Foundry resource is created. Choose a [Content Understanding supported region](https://learn.microsoft.com/azure/ai-services/content-understanding/language-region-support#region-support). Default: `eastus2`. Model availability and quota matter if you plan to add models. |
| Microsoft Foundry resource name prefix | An optional lowercase prefix, such as `my-cu`, for the globally unique resource name used in the public endpoint hostname (`https://<name>.services.ai.azure.com`). With a prefix, the name is `<prefix>-<unique-suffix>`; blank uses `aif-<unique-suffix>`. |
| Assign RBAC roles | Default: No. Select Yes only with role-assignment permissions. Otherwise arrange data-plane access with an administrator. |

Explicit flags skip the corresponding questions, including
`--assign-roles=false` and `--foundry-prefix ""`. The wizard does not reuse
deployment settings from another project's selected environment.

The following illustrative console session shows the complete generation
flow with model deployment disabled. IDs, paths, resource suffixes, and azd
prompt styling are examples, not a recording of a new live deployment.
The user in this example has permission to assign roles.

```text
$ azd ai cu infra generate --output-dir provision --models none
azd ai cu infra generate -> /work/provision

`azd ai cu infra generate` generates an azd template. After the Microsoft Foundry resource is provisioned, its post-provision script can optionally deploy supported chat completion and embeddings models for prebuilt analyzers such as prebuilt-invoice and for custom analyzers.
It doesn't create any Azure resources itself - you run azd up afterwards to do the actual provisioning.
Choose the Azure subscription used to provision resources, then configure the deployment.

? Select the Azure subscription for the generated Content Understanding project: Development (<subscription-id>)
  Azure subscription: Development (<subscription-id>)

Input your azd environment name. An azd environment name:
  - lets azd store this deployment's config + outputs under .azure/<env>/ in the generated directory.
  - seeds your Azure resource names (e.g. rg-<env>, proj-<env>).
  - keeps separate stacks apart, such as dev, test, or prod.
? Enter your environment name (1-64 letters, numbers, -, _, ., or parentheses): dev

Content Understanding supported regions
Check the latest region support at https://learn.microsoft.com/azure/ai-services/content-understanding/language-region-support
? Azure region (where the Foundry resource is created): eastus2

Microsoft Foundry resource naming
The resource name becomes part of the public endpoint host (for example, https://<name>.services.ai.azure.com), so it must be globally unique.
If you provide a prefix, azd constructs the resource name as <prefix>-<unique-suffix>. Without a prefix, it uses aif-<unique-suffix>.
? Optional Microsoft Foundry resource name prefix (lowercase letters, numbers, hyphen; blank to skip): my-cu

RBAC roles
Required for Entra-based auth from `azd ai cu`. The template assigns Cognitive Services User.
Needs Owner, User Access Administrator, or Role Based Access Control Administrator on the subscription.
Pick 'n' if you only have Contributor. If you skip assignment, ask an administrator to grant access unless you already have it.
Provisioning and post-provision setup use Entra ID, not an API key.
? Assign RBAC roles to your user on the Microsoft Foundry resource? Yes

Generated project: /work/provision
Environment: dev
Region: eastus2
Subscription: Development (<subscription-id>)

Files written:
  .azure/config.json
  .azure/dev/.env
  .azure/dev/config.json
  README.md
  azure.yaml
  hooks/postprovision.ps1
  hooks/postprovision.sh
  infra/main.bicep
  infra/main.parameters.json
  infra/models.json
  infra/modules/foundry.bicep

No Azure resources were deployed.

Next steps:
  cd '/work/provision'
  azd auth login    # (one-time)
  azd up    # provisions a Microsoft Foundry resource without model deployments

The post-provision hook prints verified Content Understanding setup and test commands.

Run azd ai cu doctor afterwards to check the selected environment.
```

### Step 2: Deploy and complete setup

```bash
cd provision
azd up
```

The new-resource path creates a resource group, a Microsoft Foundry resource,
and a Foundry project. The postprovision hook then saves the runtime endpoint
and default analyzer.

This illustrative successful continuation includes the hook's complete JSON
result for a fresh resource with no models. Host progress wording, timing, and
resource names vary by azd version; no model picker appears with `--models none`.

```text
$ cd provision
$ azd up
Provisioning Azure resources (azd provision)
  (✓) Done: Resource group: rg-dev
  (✓) Done: Azure AI Services: my-cu-<unique-suffix>
  (✓) Done: Microsoft Foundry project: proj-dev
{
  "succeeded": true,
  "environmentConfigured": true,
  "environmentPreserved": false,
  "configurationSetupDisabled": false,
  "modelSetup": "completed",
  "defaultsConfigured": false,
  "generativeReady": false,
  "warnings": [],
  "messages": [
    "No model deployments selected.",
    "Available without an LLM or embeddings model: prebuilt-digitalParse, prebuilt-read, prebuilt-layout",
    "No optional models were configured; content extraction analyzers remain available."
  ]
}
SUCCESS: Your up workflow to provision and deploy to Azure completed.
```

`generativeReady: false` is expected in this model-free example. Check the
selected environment with `azd ai cu doctor --environment dev`; see
[readiness diagnostics](#diagnose-readiness) for exit-code semantics.
If model defaults have not been set, `doctor` reports incomplete generative
readiness without creating models or initializing defaults.
If role assignment was skipped, obtain data-plane access before expecting setup
to complete. Errors or warnings are not evidence of a successful setup.

<a id="deploy-models-and-configure-defaults"></a>

### Optional: Deploy models or reuse an existing resource

To select models interactively, omit `--models` in step 1. After provisioning,
the picker shows the live supported catalog, including name, version, kind,
and SKU. Recommended models are marked and preselected when unambiguous.
Submit a selection and explicitly confirm deployment (default No), or clear
all selections to deploy no models. Conflicting versions show a visible reason
and retain selections for correction. Declining confirmation cancels setup;
it does not roll back already provisioned resources.

To use recommended models without a picker, pass `--models recommended`.
You can also pass comma-separated `model@version` selectors. Model availability
and quota vary by region; consult the current
[supported models](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits#supported-generative-models).

To reuse a resource, replace step 1 with:

```bash
azd ai cu infra generate \
  --foundry-endpoint "https://<resource-name>.services.ai.azure.com/" \
  --models recommended \
  --output-dir provision
```

Then run step 2. Generation discovers the resource group and region and skips
the new-resource prefix, region, and role questions. It still selects a
subscription unless `--subscription` is supplied. This path creates no new
Foundry resource, project, or role assignment; it can deploy models and
configure defaults on the existing resource.
Do not combine `--foundry-endpoint` with `--foundry-prefix` or `--assign-roles`.

## Coding agent unattended workflow

Use this path only after the user or pipeline owner has authorized the
subscription, resource scope, role assignments, and Azure charges.
Authentication and required permissions must already be configured;
`--no-prompt` does not sign in or grant access. For CI credentials, follow
[azd pipeline authentication](https://learn.microsoft.com/azure/developer/azure-developer-cli/pipeline-github-actions).
Do not put secrets in command examples or generated project files.

### Create a new resource without prompts

This Bash example explicitly selects the subscription and all wizard choices.
Replace the subscription placeholder before running. It assumes the identity
already has data-plane access; if it does not, arrange access or deliberately
choose `--assign-roles` with an authorized role-assigning identity.

```bash
set -e
azd ai cu infra generate \
  --subscription "<subscription-name-or-GUID>" \
  --environment dev \
  --location eastus2 \
  --foundry-prefix my-cu \
  --no-assign-roles \
  --models none \
  --output-dir provision \
  --no-prompt
cd provision
AZD_NO_PROMPT=true azd up --environment dev --no-prompt
```

Generation writes files only. The `azd up` line creates Azure resources.
Use `--no-prompt` on **both** commands, and propagate `AZD_NO_PROMPT=true`
to hook subprocesses as shown. In PowerShell, set
`$env:AZD_NO_PROMPT = "true"` before running the commands and stop on any
nonzero native-command exit code.

For unattended generative setup, replace `--models none` with
`--models recommended` or explicit comma-separated `model@version` selectors.
Do not leave the default `prompt`: a noninteractive postprovision run reports
optional model setup as failed instead of opening a picker. Optional failures
can be warnings without failing resource provisioning, so inspect setup
results as well as command exit codes.

### Reuse an existing resource without prompts

```bash
set -e
azd ai cu infra generate \
  --subscription "<subscription-name-or-GUID>" \
  --environment dev \
  --foundry-endpoint "https://<resource-name>.services.ai.azure.com/" \
  --models recommended \
  --output-dir provision \
  --no-prompt
cd provision
AZD_NO_PROMPT=true azd up --environment dev --no-prompt
```

The selected subscription must contain the resource. Neither a prefix nor
`--assign-roles` is applicable here. Omitting location uses the discovered
region when available, otherwise `eastus2`; the generated template validates
its supported regions.

### Unattended inputs and defaults

These are command inputs, not a request to set every generated environment
variable manually:

| Input | Requirement and behavior |
| --- | --- |
| `--subscription` | Recommended for deterministic automation. Accepts an accessible subscription name or GUID, case-insensitively. IDs take precedence over names; duplicate names require an ID. An explicit empty, unknown, or ambiguous selector fails without falling back or changing azd defaults. |
| `--environment` | Recommended explicit stack name for both commands. Otherwise generation uses inherited `AZD_ENVIRONMENT`, then `dev`. |
| `--location` | Recommended for a new resource; defaults to `eastus2`. For existing resources, uses discovery when omitted. |
| `--models` | Use `none`, `recommended`, or comma-separated `model@version` selectors. Default `prompt` requires interactive model setup after provisioning. |
| `--foundry-endpoint` | Required only when reusing an existing resource. Accepts an account name, ARM ID, or resource endpoint. Omit for a new resource. |
| `--foundry-prefix` | Optional new-resource prefix. Omit or pass `""` for the generated `aif-` naming default. Cannot be combined with `--foundry-endpoint`. |
| `--assign-roles` / `--no-assign-roles` | Optional new-resource role choice. No assignment by default with `--no-prompt`. The flags conflict with each other; even `--assign-roles=false` conflicts with an existing-resource selector. |
| `--output-dir` | Optional generated directory; defaults to `provision`. Use an empty directory for a fresh project. |
| `--api-version` | Optional API version saved for postprovision; defaults to the extension's GA version. Generation does not infer it from process `CU_API_VERSION`. |
| `--force` | Optional replacement of known generated files and CU configuration. Review the [saved-state rules](#saved-project-settings) before using it. |
| `--no-prompt` | Required on generation and deployment for unattended usage. Does not imply consent to model selection or grant permissions. |
| `--output json` | Optional structured generation result with `outputDirectory`, `files`, and `nextSteps`. Does not by itself disable subscription prompts. Use `--no-prompt` as well. |
| Process `AZD_ENVIRONMENT` | Inherited azd environment selection when `--environment` is omitted. Explicit `--environment` wins. |
| Process `AZD_NO_PROMPT` | Host-level noninteractive setting. Set to `true` for the deployment and its generated hook subprocesses, in addition to the explicit flags shown above. |
| Process `CU_DISABLE_AUTO_CONFIG` | Optional postprovision opt-out, not a generation input. See [process-only controls](#postprovision-only-override). |

Without `--subscription`, interactive generation uses azd's selector.
Unattended generation checks `defaults.subscription`, then a host-marked
default or sole accessible subscription. An inaccessible configured default
fails; ambiguous choices require an explicit selector or configuration.
An explicit selector bypasses the saved default entirely, including a stale
or invalid default. Prefer it over changing global configuration in a script.

`CU_API_KEY` is not used for provisioning. Process `CU_ENDPOINT` does not
select an existing resource for generation; use `--foundry-endpoint`.
Saved deployment inputs and outputs below are not additional required process
variables.

After deployment, inspect the hook result and
`azd ai cu doctor --environment dev --output json --no-prompt`.
An exit code of zero alone does not establish generative readiness:
check `generativeReady` in the hook result and `ready` in the doctor report.
For intentional model-free use, generative readiness is not required.

## Saved project settings

Generation saves resource choices in the selected azd environment.
`infra/main.parameters.json` uses environment substitutions under
`parameters.<name>.value`, so dev and prod can target different resources in
the same generated directory. Regeneration changes only the selected environment's
choices, except when migrating older saved input names or shared literal parameters
as described below.
Recognized projects retain editable templates and model files unless `--force`
is supplied. Other parameter fields are preserved.

| Saved provisioning input | Bicep parameter |
| --- | --- |
| `CU_FOUNDRY_RESOURCE_PREFIX` | `foundryResourcePrefix`: optional prefix for a new resource. |
| `CU_FOUNDRY_EXISTING_ENDPOINT` | `existingFoundryEndpoint`: existing resource endpoint, or empty for a new resource. |
| `CU_FOUNDRY_EXISTING_RESOURCE_GROUP` | `existingFoundryResourceGroup`: existing resource group, or empty for a new resource. |
| `CU_ASSIGN_ROLES` | `assignRolesToPrincipal`: `true` or `false` for role assignments on a new resource. |

These are deployment inputs saved under `.azure/<environment>/.env`, not CU
runtime settings or `_CU_PROVISIONED_*` deployment outputs. Saving them does not
export them into the parent shell. Inspect them with `azd env get-values -e dev`
or change them with `azd env set <name> <value> -e dev`. Keep a new-resource prefix
and an existing endpoint mutually exclusive.

Older generated projects using `FOUNDRY_RESOURCE_PREFIX`,
`FOUNDRY_EXISTING_ENDPOINT`, `FOUNDRY_EXISTING_RESOURCE_GROUP`, or `AZD_ASSIGN_ROLES`
continue to work with their existing parameter files. On regeneration, including
with `--force`, generation updates the parameter references to the names above.
For other existing environments, it copies each old saved value only when the
new key is absent; a present new key wins, even when empty. Missing values use
the parameter default (empty for resource selectors, `false` for role assignment).
Old keys are retained but no longer used by the updated parameter file.
The selected environment uses the current generation choices instead.

For older shared literal resource parameters, generation preserves their effective
values in other existing environments before changing the shared references.
Those literals take precedence over saved keys because they controlled the previous
deployment target. Values already overwritten by an earlier generation cannot be
recovered. Unsupported resource expressions or unsafe saved inputs fail before
any files are written.

Force replaces known generated assets, including `infra/models.json`, and
allows CU configuration replacement; unrelated files are not deleted.
The existing model-setup completion marker is preserved.

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
`--force`.

Generation saves postprovision choices in the selected environment's
`cu` configuration, not in dedicated environment variables:

| Saved environment setting | Purpose |
| --- | --- |
| `cu.modelSelection` | Generated from `--models`: `prompt` (default), `recommended`, `none`, or comma-separated `model@version` selectors. Use `azd env config set cu.modelSelection recommended` for unattended selection, or `none` to skip model deployment. |
| `cu.forceConfigurationSetup` | Boolean allowing the hook to replace existing CU environment configuration. Generation saves `true` with `--force`, otherwise `false`. Does not reset completed model setup. |

Successful model setup records the boolean `cu.modelSetupComplete` in azd
environment configuration. Inspect this marker and model mappings with
`azd env config get cu`.

### Deployment outputs

The generated Bicep declares the following outputs. After provisioning, azd
saves them in the selected project's environment. They describe the provisioned
resource; they are not user-supplied generation settings.

Inspect the saved values from the generated directory:

```bash
azd env get-values
```

| Output | Purpose |
| --- | --- |
| `_CU_PROVISIONED_LOCATION` | Deployment location. |
| `_CU_PROVISIONED_RESOURCE_GROUP` | Created or existing resource group containing the Foundry resource. |
| `_CU_PROVISIONED_SUBSCRIPTION_ID` | Subscription containing the resource. |
| `_CU_PROVISIONED_TENANT_ID` | Tenant of the deployment subscription, not necessarily the signed-in user's access tenant. Postprovision looks up the access tenant through azd. |
| `_CU_PROVISIONED_ENDPOINT` | Created or adopted resource endpoint, used by postprovision setup. |
| `_CU_PROVISIONED_RESOURCE_NAME` | Created or adopted account name, used by postprovision model setup. |
| `_CU_PROVISIONED_PROJECT_NAME` | Foundry project name when available; may be empty for an existing resource. |
| `_CU_PROVISIONED_PROJECT_ENDPOINT` | Project-scoped endpoint when available; not the resource endpoint required by CU commands. |
| `_CU_PROVISIONED_MODEL_DEPLOYMENTS` | Model deployments declared in Bicep, not CU service-default mappings. Subsequent live hook selections are saved in `infra/models.json`. |

Saving these outputs does not export them into the parent shell. azd makes
environment values available to hook subprocesses, and the postprovision
command reads the selected azd environment.

The `_CU_PROVISIONED_*` prefix identifies deployment results, not user settings.
Generation saves standard azd inputs such as `AZURE_ENV_NAME`,
`AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID`, and `AZURE_TENANT_ID`; these support
azd's deployment context and are not Bicep outputs. CU commands use provisioned
subscription and resource-group metadata as saved scope hints, with explicit
flags and process scope inputs taking precedence.

Automatic postprovision configuration copies the provisioned resource endpoint
to the runtime setting `CU_ENDPOINT`, unless existing CU configuration is
preserved. `_CU_PROVISIONED_ENDPOINT` is not a runtime endpoint alias. For
user-supplied settings, see the
[runtime environment-variable reference](../../../docs/environment-variables.md#azureaicontent-understanding).

### Postprovision-only override

`CU_DISABLE_AUTO_CONFIG` is an optional process-only control for the
postprovision hook, not a durable project setting or an interactive-answer
handoff. Generation does not save it.

Values `1`, `true`, `yes`, or `on` enable the override, case-insensitively.
Other values, including an unset value, leave it disabled. When enabled, it
skips automatic CU configuration, not resource provisioning or model setup.
Bicep saves `_CU_PROVISIONED_ENDPOINT` as deployment metadata. When automatic
configuration is disabled, configure the runtime setting `CU_ENDPOINT` yourself
or pass `--endpoint` to CU commands.

For example, in Bash:

```bash
CU_DISABLE_AUTO_CONFIG=true azd up
```

### Compatibility with existing generated projects

The hook still supports these saved azd environment values for older generated
projects. New projects use the environment config settings and deployment
outputs described above.

| Legacy saved value | Compatibility behavior |
| --- | --- |
| `CU_MODEL_SELECTION` | Read only when `cu.modelSelection` is absent. New generation saves the `--models` choice in environment config. |
| `CU_CONFIG_SETUP_FORCE` | Read only when `cu.forceConfigurationSetup` is absent. New generation saves the boolean `--force` choice in environment config. |
| `CU_MODEL_SETUP_COMPLETE` | Preserved but ignored. The completion marker is `cu.modelSetupComplete` in environment config. |
| `CU_ENDPOINT` | Used as the postprovision endpoint only when `_CU_PROVISIONED_ENDPOINT` is absent. Remains a supported runtime setting. |
| `CU_RESOURCE_NAME`, `FOUNDRY_RESOURCE_NAME` | Read in this order only when `_CU_PROVISIONED_RESOURCE_NAME` is absent. |
| `AZURE_SUBSCRIPTION_ID`, `AZURE_RESOURCE_GROUP` | Saved scope fallbacks when the corresponding `_CU_PROVISIONED_*` outputs are absent. Standard azd inputs remain supported. |

New config fields take precedence, including explicit `false` values; malformed
new fields fail rather than falling back. Postprovision also checks new output
names by presence: an empty or invalid required output fails instead of using a
stale legacy value.
For projects generated with shared literal resource parameters, regeneration
converts them to environment substitutions and saves the old literals in every
other existing environment before applying the selected environment's new
choices. This preserves the other environments'
effective resource parameters, including when using `--force`; it cannot recover
choices already overwritten by an earlier generation. Unrelated environment
entries and other environments' config files are preserved. Unsupported custom
resource-parameter expressions or unsafe environment files fail before any writes;
review the parameter file or generate into a new directory.

Use `--force` only
when you also intend to replace those templates with the new
`_CU_PROVISIONED_*` outputs. Old saved values are not automatically deleted.

### Verify setup

Verify the selected environment from the generated directory:

```bash
azd ai cu doctor --environment dev
azd ai cu defaults show --environment dev
```

Replace `dev` if you generated a different environment.

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
