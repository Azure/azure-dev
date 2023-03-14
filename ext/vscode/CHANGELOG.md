# Release History

## 0.4.1 (2023-03-14)
### Bugs Fixed
- [[#1724]](https://github.com/Azure/azure-dev/pull/1724) Make the notification to install the CLI less aggressive.

## 0.4.0 (2023-03-08)
### Added
- [[#853]](https://github.com/Azure/azure-dev/pull/853) Integration with the Azure Resources extension's workspace view. Requires version 0.6.1 of the [Azure Resources](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-azureresourcegroups) extension.
- [[#1644]](https://github.com/Azure/azure-dev/pull/1644) Added a walkthrough experience for using the extension.

## 0.3.0 (2022-09-14)
### Added
- [[#493]](https://github.com/Azure/azure-dev/pull/493) Show README file after successful init/up.

### Fixed
- [[#498]](https://github.com/Azure/azure-dev/pull/498) Use `azd template list` to populate template list in VS Code (now always consistent with the CLI).
- [[#556]](https://github.com/Azure/azure-dev/pull/556) Improve error message when no environments are found.

## 0.2.0 (2022-08-02)

### Changed
- [[#189]](https://github.com/Azure/azure-dev/pull/189) Bump bicep minimum version to v0.8.9

### Added
- [[#151]](https://github.com/Azure/azure-dev/pull/151) Detect and warn the user if `azd` CLI is not installed.

### Fixed
- [[#159]](https://github.com/Azure/azure-dev/pull/159) Enable user feedback via surveys.
- [[#170]](https://github.com/Azure/azure-dev/pull/170) Enable gradual rollout of new features.

## 0.1.0 (2022-07-11)

- Initial release.
