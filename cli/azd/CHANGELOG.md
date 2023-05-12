# Release History

## 0.9.0-beta.3 (Unreleased)

### Features Added

### Breaking Changes

### Bugs Fixed

### Other Changes

## 0.9.0-beta.2 (2023-05-11)

### Bugs Fixed

- [[2177]](https://github.com/Azure/azure-dev/issues/2177) Use information in `.installed-by.txt` to advise the user on how to upgrade azd
- [[2183]](https://github.com/Azure/azure-dev/pull/2182) Statically link CRT in MSI custom action

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

This change affects `staticwebapp` services that are currently relying on azd provided `.env` file variables during `azd deploy`. If you have an application initialized from an older `azd` provided Static Web App template (before April 10, 2023), we recommend uptaking the latest changes if you're relying on `.env` variables being present. A way to check whether this affects you is by looking at contents in `azure.yaml`:

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
- [[#1970]](https://github.com/Azure/azure-dev/pull/1970) Fix `pipeline config` issues on Codespaces for `ghcli` and `gitcli` auth.
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
