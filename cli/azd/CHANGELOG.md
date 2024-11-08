# Release History

## 1.11.0-beta.1 (Unreleased)

### Features Added

### Breaking Changes

### Bugs Fixed

### Other Changes

## 1.10.4 (2024-11-06)

### Bugs Fixed

- [[4039]](https://github.com/Azure/azure-dev/pull/4039) Use DOTNET_CONTAINER.
- [[4426]](https://github.com/Azure/azure-dev/pull/4426) Show inner error description for stack deployments.
- [[4472]](https://github.com/Azure/azure-dev/pull/4472) Fix projects with empty spaces.
- [[4458]](https://github.com/Azure/azure-dev/pull/4458) Fix panic on empty hooks.
- [[4484]](https://github.com/Azure/azure-dev/pull/4484) Fix missing quotes in Aspire projects.
- [[4515]](https://github.com/Azure/azure-dev/pull/4515) Use DOTNET_NOLOGO for Aspire projects.

## 1.10.3 (2024-10-16)

### Bugs Fixed

- [[4450]](https://github.com/Azure/azure-dev/pull/4450) fix `persistSettings` alpha feature.

## 1.10.2 (2024-10-08)

### Features Added

- [[4272]](https://github.com/Azure/azure-dev/pull/4272) Supports configurable `api-version` for container app deployments.
- [[4286]](https://github.com/Azure/azure-dev/pull/4286) Adds `alpha` feature `alpha.aspire.useBicepForContainerApps` to use bicep for container app deployment.
- [[4371]](https://github.com/Azure/azure-dev/pull/4371) Adds support for `default.value` for `parameter.v0`.

### Bugs Fixed

- [[4375]](https://github.com/Azure/azure-dev/pull/4375) Enables remote build support for AKS.
- [[4363]](https://github.com/Azure/azure-dev/pull/4363) Fix environment variables to be evaluated too early for `main.parameters.json`.

### Other Changes

- [[4336]](https://github.com/Azure/azure-dev/pull/4336) Adds spinner to `azd down`.
- [[4357]](https://github.com/Azure/azure-dev/pull/4357) Updates `azure.yaml.json` for `remoteBuild`.
- [[4369]](https://github.com/Azure/azure-dev/pull/4369) Updates docker `buildargs` to expandable strings.
- [[4331]](https://github.com/Azure/azure-dev/pull/4331) Exposes configurable settings for `actionOnUnmanage` and `denySettings` for Azure Deployment Stacks (alpha).

## 1.10.1 (2024-09-05)

### Bugs Fixed

- [[4299]](https://github.com/Azure/azure-dev/pull/4299) Fixes issue in vs-server for Aspire projects.
- [[4294]](https://github.com/Azure/azure-dev/pull/4294) Fixes azd pipeline config on Codespaces.
- [[4295]](https://github.com/Azure/azure-dev/pull/4295) Fixes azd pipeline config for Terraform.

## 1.10.0 (2024-09-04)

### Features Added

- [[4165]](https://github.com/Azure/azure-dev/pull/4165) Add support for `alpha` feature Azure Deployment stacks.
- [[4236]](https://github.com/Azure/azure-dev/pull/4236) Support `args` on `container.{v0,v1}`.
- [[4257]](https://github.com/Azure/azure-dev/pull/4257) Add support for multiple hooks per event.
- [[4190]](https://github.com/Azure/azure-dev/pull/4190) Add support for `.azuredevops` folder.
- [[4161]](https://github.com/Azure/azure-dev/pull/4161) Add remote builds support with Azure Container Registry.
- [[4254]](https://github.com/Azure/azure-dev/pull/4254) Add support for environment variable substitution for source container image.
- [[4203]](https://github.com/Azure/azure-dev/pull/4203) Add GitHub as template source configuration option.
- [[4208]](https://github.com/Azure/azure-dev/pull/4208) Add support for Java Azure Functions.

### Bugs Fixed

- [[4237]](https://github.com/Azure/azure-dev/pull/4237) Fix pipeline config failing bug.
- [[4263]](https://github.com/Azure/azure-dev/pull/4263) Fix `azd infra synth` ignored by `azd deploy` in azdo CI/CD pipeline bug.
- [[4281]](https://github.com/Azure/azure-dev/pull/4281) Fix failed provision with the STG location.

### Other Changes

- [[4243]](https://github.com/Azure/azure-dev/pull/4243) Add AI services model deployments to provisioning display.

## 1.9.6 (2024-08-13)

### Features Added

- [[4115]](https://github.com/Azure/azure-dev/pull/4115) Adding `alpha` feature `alpha.aca.persistIngressSessionAffinity`.

### Bugs Fixed

- [[4111]](https://github.com/Azure/azure-dev/pull/4111) Container Apps: Fail when explicit Dockerfile path not found.
- [[4149]](https://github.com/Azure/azure-dev/pull/4149) Remove Admin Access as default for all .Net Aspire services.
- [[4104]](https://github.com/Azure/azure-dev/pull/4104) Remove Azure Dev Ops git remote constraint for dev.azure.com only.
- [[4160]](https://github.com/Azure/azure-dev/pull/4160) Fix automatic generation of CI/CD files for .Net Aspire projects.
- [[4182]](https://github.com/Azure/azure-dev/pull/4182) Allow `.yaml` and `.yml` extension for azure-dev pipeline files.
- [[4187]](https://github.com/Azure/azure-dev/pull/4187) Fix panic during deployment progress rendering.

## 1.9.5 (2024-07-10)

### Features Added

- [[4080]](https://github.com/Azure/azure-dev/pull/4080) Add `azd env get-value`.

### Bugs Fixed

- [[4065]](https://github.com/Azure/azure-dev/pull/4065) Fix panic when a project has no endpoints.
- [[4074]](https://github.com/Azure/azure-dev/pull/4074) Fix error in retrieving cross-rg service plan.
- [[4073]](https://github.com/Azure/azure-dev/pull/4073) Fix bug where windows logic app passed isLinuxWebApp.

## 1.9.4 (2024-07-02)

### Features Added

- [[3924]](https://github.com/Azure/azure-dev/pull/3924) Updating azd pipeline config to support Federated Credential for Azure DevOps.
- [[3553]](https://github.com/Azure/azure-dev/pull/3553) Support swa-cli.config.json for Azure Static Web Apps.
- [[3955]](https://github.com/Azure/azure-dev/pull/3955) Adding `alpha` feature `alpha.aca.persistDomains`.
- [[3723]](https://github.com/Azure/azure-dev/pull/3723) Add --managed-identity to azd auth login.
- [[3965]](https://github.com/Azure/azure-dev/pull/3965) Add deployment status tracking for linux web apps.
- [[4003]](https://github.com/Azure/azure-dev/pull/4003) Add support for deploying flex-consumption function apps.
- [[4008]](https://github.com/Azure/azure-dev/pull/4008) Add support for container.v1 [Aspire].
- [[4030]](https://github.com/Azure/azure-dev/pull/4030) Prompt to add pipeline definition file during azd pipeline config.
- [[3790]](https://github.com/Azure/azure-dev/pull/3790) Adding `alpha` feature `azd.operations` to support .Net Aspire bind mounts.
- [[4049]](https://github.com/Azure/azure-dev/pull/4049) Adding pipeline config `--applicationServiceManagementReference`.

### Bugs Fixed

- [[3941]](https://github.com/Azure/azure-dev/pull/3941) Fix exposed ports for Aspire projects.
- [[3948]](https://github.com/Azure/azure-dev/pull/3948) Adds missing namespace property to Helm configuration schema.
- [[3942]](https://github.com/Azure/azure-dev/pull/3942) Fixes issue selected environment with different environment type.
- [[3985]](https://github.com/Azure/azure-dev/pull/3985) Reset the read cursor in zip deployments to fix bugs in retry.

### Other Changes

- [[4043]](https://github.com/Azure/azure-dev/pull/4043) wait for Ai-studio deployments before polling.

## 1.9.3 (2024-05-20)

### Other Changes

- [[3925]](https://github.com/Azure/azure-dev/pull/3925) Graduates alpha feature: `Aspire Dashboard`
- [[3929]](https://github.com/Azure/azure-dev/pull/3929) Graduates alpha feature: `Aspire Auto Configure Data Protection`

## 1.9.2 (2024-05-15)

### Bugs Fixed

- [[3915]](https://github.com/Azure/azure-dev/pull/3915) Revert - Add deployment status tracking for linux web apps.

## 1.9.1 (2024-05-14)

### Bugs Fixed

- [[3876]](https://github.com/Azure/azure-dev/pull/3876) Take infra section of azure.yaml into account.
- [[3881]](https://github.com/Azure/azure-dev/pull/3881) Make azd to wait until the expected state can be seen from the online endpoint.
- [[3763]](https://github.com/Azure/azure-dev/pull/3763) Add deployment status tracking for linux web apps.
- [[3897]](https://github.com/Azure/azure-dev/pull/3897) Update ResolvedRaw() to remove reference to the vault.
- [[3898]](https://github.com/Azure/azure-dev/pull/3898) Easy Init: Improve handling for empty state.
- [[3903]](https://github.com/Azure/azure-dev/pull/3903) Fix type issues in PromptDialog with external prompting.

## 1.9.0 (2024-05-07)

### Features Added

- [[3718]](https://github.com/Azure/azure-dev/pull/3718) Deploy AI/ML studio online endpoints with host `ml.endpoint`. Starter templates `azd-ai-starter` and `azd-aistudio-starter` are available to get started with ease.
- [[3840]](https://github.com/Azure/azure-dev/pull/3840) Filter templates when running `azd init` or `azd template list` with `--filter`
- .NET Aspire:
  - [[3267]](https://github.com/Azure/azure-dev/pull/3267) Support services with multiple exposed ports
  - [[3820]](https://github.com/Azure/azure-dev/pull/3820) Container resources now supports reference expressions, and are now modeled the same as project resources

### Bugs Fixed

- [[3822]](https://github.com/Azure/azure-dev/pull/3822) Fix Aspire KeyVault references in manifest files
- [[3858]](https://github.com/Azure/azure-dev/pull/3858) Allow overriding location for Aspire bicep modules

### Other Changes

- [[3821]](https://github.com/Azure/azure-dev/pull/3821) Support running `azd init` in Aspire app host directory
- [[3848]](https://github.com/Azure/azure-dev/pull/3848) Add "Demo Mode" which hides subscription IDs
- [[3828]](https://github.com/Azure/azure-dev/pull/3828) Update Bicep CLI to version 0.26.170.
- [[3800]](https://github.com/Azure/azure-dev/pull/3800) Write ACA Container Manifests in the `infra` directory under the AppHost during `infra synth`.

**Note:** If you had previously used `infra synth`, you will need to move the container app manifests from their old location to the new one for `azd` to use them. If you do not do so, `azd` will generate the default IaC based on your current app host. To do this, move the `containerApp.tmpl.yaml` file in the `manifests` folder under each individual project into an `infra` folder next to the `.csproj` file for your project's Aspire App Host and rename it from `containerApp.tmpl.yaml` to `<name-passed-to-AddProject>.tmpl.yaml` (e.g. `apiserver.tmpl.yaml`, if you write `builder.AddProject<...>("apiserver")`).

## 1.8.2 (2024-04-30)

### Features Added

- [[3804]](https://github.com/Azure/azure-dev/pull/3804) Add user vault storage for development secrets
- [[3755]](https://github.com/Azure/azure-dev/pull/3755) Store `secure()` Bicep parameters outside source tree

### Bugs Fixed

- [[3788]](https://github.com/Azure/azure-dev/pull/3788) Avoid panic in prompting with option details
- [[3796]](https://github.com/Azure/azure-dev/pull/3796) Fix `env refresh` failing when no bicep files are present
- [[3801]](https://github.com/Azure/azure-dev/pull/3801) Fix `azd provision` failing for `.bicepparam` files

### Other Changes

- [[3798]](https://github.com/Azure/azure-dev/pull/3798) Update provider.tf files with skip_provider_registration = "true"

## 1.8.1 (2024-04-23)

### Features Added

- [[3731]](https://github.com/Azure/azure-dev/pull/3731) Support Data Protection Runtime feature for .NET Aspire in ACA under feature flag `azd config set alpha.aspire.autoConfigureDataProtection on`
- [[3715]](https://github.com/Azure/azure-dev/pull/3715) Improved security to prevent committing an environment to the repository

### Bugs Fixed

- [[3748]](https://github.com/Azure/azure-dev/pull/3748) Fix cross-build configuration

## 1.8.0 (2024-04-09)

### Features Added

- [[3569]](https://github.com/Azure/azure-dev/pull/3569) Adds `--from-code ` flag to initialize from existing code when running `azd init`
- Dotnet Aspire:
  - [[3612]](https://github.com/Azure/azure-dev/pull/3612) Supports Aspire apps with multiple exposed ports
  - [[3484]](https://github.com/Azure/azure-dev/pull/3484) Discovers export port from the result of `dotnet publish`
  - [[3556]](https://github.com/Azure/azure-dev/pull/3556) Adds Aspire volumes support
  - [[3561]](https://github.com/Azure/azure-dev/pull/3561) Supports more input generation in Aspire manifest

### Breaking Changes

- [[3589]](https://github.com/Azure/azure-dev/pull/3589) Secrets are now marked as secure() in `container-app.bicep` and `container-app-upsert.bicep`. Thanks @pamelafox for the contribution
- [[3594]](https://github.com/Azure/azure-dev/pull/3594) Updates Node.js version to 20 for templates and pipelines
- [[3578]](https://github.com/Azure/azure-dev/issues/3578) Updates Node.js version to 20 for [installing `azd` GitHub Action](https://github.com/Azure/setup-azd)

### Bugs Fixed

- [[3651]](https://github.com/Azure/azure-dev/pull/3651) Fixes trailing comma for `todo-nodejs-mongo-aks` template's invalid url in GitHub Action
- [[3638]](https://github.com/Azure/azure-dev/pull/3638) Fixes `InvalidAuthenticationTokenTenant` error
- Dotnet Aspire: 
  - [[3610]](https://github.com/Azure/azure-dev/pull/3610) Fixes too long auto-generated Azure Key Vault name by using Hash
  - [[3650]](https://github.com/Azure/azure-dev/pull/3650) Writes default port to manifest for docker
  - [[3545]](https://github.com/Azure/azure-dev/pull/3545) Updates Aspire generator to use the build args from the dockerfile resources
  - [[3554]](https://github.com/Azure/azure-dev/pull/3554) Fixes `azd infra synth` doesn't convert dashes to underscores in `containerApp.tmpl.yaml`

### Other Changes

- [[3522]](https://github.com/Azure/azure-dev/pull/3522) Fixes typo in `next-steps.md`. Thanks @mikekistler for the contribution
- [[3495]](https://github.com/Azure/azure-dev/pull/3495) Updates `infra/core` to adapt more azdevify templates
- [[3171]](https://github.com/Azure/azure-dev/pull/3171) Updates web project `react-fluentui` to use `vite`

## 1.7.0 (2024-03-12)

### Features Added

- [[3450]](https://github.com/Azure/azure-dev/pull/3450) Adds support for pushing container images to external container registries
- [[3452]](https://github.com/Azure/azure-dev/pull/3452) Adds support for other clouds
- Dotnet Aspire:
  - [[3349]](https://github.com/Azure/azure-dev/pull/3349) Adds support for bicep and prompts for parameters
  - [[3411]](https://github.com/Azure/azure-dev/pull/3411) Adds support for `value.v0`
  - [[3425]](https://github.com/Azure/azure-dev/pull/3425) Sets `DOTNET_ENVIRONMENT` when running AppHost

### Bugs Fixed

- [[3381]](https://github.com/Azure/azure-dev/pull/3381) Removes session `container` and `manifest` caching
- [[3407]](https://github.com/Azure/azure-dev/pull/3407) Fixes docker build/package for Aspire projects
- [[3418]](https://github.com/Azure/azure-dev/pull/3418) Fixes issues where deploying to AKS fails when service does not build any container
- [[3445]](https://github.com/Azure/azure-dev/pull/3445) Fixes concurrent map issues in dev center client
- [[3390]](https://github.com/Azure/azure-dev/pull/3390) Fixes issues where the ADE configuration was not being refreshed during `azd init` or `azd provision` in dev center
- [[3382]](https://github.com/Azure/azure-dev/pull/3382) Cleans empty secrets and variables before setting them again
- [[3448]](https://github.com/Azure/azure-dev/pull/3448) Fixes issues where `azd infra synth` doesn't generate autogenerate inputs
- [[3506]](https://github.com/Azure/azure-dev/pull/3506) Fixes service config handlers referencing stale components
- [[3513]](https://github.com/Azure/azure-dev/pull/3513) Fixes rules for setting secret environment variables in Aspire
- [[3516]](https://github.com/Azure/azure-dev/pull/3516) Fixes issues where output bicep is invalid when using dash in resource names

### Other Changes

- [[3357]](https://github.com/Azure/azure-dev/pull/3357) Allows selection on existing environments when default environment isn't set
- [[3282]](https://github.com/Azure/azure-dev/pull/3282) Updates `azure-dev.yaml` for `azd-starter-bicep`. Thanks @IEvangelist for the contribution
- [[3334]](https://github.com/Azure/azure-dev/pull/3334) Adds MySQL to bicep core. Thanks @john0isaac for the contribution
- [[3413]](https://github.com/Azure/azure-dev/pull/3413) Adds Azure App Configuration store to bicep core. Thanks @RichardChen820 for the contribution
- [[3442]](https://github.com/Azure/azure-dev/pull/3442) Updates AKS template tests without playwright validation
- [[3478]](https://github.com/Azure/azure-dev/pull/3478) Updates `azd` to use default http client

## 1.6.1 (2024-02-15)

### Bugs Fixed

- [[3375]](https://github.com/Azure/azure-dev/pull/3375) Fixes issues deploying to AKS service targets
- [[3373]](https://github.com/Azure/azure-dev/pull/3373) Fixes resolution of AZD compatible templates within azure dev center catalogs
- [[3372]](https://github.com/Azure/azure-dev/pull/3372) Removes requirement for dev center projects to include an `infra` folder

## 1.6.0 (2024-02-13)

### Features Added

- [[3269]](https://github.com/Azure/azure-dev/pull/3269) Adds support for external/prebuilt container image references
- [[3251]](https://github.com/Azure/azure-dev/pull/3251) Adds additional configuration resolving container registry names
- [[3249]](https://github.com/Azure/azure-dev/pull/3249) Adds additional configuration resolving AKS cluster names
- [[3223]](https://github.com/Azure/azure-dev/pull/3223) Updates AKS core modules for `azd` to easily enable RBAC clusters
- [[3211]](https://github.com/Azure/azure-dev/pull/3211) Adds support for RBAC enabled AKS clusters using `kubelogin`
- [[3196]](https://github.com/Azure/azure-dev/pull/3196) Adds support for Helm and Kustomize for AKS service targets
- [[3173]](https://github.com/Azure/azure-dev/pull/3173) Adds support for defining customizable `azd up` workflows
- Dotnet Aspire additions:
  - [[3164]](https://github.com/Azure/azure-dev/pull/3164) Azure Cosmos DB.
  - [[3226]](https://github.com/Azure/azure-dev/pull/3226) Azure SQL Database.
  - [[3276]](https://github.com/Azure/azure-dev/pull/3276) Secrets handling improvement.
- [[3155]](https://github.com/Azure/azure-dev/pull/3155) Adds support to define secrets and variables for `azd pipeline config`.

### Bugs Fixed

- [[3097]](https://github.com/Azure/azure-dev/pull/3097) For Dotnet Aspire projects, do not fail if folder `infra` is empty.

## 1.5.1 (2023-12-20)

### Features Added

- [[2998]](https://github.com/Azure/azure-dev/pull/2998) Adds support for Azure Storage Tables and Queues on Aspire projects.
- [[3052]](https://github.com/Azure/azure-dev/pull/3052) Adds `target` argument support for docker build.
- [[2488]](https://github.com/Azure/azure-dev/pull/2488) Adds support to override behavior of the KUBECONFIG environment variable on AKS.
- [[3075]](https://github.com/Azure/azure-dev/pull/3075) Adds support for `dockerfile.v0` on Aspire projects.
- [[2992]](https://github.com/Azure/azure-dev/pull/2992) Adds support for `dapr` on Aspire projects.

### Bugs Fixed

- [[2969]](https://github.com/Azure/azure-dev/pull/2969) Relax container names truncation logic for Aspire `redis.v0` and `postgres.database.v0`.
  Truncation now happens above 30 characters instead of 12 characters.
- [[3035]](https://github.com/Azure/azure-dev/pull/3035) .NET Aspire issues after `azd pipeline config`.
- [[3038]](https://github.com/Azure/azure-dev/pull/3038) Fix init to not consider parent directories.
- [[3045]](https://github.com/Azure/azure-dev/pull/3045) Handle interrupt to unhide cursor.
- [[3069]](https://github.com/Azure/azure-dev/pull/3069) .NET Aspire, enable `admin user` for ACR.
- [[3049]](https://github.com/Azure/azure-dev/pull/3049) Persist location from provisioning manager.
- [[3056]](https://github.com/Azure/azure-dev/pull/3056) Fix `azd pipeline config` for resource group deployment.
- [[3106]](https://github.com/Azure/azure-dev/pull/3106) Fix `azd restore` on .NET projects.
- [[3041]](https://github.com/Azure/azure-dev/pull/3041) Ensure azd environment name is synchronized to .env file.

### Other Changes

- [[3044]](https://github.com/Azure/azure-dev/pull/3044) Sets allowInsecure to true for internal services on Aspire projects.

## 1.5.0 (2023-11-15)

### Features Added

- [[2767]](https://github.com/Azure/azure-dev/pull/2767) Adds support for Azure Deployments Environments.

## 1.4.5 (2023-11-13)

### Bugs Fixed

- [[2962]](https://github.com/Azure/azure-dev/pull/2962) Fix for incorrect id on storage blob built-in role id.
- [[2963]](https://github.com/Azure/azure-dev/pull/2963) Handle project is undetected.

## 1.4.4 (2023-11-10)

### Features Added

- [[2893]](https://github.com/Azure/azure-dev/pull/2893) Added command `azd show`.
- [[2925]](https://github.com/Azure/azure-dev/pull/2925) Promote simplified `azd init` and Cloud Native buildpacks features to beta

## 1.4.3 (2023-10-24)

### Features Added

- [[2787]](https://github.com/Azure/azure-dev/pull/2787) Added `azd config show` and deprecated `azd config list`.

### Other Changes

- [[2887]](https://github.com/Azure/azure-dev/pull/2887) Update the subscription and location information during `azd provision`.

## 1.4.2 (2023-10-12)

### Features Added

- [[2845]](https://github.com/Azure/azure-dev/pull/2845) Feature Clickable Template Links in Terminal (azd template list). Thanks @john0isaac for the contribution
- [[2829]](https://github.com/Azure/azure-dev/pull/2829) Feature Display the Subscription Name and ID (azd provision). Thanks @john0isaac for the contribution

### Bugs Fixed

- [[2858]](https://github.com/Azure/azure-dev/pull/2858) Fixes issue with running VS Code Tasks that rely on environment configuration path.

## 1.4.1 (2023-10-06)

### Bugs Fixed

- [[2837]](https://github.com/Azure/azure-dev/pull/2837) `azd down` does not clear provision state.

## 1.4.0 (2023-10-05)

### Features Added

- [[2725]](https://github.com/Azure/azure-dev/pull/2725) Adds support for provision state to the bicep provider.
- [[2765]](https://github.com/Azure/azure-dev/pull/2765) Support for remote environments.
- [[1642]](https://github.com/Azure/azure-dev/pull/1642) A new `azd hooks run` command for running and testing your hooks.

### Bugs Fixed

- [[2793]](https://github.com/Azure/azure-dev/pull/2793) Support user defined types for the bicep provider.
- [[2543]](https://github.com/Azure/azure-dev/pull/2543) `azd package` now allows users to specify `--output-path` parameter to control the output location of file-based packages.
- [[2302]](https://github.com/Azure/azure-dev/pull/2302) `azd config --help` doesn't show help for `AZD_CONFIG_DIR`.
- [[2050]](https://github.com/Azure/azure-dev/pull/2050) `azd init` now supports `--subscription`.
- [[2695]](https://github.com/Azure/azure-dev/pull/2695) `azd` now honors `@allowed` locations in Bicep to filter the list of possible deploy locations.
- [[2599]](https://github.com/Azure/azure-dev/pull/2599) ARM64 support is now generally available.
- [[2683]](https://github.com/Azure/azure-dev/pull/2683) Bicep installer prefers MUSL variant over glibc.
- [[2794]](https://github.com/Azure/azure-dev/pull/2794) When running `azd init`, the Starter - Bicep template is unavailable.
  
### Other Changes

- [[#2796]](https://github.com/Azure/azure-dev/pull/2796) Update `terraform` provider from alpha to beta.

## 1.3.1 (2023-09-20)

### Minor Changes

- [[2737]](https://github.com/Azure/azure-dev/pull/2737) Update bicep to 0.21.1
- [[2696]](https://github.com/Azure/azure-dev/pull/2696) Support filtering for azd location in bicep
- [[2721]](https://github.com/Azure/azure-dev/pull/2721) `azd package` support for user specified output paths
- [[2756]](https://github.com/Azure/azure-dev/pull/2756) Minor enhancements to simplified init

### Bugs Fixed

- [[2719]](https://github.com/Azure/azure-dev/pull/2719) Fix mistypes in soft delete warning message
- [[2722]](https://github.com/Azure/azure-dev/pull/2722) Prefer glibc based Bicep when both musl and glibc are installed
- [[2726]](https://github.com/Azure/azure-dev/pull/2726) Mention `AZD_CONFIG_DIR` in `azd config --help` help text

## 1.3.0 (2023-09-06)

### Features Added

- [[2573]](https://github.com/Azure/azure-dev/pull/2573) Adds support for custom template sources.
- [[2637]](https://github.com/Azure/azure-dev/pull/2637) Awesome azd templates are now shown by default in `azd init` template listing.
- [[2628]](https://github.com/Azure/azure-dev/pull/2628) Support for `.bicepparam`.
- [[2700]](https://github.com/Azure/azure-dev/pull/2700) New simplified `azd init` to initialize your existing application for Azure (alpha feature)
- [[2678]](https://github.com/Azure/azure-dev/pull/2678) Support for Cloud Native Buildpacks (alpha feature)

### Breaking Changes

### Bugs Fixed

- [[2624]](https://github.com/Azure/azure-dev/pull/2624) Fix provisioning deployment display not showing progress when certain errors occur.
- [[2676]](https://github.com/Azure/azure-dev/pull/2676) Fix `buildArgs` support for docker build.
- [[2698]](https://github.com/Azure/azure-dev/pull/2698) Fix `azd auth login` default browser prompt in Codespaces environments.
- [[2664]](https://github.com/Azure/azure-dev/pull/2664) Fix `azd auth login` login loop after upgrading to 1.2.0.
- [[2630]](https://github.com/Azure/azure-dev/pull/2630) Fix coloring for ignored operations in `azd provision --preview`

### Other Changes

- [[2660]](https://github.com/Azure/azure-dev/pull/2660) Starter templates now include `core` libraries by default.

## 1.2.0 (2023-08-09)

### Features Added

- [[2550]](https://github.com/Azure/azure-dev/pull/2550) Add `--preview` to `azd provision` to get the changes.
- [[2521]](https://github.com/Azure/azure-dev/pull/2521) Support `--principal-id` param for azd pipeline config to reuse existing service principal.
- [[2455]](https://github.com/Azure/azure-dev/pull/2455) Adds optional support for text templates in AKS k8s manifests.

### Bugs Fixed

- [[2569]](https://github.com/Azure/azure-dev/pull/2569) Fix `azd down` so it works after a failed `azd provision`.
- [[2367]](https://github.com/Azure/azure-dev/pull/2367) Don't fail AKS deployment for failed environment substitution.
- [[2576]](https://github.com/Azure/azure-dev/pull/2576) Fix `azd auth login` unable to launch browser on WSL.

### Other changes

- [[2572]](https://github.com/Azure/azure-dev/pull/2572) Decrease expiration time of service principal secret from default (24 months) to 180 days.
- [[2500]](https://github.com/Azure/azure-dev/pull/2500) Promoted Azure Spring Apps from `alpha` to `beta`.

## 1.1.0 (2023-07-12)

### Features Added

- [[2364]](https://github.com/Azure/azure-dev/pull/2364) Display docker output during `package` and `deploy`.
- [[2463]](https://github.com/Azure/azure-dev/pull/2463) Support `--docs` flag for all azd commands to show official documentation website.

### Bugs Fixed

- [[2390]](https://github.com/Azure/azure-dev/pull/2367) Fixes unmarshalling of k8s ingress resources with TLS hosts
- [[2402]](https://github.com/Azure/azure-dev/pull/2279) Support for workload profiles in Azure Container Apps
- [[2428, 2040]](https://github.com/Azure/azure-dev/pull/2468) Include current git branch in GitHub federated credentials

### Other Changes

- [[1118]](https://github.com/Azure/azure-dev/pull/1118) Add `azd` as a devcontainer feature. Thanks [aaronpowell](https://github.com/aaronpowell) for their contributions to this feature and for updating our templates to use this new feature!

## 1.0.2 (2023-06-14)

### Features Added

- [[2266]](https://github.com/Azure/azure-dev/pull/2266) Support for buildArgs on Docker builds.
- [[2322]](https://github.com/Azure/azure-dev/pull/2322) Support Azure Spring Apps consumption dedicated plan.

### Bugs Fixed

- [[2348]](https://github.com/Azure/azure-dev/pull/2279) Support purging Managed HSMs.
- [[2362]](https://github.com/Azure/azure-dev/pull/2362) Prevent more errors from interrupting console progress.
- [[2366]](https://github.com/Azure/azure-dev/pull/2366) Fixes issue where hooks inline script slashes are replaced.
- [[2375]](https://github.com/Azure/azure-dev/pull/2375) Store numeric values with leading zeros in .env correctly.
- [[2401]](https://github.com/Azure/azure-dev/pull/2401) Fix the application url fetched from ASA consumption plan.
- [[2426]](https://github.com/Azure/azure-dev/pull/2426) Fix saving of subscription and location defaults.

### Other Changes

- [[2337]](https://github.com/Azure/azure-dev/pull/2337) Update device-code auth flow.

## 1.0.1 (2023-05-25)

### Bugs Fixed

- [[2300]](https://github.com/Azure/azure-dev/pull/2300) Fix `azd auth login` failing with error "reauthentication required: run `azd auth login` to log in" due to stale cache data.

## 1.0.0 (2023-05-22)

### Bugs Fixed

- [[2279]](https://github.com/Azure/azure-dev/pull/2279) Fetch k8s GPG key from alternate location.
- [[2278]](https://github.com/Azure/azure-dev/pull/2278) Remove infrastructure outputs from .env on azd down.
- [[2274]](https://github.com/Azure/azure-dev/pull/2274) Change AKS service spec 'targetPort' from int to string.

## 0.9.0-beta.3 (2023-05-19)

### Features Added
 
- [[2245]](https://github.com/Azure/azure-dev/pull/2245) Add support to login to Azure Container Registry with current identity.
- [[2228]](https://github.com/Azure/azure-dev/pull/2228) Add error classification and reporting for external errors to `azd`.
- [[2219]](https://github.com/Azure/azure-dev/pull/2219) Support environment name as explicit argument for `azd env refresh`.
- [[2164]](https://github.com/Azure/azure-dev/pull/2164) Add timing information on `up`,`package`,`build`, `provision`,`deploy`, `down` and `restore` commands.

#### Template Feature

- [[2157]](https://github.com/Azure/azure-dev/pull/2157) Add `Dapr` and container configuration properties to Azure Container Apps modules.

### Bugs Fixed

- [[2257]](https://github.com/Azure/azure-dev/pull/2257) Add purge option of cognitive accounts for `azd down`.
- [[2243]](https://github.com/Azure/azure-dev/pull/2243) Return error when login fails.
- [[2251]](https://github.com/Azure/azure-dev/pull/2251) Create an `alpha` version of azure.yaml schema with `terraform`.
- [[2028]](https://github.com/Azure/azure-dev/pull/2028) Add check on required role assignments for `azd pipeline config`.

### Other Changes

- [[2218]](https://github.com/Azure/azure-dev/pull/2218) Update `azd pipeline config` default roles to include `User Access Administrator`.
- [[2185]](https://github.com/Azure/azure-dev/pull/2185) Improve error messages on `auth` command.

## 0.9.0-beta.2 (2023-05-11)

### Bugs Fixed

- [[2177]](https://github.com/Azure/azure-dev/issues/2177) Use information in `.installed-by.txt` to advise the user on how to upgrade azd.
- [[2183]](https://github.com/Azure/azure-dev/pull/2182) Statically link CRT in MSI custom action.

## 0.9.0-beta.1 (2023-05-11)

### Features Added

- [[1808]](https://github.com/Azure/azure-dev/pull/1808) Support for Azure Spring Apps(alpha feature).
- [[2083]](https://github.com/Azure/azure-dev/pull/2083) Allow resource group scope deployments(alpha feature).

### Breaking Changes

- [[2066]](https://github.com/Azure/azure-dev/pull/2066) `azd` no longer assumes `dotnet` by default when `services.language` is not set, or empty in `azure.yaml`. If you receive an error message 'language property must not be empty', specify `language: dotnet` explicitly in `azure.yaml`.
- [[2100]](https://github.com/Azure/azure-dev/pull/2100) As a follow up from the change for [azd up ordering](#azd-up-ordering), automatic `.env` file injection when building `staticwebapp` services have been removed. For more details, read more about [Static Web App Dynamic Configuration](#static-web-app-dynamic-configuration) below.
- [[2126]](https://github.com/Azure/azure-dev/pull/2126) During `azd pipeline config` commands `azd` will no longer store non-secret configuration values in [GitHub secrets](https://docs.github.com/actions/automating-your-workflow-with-github-actions/creating-and-using-encrypted-secrets) and instead will be stored in [GitHub variables](https://docs.github.com/actions/learn-github-actions/variables). Non-secret variables should be referenced using the `vars` context instead of the `secrets` context within your GitHub actions.
- [[1989]](https://github.com/Azure/azure-dev/pull/1989) Refactor Container App service target. Deploy will fail if you are using Azure Container Apps that are not deploying the Azure Container Apps resources as part of the initial `provision` step.

### Bugs Fixed

- [[2071]](https://github.com/Azure/azure-dev/pull/2071) Fix `azd config reset` causing a logout to occur.
- [[2048]](https://github.com/Azure/azure-dev/pull/2048) Fix `azd down` deletion on an empty resource group environment.
- [[2088]](https://github.com/Azure/azure-dev/pull/2088) Fix error when running `azd pipeline config --provider azdo` on Codespaces.
- [[2094]](https://github.com/Azure/azure-dev/pull/2094) Add error check for pipeline yml file and ssh interaction when running `azd pipeline config`.

#### Template Fix
- [[2013]](https://github.com/Azure/azure-dev/pull/2013) Fix `load template missing` error in `azd env list`.
- [[2001]](https://github.com/Azure/azure-dev/pull/2001) Fix Azure Container Apps CORS strategy for Java, NodeJs and Python.

### Other Changes

- [[2026]](https://github.com/Azure/azure-dev/pull/2026) Improve provisioning performance for `dotnet` services by batching `dotnet user-secret` updates.
- [[2004]](https://github.com/Azure/azure-dev/pull/2004) Improve error message when no subscriptions are found.
- [[1792]](https://github.com/Azure/azure-dev/pull/1792) Add `java postgresql terraform` template.
- [[2055]](https://github.com/Azure/azure-dev/pull/2055) Add new starter templates for bicep and terraform.
- [[2090]](https://github.com/Azure/azure-dev/pull/2090) Update todo templates names and descriptions.

#### Static Web App Dynamic Configuration

This change affects `staticwebapp` services that are currently relying on azd provided `.env` file variables during `azd deploy`. If you have an application initialized from an older `azd` provided Static Web App template (before April 10, 2023), we recommend adopting the latest changes if you're relying on `.env` variables being present. A way to check whether this affects you is by looking at contents in `azure.yaml`:

Old, uptake needed:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json

name: <your project>
metadata:
  template: todo-nodejs-mongo-swa-func@0.0.1-beta
services:
  web:
    project: ./src/web
    dist: build
    language: js
    host: staticwebapp
  api:
    project: ./src/api
    language: js
    host: function
```

New, no changes necessary:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json

name: <your project>
metadata:
  template: todo-python-mongo-swa-func@0.0.1-beta
services:
  web:
    project: ./src/web
    dist: build
    language: js
    host: staticwebapp
    hooks:
      predeploy:
        posix:
          shell: sh
          run: node entrypoint.js -o ./build/env-config.js
          continueOnError: false
          interactive: false
        windows:
          shell: pwsh
          run: node entrypoint.js -o ./build/env-config.js
          continueOnError: false
          interactive: false
  api:
    project: ./src/api
    language: py
    host: function
```

From the example above, dynamic configuration can still be generated from azd `.env` files by creating a `predeploy` hook that embeds the configuration into web assets. See an example change [here](https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func/commit/50f9268881717a796167c371cb60525f83be8a59#diff-fa5d677aeff171483fa03a69284506672cb9afafa0a7139e03a336e4fb7b773f).

## 0.8.0-beta.2 (2023-04-20)

### Features Added

- [[#1931]](https://github.com/Azure/azure-dev/pull/1931) Support *.war and *.ear java archive files, and specify a specific archive file if multiple archives are present.
- [[#1704]](https://github.com/Azure/azure-dev/pull/1704) Add `requiredVersions` to `azure.yaml`.
- [[#1924]](https://github.com/Azure/azure-dev/pull/1924) Improve UX on `azd down`.
- [[#1807]](https://github.com/Azure/azure-dev/pull/1807) Retrieves credentials using the token endpoint on `CloudShell`.

### Bugs Fixed

- [[#1923]](https://github.com/Azure/azure-dev/pull/1923) Fix `Python CLI not installed` error when Python is installed.
- [[#1963]](https://github.com/Azure/azure-dev/pull/1963) Update GitHub federated auth token provider to allow for fetching of tokens when tokens expire.
- [[#1967]](https://github.com/Azure/azure-dev/pull/1967) Display provisioning resources in `Failed` state.
- [[#1940]](https://github.com/Azure/azure-dev/pull/1940) Detect and update environment changes before and after hook executions.
- [[#1970]](https://github.com/Azure/azure-dev/pull/1970) Fix `pipeline config` issues on Codespaces for `GitHub cli` and `git cli` auth.
- [[#1982]](https://github.com/Azure/azure-dev/pull/1982) Ensure directory has user "execute" permissions.

## 0.8.0-beta.1 (2023-04-10)

### Features Added

- [[#1715]](https://github.com/Azure/azure-dev/pull/1715) Adding feature alpha toggle:
  - Moving terraform provider as alpha feature. Use `azd config set alpha.terraform on` to have it enabled.
- [[#1833]](https://github.com/Azure/azure-dev/pull/1833) Deploy from existing package using `--from-package` flag.

### Breaking Changes

- [[#1715]](https://github.com/Azure/azure-dev/pull/1715) Using `terraform` as provisioning provider will fail and require user to enable terraform running `azd config set alpha.terraform on`.
- [[#1801]](https://github.com/Azure/azure-dev/pull/1801) Restructuring specific command flags.
  - `azd up` no longer runs `azd init`. As a result, the following flags have been removed from `azd up`:
    - `--template` / `-t`
    - `--location` / `-l`
    - `--branch` / `-b`
    - `--subscription`
  - Use of `--service` and `--no-progress` in `azd up` is being deprecated.
  - `azd deploy` now accepts a positional argument. Use `azd deploy <web>` instead of `azd deploy --service <web>`
  - Deprecate `--no-progress` flag as it currently does nothing. A warning message is shown when used.
  - Hide `--output` flag in the usage printout to correctly reflect the current it's current alpha-preview status. The output contract for structured schema such as JSON has yet been finalized.
- [[#1804]](https://github.com/Azure/azure-dev/pull/1804) Adjust command aliases.
  - `azd login` and `azd logout` are now available as `azd auth login` and `azd auth logout` respectively. `azd login` and `azd logout` are still available for use, but will be removed in a future release.
  - `azd infra create` and `azd infra delete`, which have always been aliases for `azd provision` and `azd down`, are now deprecated. The commands are still available for use, but will be removed in a future release.
- [[#1824]](https://github.com/Azure/azure-dev/pull/1824) Add working directory sensitivity for `restore` and `deploy`.
  - `azd deploy` will now deploy the current service, when the current working directory is set to a service directory.
  - `azd deploy` will deploy all services, when the current working directory is set to the project directory containing `azure.yaml`
  - In other directories, `azd deploy` will not attempt a deployment and instead error out with suggestions. `azd deploy --all` can be used to deploy all services, or `azd deploy <service>` to deploy a given service always.
- [[#1752]](https://github.com/Azure/azure-dev/pull/1752) Ask fewer questions during `init`.
  - `azd init` will now only prompt for the environment name. Azure subscription and location values are prompted only when infrastructure provisioning is needed, when running `azd provision`, and consequently when running `azd up`.

### Bugs Fixed

- [[#1734]](https://github.com/Azure/azure-dev/pull/1734) Fix setting `AZURE_PRINCIPAL_ID` on multi-tenant directory.
- [[#1738]](https://github.com/Azure/azure-dev/pull/1738) Fix generating auth token on multi-tenant directory.
- [[#1762]](https://github.com/Azure/azure-dev/pull/1762) Allow local files to be kept when running `init`.
- [[#1764]](https://github.com/Azure/azure-dev/pull/1764) Enhance zip-deploy during build for:
  - Python: Do not include virtual environments for python.
  - Node: Update node modules detection to exclude it from build.
- [[#1857]](https://github.com/Azure/azure-dev/pull/1857) Adds `package` command hooks to azd schema.
- [[#1878]](https://github.com/Azure/azure-dev/pull/1878) Ensure default generated docker repo/tags are all lowercase.
- [[#1875]](https://github.com/Azure/azure-dev/pull/1875) Fixes panic for `postpackage` hook errors.

### Other Changes

#### `azd up` no longer runs `azd init`

The behavior of `azd up -t <template>` can be reproduced with:

```bash
cd <empty dir>
azd init -t <template>
azd up
```

#### `azd deploy` no longer deploys all services when ran in any directory

The new behavior is as follows:

1. `azd deploy` will now deploy the current service, when the current working directory is set to a service directory.
2. `azd deploy` will deploy all services, when the current working directory is set to the project directory containing `azure.yaml`.
3. In other directories, `azd deploy` will not attempt a deployment and error out with suggestions. `azd deploy --all` can be used to deploy all services, or `azd deploy <service>` to deploy a given service always.

#### `azd up` ordering

`azd up` now packages artifacts prior to running `azd provision` and `azd deploy`. This should not affect most users, with the exception of users that may be taking advantage of `azd`'s environment values in packaging `staticwebapp` services. If `azd up` no longer works as expected, and you are currently taking advantage of `azd`'s provided environment values to package your application, a `predeploy` hook may be used to generate configuration files from `azd` environment values. See the working example in our ToDo templates that leverage `staticwebapp`, example [here](https://github.com/Azure-Samples/todo-python-mongo-swa-func/blob/main/azure.yaml). Note that script `hooks` automatically have `azd` environment values loaded in the shell environment.

## 0.7.0-beta.1 (2023-03-09)

### Features Added

- [[#1515]](https://github.com/Azure/azure-dev/pull/1515) Remove gh-cli as external dependency for `azd pipeline config`.
- [[#1558]](https://github.com/Azure/azure-dev/pull/1558) Upgrade bicep version to 0.14.46 and fetch ARM specific version on ARM platforms.
- [[#1611]](https://github.com/Azure/azure-dev/pull/1611) Updated formatting for displaying command's help.
- [[#1629]](https://github.com/Azure/azure-dev/pull/1629) Add support for Azure Kubernetes Service (AKS) target.

### Bugs Fixed

- [[#1631]](https://github.com/Azure/azure-dev/pull/1631) Fail fast during `azd init` when `git` is not installed.
- [[#1559]](https://github.com/Azure/azure-dev/pull/1559) No feedback output during provisioning some templates.
- [[#1683]](https://github.com/Azure/azure-dev/pull/1683) Fix `azd pipeline config` to honor provider from `azure.yaml`.
- [[#1578]](https://github.com/Azure/azure-dev/pull/1578) Fix crash while running `azd login`, due to a tenant `DisplayName` being nil.

Thanks to community members: @pamelafox, @tonybaloney, @cobey for their contributions in this release.

## 0.6.0-beta.2 (2023-02-10)

### Bugs Fixed

- [[#1527]](https://github.com/Azure/azure-dev/pull/1527) Fix running specific commands with `--output json`  causing stack overflow errors to occur.
- [[#1534]](https://github.com/Azure/azure-dev/pull/1534) Fix running commands with `-e <environment name>` flag or with `AZURE_ENV_NAME` set not being respected. When running in CI environments, this caused prompting to occur, and failing if `--no-prompt` is specified.

## 0.6.0-beta.1 (2023-02-08)

### Features Added

- [[#1236]](https://github.com/Azure/azure-dev/pull/1236) Support for command and service hooks
- [[#1414]](https://github.com/Azure/azure-dev/pull/1414) Support for installation via Homebrew. Windows Package Manager, and Chocolatey are also now supported.
- [[#1407]](https://github.com/Azure/azure-dev/pull/1407) Improve UX styling for `azd pipeline config`.
- [[#1478]](https://github.com/Azure/azure-dev/pull/1478) Support for multiple Azure tenants.

- [[#1345]](https://github.com/Azure/azure-dev/pull/1345) Core bicep module `appservice.bicep` now supports `ftpsState` as a parameter to configure FTPS upload behavior.
- [[#1497]](https://github.com/Azure/azure-dev/pull/1497) Core bicep module `appservice.bicep` now supports `healthCheckPath` as a parameter to configure the health-check endpoint.
- [[#1403]](https://github.com/Azure/azure-dev/pull/1403) Core bicep module `apim-api.bicep` now links Web App or Function App instances. This allows users on the Azure Portal to navigate to the API management resource directly from the Web App or Function App.

### Bugs Fixed

- [[#1406]](https://github.com/Azure/azure-dev/pull/1424) On Windows, fix MSI installation not updating `azd` in some cases (reported by @lechnerc77, fixed by @heaths)
- [[#1418]](https://github.com/Azure/azure-dev/pull/1418) Display `provision` progress for PostgreSQL server resources.
- [[#1483]](https://github.com/Azure/azure-dev/pull/1483) For Python projects, skip packaging of virtual environment (`.venv` folders)
- [[#1495]](https://github.com/Azure/azure-dev/pull/1495) `init` now restores file executable permissions and initializes a `git` repository automatically.
- [[#1470]](https://github.com/Azure/azure-dev/pull/1470) Improve performance of `azd --help` on Windows for domain-joined users.
- [[#1503]](https://github.com/Azure/azure-dev/pull/1503) Fix display for Function App types in `provision` progress

Thanks to community members: @pamelafox, @lechnerc77 for their contributions in this release.

## 0.5.0-beta.3 (2023-01-13)

### Bugs Fixed

- [[#1394]](https://github.com/Azure/azure-dev/pull/1394) Bug when running azd up with a template.

## 0.5.0-beta.2 (2023-01-12)

### Bugs Fixed

- [[#1366]](https://github.com/Azure/azure-dev/issues/1366) Login not possible with personal account after upgrade to 0.5.0.

## 0.5.0-beta.1 (2023-01-11)

### Features Added

- [[#1311]](https://github.com/Azure/azure-dev/pull/1311) Add support to install script with MSI on Windows.
- [[#1312]](https://github.com/Azure/azure-dev/pull/1312) Allow users to configure service endpoints using `SERVICE_<service>_ENDPOINTS`.
- [[#1323]](https://github.com/Azure/azure-dev/pull/1323) Add API Management Service support for all templates.
- [[#1326]](https://github.com/Azure/azure-dev/pull/1326) Add purge support for API Management Service.
- [[#1076]](https://github.com/Azure/azure-dev/pull/1076) Refactor the Bicep tool in azd to use the standalone API vs az command wrapper.
- [[#1087]](https://github.com/Azure/azure-dev/pull/1087) Add NodeJs and Terraform devcontainer.
- [[#965]](https://github.com/Azure/azure-dev/pull/965) Add UX style for `azd init`.
- [[#1100]](https://github.com/Azure/azure-dev/pull/1100) Add Shell completion.
- [[#1086]](https://github.com/Azure/azure-dev/pull/1086) Add FederatedIdentityCredentials (FICS).
- [[#1177]](https://github.com/Azure/azure-dev/pull/1177) Add command `azd auth token`.
- [[#1210]](https://github.com/Azure/azure-dev/pull/1210) Have azd acquire Bicep.
- [[#1133]](https://github.com/Azure/azure-dev/pull/1133) Add UX style for `azd provision`.
- [[#1248]](https://github.com/Azure/azure-dev/pull/1248) Support `redirect port` for `azd login`.
- [[#1269]](https://github.com/Azure/azure-dev/pull/1269) Add UX style for `azd deploy`.

### Breaking Changes

- [[#1129]](https://github.com/Azure/azure-dev/pull/1129) Remove all dependencies on az cli. 
- [[#1105]](https://github.com/Azure/azure-dev/pull/1105) `azd env new` now accepts the name of the environment as the first argument, i.e. `azd env new <environment>`. Previously, this behavior was accomplished via the global environment flag `-e`, i.e. `azd env new -e <environment>`.
- [[#1022]](https://github.com/Azure/azure-dev/pull/1022) `azd` no longer uses the `az` CLI to authenticate with Azure by default. You will need to run `azd login` after upgrading. You may run `azd config set auth.useAzCliAuth true` to restore the old behavior of using `az` for authentication.

### Bugs Fixed

- [[#1107]](https://github.com/Azure/azure-dev/pull/1107) Fix Bicep path not found.
- [[#1096]](https://github.com/Azure/azure-dev/pull/1096) Fix Java version check for major-only release.
- [[#1105]](https://github.com/Azure/azure-dev/pull/1105) Fix `env new` to use positional argument.
- [[#1168]](https://github.com/Azure/azure-dev/pull/1168) Fix purge option for command `azd down --force --purge` to purge key vaults and app configurations resources.

If you have existing pipelines that use `azd`, you will need to update your pipelines to use the new `azd` login methods when authenticating against Azure.

**GitHub Actions pipelines**:

Update your `azure-dev.yml` to stop using the `azure/login@v1` action, and instead log in using `azd` directly. To do so, replace:

```yaml
- name: Log in with Azure
  uses: azure/login@v1
  with:
    creds: ${{ secrets.AZURE_CREDENTIALS }}
```

with

```yaml
- name: Log in with Azure
  run: |
    $info = $Env:AZURE_CREDENTIALS | ConvertFrom-Json -AsHashtable;
    Write-Host "::add-mask::$($info.clientSecret)"

    azd login `
      --client-id "$($info.clientId)" `
      --client-secret "$($info.clientSecret)" `
      --tenant-id "$($info.tenantId)"
  shell: pwsh
  env:
    AZURE_CREDENTIALS: ${{ secrets.AZURE_CREDENTIALS }}
```

**Azure DevOps pipelines**:

Update your `azure-dev.yml` file to force `azd` to use `az` for authentication.  To do so, add a new step before any other steps which use `azd`:

```yaml
- pwsh: |
    azd config set auth.useAzCliAuth "true"
  displayName: Configure azd to Use az CLI Authentication.
```

We plan to improve this behavior with [[#1126]](https://github.com/Azure/azure-dev/issues/1126).

## 0.4.0-beta.1 (2022-11-02)

### Features Added

- [[#773]](https://github.com/Azure/azure-dev/pull/773) Add support for Java with Maven.
- [[#1026]](https://github.com/Azure/azure-dev/pull/1026), [[#1021]](https://github.com/Azure/azure-dev/pull/1021) New official templates: ToDo with Java on App Service, ToDo with Java on Azure Container Apps, ToDo with C# on Azure Functions
- [[#967]](https://github.com/Azure/azure-dev/pull/967) New `azd config` command for managing default subscription and location selections.
- [[#1035]](https://github.com/Azure/azure-dev/pull/1035) Add terraform support for Azure Pipelines created using `azd pipeline config`.

### Bugs Fixed

- [[#1060]](https://github.com/Azure/azure-dev/pull/1060) Fix color rendering on Windows.
- [[#1011]](https://github.com/Azure/azure-dev/pull/1011) Improve error printout for deployment failures.
- [[#991]](https://github.com/Azure/azure-dev/pull/991) Fix `devcontainers.json` to use non-deprecated syntax.
- [[#996]](https://github.com/Azure/azure-dev/pull/996) ToDo templates:
  - Fix cases where provisioning of app settings would succeed, but app settings configuration would not take place.
  - Move resource naming to `main.bicep` and remove `resources.bicep` from templates.

## 0.3.0-beta.5 (2022-10-26)

### Bugs Fixed

- [[#979]](https://github.com/Azure/azure-dev/pull/979) Fix provisioning template with non string outputs.

## 0.3.0-beta.4 (2022-10-25 **DEPRECATED**)

### Bugs Fixed

- [[#979]](https://github.com/Azure/azure-dev/pull/979) Fix provisioning template with non string outputs.

## 0.3.0-beta.3 (2022-10-21)

### Features Added

- [[#878]](https://github.com/Azure/azure-dev/pull/878) `azd down` supports purge of app configuration stores.

### Bugs Fixed

- [[#925]](https://github.com/Azure/azure-dev/pull/925) Fix issues where running `azd infra create` with `--output==json` would emit invalid JSON.  As part of this change, we now no longer emit multiple objects to `stdout` as part of an operation. Instead, progress messages are streamed in a structured way to `stderr`.

### Other Changes

- [[#691]](https://github.com/Azure/azure-dev/pull/691) Rearrange Terraform templates by extracting common resources and using these common modules.
- [[#892]](https://github.com/Azure/azure-dev/pull/892) Simplify template bicep modules.

## 0.3.0-beta.2 (2022-10-05)

### Bugs Fixed

- [[#795]](https://github.com/Azure/azure-dev/pull/795) Fix cases where clicking the Azure deployment progress link provided in `azd provision` might result in a 404 NotFound error page due to timing.
- [[#755]](https://github.com/Azure/azure-dev/pull/755) Fix cases where `azd pipeline config` might fail in pushing the repository due to cached credentials.

## 0.3.0-beta.1 (2022-09-30)

### Features Added

- [[#743]](https://github.com/Azure/azure-dev/pull/743) Azure DevOps support for pipeline config command.

### Bugs Fixed

- [[#730]](https://github.com/Azure/azure-dev/pull/730) Fix hierarchical configuration keys for dotnet to show up correctly when stored as dotnet user-secrets. Thanks community member [@sebastianmattar](https://github.com/sebastianmattar) for providing the initial fix!
- [[#761]](https://github.com/Azure/azure-dev/pull/761) Fix error in `azd deploy` when multiple resource groups are defined in bicep

## 0.2.0-beta.2 (2022-09-21)

### Bugs Fixed

- [[#724]](https://github.com/Azure/azure-dev/pull/724) Fix version check for supporting Docker CE / Moby schemes. 

### Other Changes

- [[#548]](https://github.com/Azure/azure-dev/pull/548) Refactor template bicep into modules.

## 0.2.0-beta.1 (2022-09-14)

### Features Added

- [[#172]](https://github.com/Azure/azure-dev/pull/172) Implement Infrastructure Provision Provider Model.
- [[#573]](https://github.com/Azure/azure-dev/pull/573) Add support for Terraform for infrastructure as code (IaC).
- [[#532]](https://github.com/Azure/azure-dev/pull/532) Add Terraform support for Python template.
- [[#646]](https://github.com/Azure/azure-dev/pull/646) Add Terraform support for Node.js template.
- [[#550]](https://github.com/Azure/azure-dev/pull/550) Add C# + Azure SQL template.

### Breaking Changes

- [[#588]](https://github.com/Azure/azure-dev/pull/588) Update default view from `azd monitor` to overview dashboard.

## 0.1.0-beta.5 (2022-08-25)

### Bugs Fixed

- [[#461]](https://github.com/Azure/azure-dev/pull/461) Fix for using a command output other than JSON.
- [[#480]](https://github.com/Azure/azure-dev/pull/480) Fix deploy error when using an environment name with capital letters.

## 0.1.0-beta.4 (2022-08-10)

### Features Added

- [[#140]](https://github.com/Azure/azure-dev/pull/140) Add consistent resource abbreviations.

### Bugs Fixed

- [[#245]](https://github.com/Azure/azure-dev/issues/245) Fix Windows installer script modifying `PATH` environment variable to `REG_SZ` (reported by [@alexandair](https://github.com/alexandair))

## 0.1.0-beta.3 (2022-07-28)

### Features Added

- [[#100]](https://github.com/Azure/azure-dev/pull/100) Add support for an optional `docker` section in service configuration to control advanced docker options.
- [[#152]](https://github.com/Azure/azure-dev/pull/152) While provisioning in interactive mode (default), Azure resources are now logged to console as they are created.

### Breaking Changes

- [[#117]](https://github.com/Azure/azure-dev/issues/117) When specifying a custom module within a service the configuration key has been changed from `moduleName` to `module` and accepts a relative path to the infra module.

### Bugs Fixed

- [[#77]](https://github.com/Azure/azure-dev/issues/77) Use the correct command to log into the GitHub CLI in error messages. Thanks to community member [@TheEskhaton](https://github.com/TheEskhaton) for the fix!
- [[#115]](https://github.com/Azure/azure-dev/issues/115) Fix deploy error when using a resource name with capital letters.

### Other Changes

- [[#188]](https://github.com/Azure/azure-dev/issues/188) Update the minimum Bicep version to `v0.8.9`.

## 0.1.0-beta.2 (2022-07-13)

### Bugs Fixed

- Fixed an issue where passing `--help` to `azd` would result in an error message being printed to standard error before the help was printed.
- [[#71]](https://github.com/Azure/azure-dev/issues/71) Fixed detection for disabled GitHub actions on new created repos.
- [[#70]](https://github.com/Azure/azure-dev/issues/70) Ensure SWA app is in READY state after deployment completes
- [[#53]](https://github.com/Azure/azure-dev/issues/53) SWA app is deployed to incorrect environment

## 0.1.0-beta.1 (2022-07-11)

Initial public release of the Azure Developer CLI.
