# Release History

## 0.7.2 (2026-07-16)

- Simple test release of extension, with no changes.

## 0.7.1 (2026-07-14)

- Simple test release of extension, with no changes.

## 0.7.0 (2026-07-13)

### Features Added

- [[#7172]](https://github.com/Azure/azure-dev/pull/7172) Add a `copilot` command demonstrating the `CopilotService` gRPC service for extension-to-agent interactions.
- [[#7482]](https://github.com/Azure/azure-dev/pull/7482) Add a demo provisioning provider showcasing the extension framework's provisioning-provider capability.
- [[#8656]](https://github.com/Azure/azure-dev/pull/8656) Add the validation-provider capability with a sample preflight validation check.
- [[#9019]](https://github.com/Azure/azure-dev/pull/9019) Add a provider-agnostic provision validation check example.
- [[#7982]](https://github.com/Azure/azure-dev/pull/7982) Add a `Secret` option to prompts for masked input.
- [[#7536]](https://github.com/Azure/azure-dev/pull/7536) Filter out deprecated AI models and surface version-level lifecycle status in the `ai` commands.

### Bug Fixes

- [[#7080]](https://github.com/Azure/azure-dev/pull/7080) Use `UserTenantId` for credential resolution.

## 0.6.0 (2026-03-04)

### Features Added

- [[#6741]](https://github.com/Azure/azure-dev/pull/6741) Add `ai models`, `ai deployment`, and `ai quota` commands for interactive AI model browsing, deployment configuration, and quota viewing.
- [[#6835]](https://github.com/Azure/azure-dev/pull/6835) Add `gh-url-parse` command and adopt `azdext.Run` lifecycle with structured error transport for improved error handling and telemetry.

## 0.5.0 (2026-01-23)

### Features Added

- Added metadata capability
- Added `AZD_EXT_DEBUG=true` support for debugging

## 0.4.0 (2025-12-03)

### Features Added

- Added custom language framework implementation
- Updated to use service context and artifacts
- Updated custom service target to use container service

## 0.3.0 (2025-10-01)

### Features Added

- Enabled MCP (Model Context Protocol) server capability (#5807)
- Added MCP server tools and commands to the demo extension

### Bug Fixes

- Fixed extension `build.ps1` scripts to fail on any error (#5739)

## 0.2.0 (Previous Release)

### New Features

- Enhanced extension with lifecycle events support
- Added multi-select prompt support
- Added colors command to display standard colors

## 0.1.0-beta.1 (Initial Release)

### Initial Features

- Testing release of azd extensions. This release may disappear.
