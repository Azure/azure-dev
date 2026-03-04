# Release History

## 0.10.0 (2026-03-04)

- [[#6826]](https://github.com/Azure/azure-dev/pull/6826) Handle locked files on Windows during `azd x build` by terminating stale extension processes.
- [[#6747]](https://github.com/Azure/azure-dev/pull/6747) Add `requiredAzdVersion` support to `azd x publish` for declaring core version dependencies.
- [[#6792]](https://github.com/Azure/azure-dev/pull/6792) Fix `azd x publish` to correctly sync `--version` flag to extension metadata.
- [[#6733]](https://github.com/Azure/azure-dev/pull/6733) Update scaffolded `environment.proto` template to make `env_name` optional in EnvironmentService methods.
- [[#6768]](https://github.com/Azure/azure-dev/pull/6768) Normalize user-facing text to use lowercase `azd` branding consistently.
- [[#6711]](https://github.com/Azure/azure-dev/pull/6711) Improve command descriptions for `publish` and `release` commands.
- [[#6677]](https://github.com/Azure/azure-dev/pull/6677) Bump protobuf dependency in Python scaffolding template from 5.29.5 to 6.33.5.

## 0.9.0 (2026-01-22)

- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Add metadata capability
- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Support `AZD_EXT_DEBUG=true` for debugging
- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Add missing capabilities to `init` command and update Go scaffolding
- [[#6578]](https://github.com/Azure/azure-dev/pull/6578) Fix prompts during `init` flow

## 0.8.0 (2026-01-08)

- [[#6474]](https://github.com/Azure/azure-dev/pull/6474) Remove Default Azure Credential from JS and Python scaffolding

## 0.7.1 (2025-12-17)

- Fixes bug during `release` when setting `--prerelease` flag
- Fixes bug during `build` - execute permissions not set on binary for POSIX systems

## 0.7.0 (2025-12-03)

- Add language-specific .gitignore templates for `init` command

## 0.6.0 (2025-10-14)

- Improve extension metadata validation
- Update `publish` command to include `providers` field in extension schema and registry
- Add MCP and service target provider capabilities to `init` command

## 0.5.1 (2025-09-22)

- Updates `azd x publish` to automatically set up local registry if not configured

## 0.5.0

- Adds .tar.gz support for Linux extensions
- Adds --artifacts support for multiple glob patterns

## 0.4.2

- Fixes Python local build support
- Updates JavaScript support to use generated proto clients

## 0.4.1

- Fixes line endings for unix based systems

## 0.4.0

- Support for Javascript extensions

## 0.3.0

- Support for .NET & Python extensions

## 0.2.0

- Support for Go extensions
