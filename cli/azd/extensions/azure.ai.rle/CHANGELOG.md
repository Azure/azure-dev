# Release History

## 0.2.1-preview (Unreleased)

- Add `azd ai rle environment list` to list environments in the configured Foundry project.
- Rename `azd ai rle deploy` to `azd ai rle publish` to avoid confusion with the core `azd deploy` command.
- Add `--version-bump` to `azd ai rle publish` so users can choose major, minor, or patch environment versioning.

## 0.2.0-preview

- Use the Foundry project endpoint for project-relative RLE environment and sandbox APIs.
- Authenticate Foundry API requests with Azure credentials from `az login`, `azd auth login`, or another supported development credential.
- Send the required `2025-11-15-preview` Foundry data-plane API version.
- Support versioned environment deployments and sandbox `baseUrl` invocation.
- Wait for asynchronous disk-image conversion before leasing a sandbox and surface conversion failures directly.

## 0.1.0-preview

- Initial preview scaffold for the RLE extension with `init`, `run`, `invoke`, `deploy`, and `version` commands.