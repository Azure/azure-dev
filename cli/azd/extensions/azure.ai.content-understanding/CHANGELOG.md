# Release History

## 0.1.0-preview (Unreleased)

### Features Added

- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Add `azd ai cu` commands to analyze files and URLs with azd sign-in, including batch analysis, default YAML front matter and Markdown output, and complete JSON results with `--llm-input=false`.
- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Add analyzer listing, inspection, creation, copying, deletion, schema authoring and validation, and testing against sample files.
- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Add service-default configuration, environment-variable inspection, and setup diagnostics.
- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Add `config set|get|list|unset` shortcuts for CU defaults in azd user configuration without a project, shared settings resolution, and effective-setting sources in `doctor`.
- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Add infrastructure generation with Bash and PowerShell hooks for model setup and Content Understanding configuration.
- [[#10668]](https://github.com/Azure/azure-dev/pull/10668) Default to API version `2025-11-01` and accept valid `YYYY-MM-DD-preview` versions for internal testing and future previews.
