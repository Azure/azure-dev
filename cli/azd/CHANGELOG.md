# Release History

## 0.2.0-beta.2 (Unreleased)

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
