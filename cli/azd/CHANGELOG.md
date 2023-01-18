# Release History

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

## 0.3.0-beta.4 (2022-10-25) **DEPRECATED**

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
