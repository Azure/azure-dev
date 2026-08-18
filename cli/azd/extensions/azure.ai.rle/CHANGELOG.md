# Release History

## 0.4.0-preview (Unreleased)

- Align environment discovery and remote invocation with the refreshed RLE service routes and cursor-based response contracts.
- Use `/rl_environments` consistently for environment and instance lifecycle APIs.
- Manage remote invoke through temporary instance groups and instances instead of direct sandbox lifecycle APIs.
- Use saved-state and explicit versions when present; otherwise let unversioned group creation resolve the latest version.
- Delete the temporary instance and group on exit with Ctrl+C-independent cleanup and concise terminal status.
- Persist the environment name as `environmentName` while continuing to read legacy `name` state files.
- Authenticate OpenEnv gateway requests and route the browser playground through an authenticated local proxy.

## 0.3.0-preview

- Add `azd ai rle list` to list environments in the configured Foundry project.
- Add `azd ai rle show <environment-name>` to inspect an environment's full details and version history.
- Allow `azd ai rle invoke <environment-name>` to invoke an existing project environment without local source or state, with optional `--version` selection.
- Rename `azd ai rle deploy` to `azd ai rle publish` to avoid confusion with the core `azd deploy` command.
- Add `--version-bump` to `azd ai rle publish` so users can choose major, minor, or patch environment versioning.
- Use the Foundry project endpoint for project-relative RLE environment and sandbox APIs.
- Authenticate Foundry API requests with Azure credentials from `az login`, `azd auth login`, or another supported development credential.
- Send the required `2025-11-15-preview` Foundry data-plane API version.
- Support versioned environment deployments and sandbox `baseUrl` invocation.
- Wait for asynchronous disk-image conversion before leasing a sandbox and surface conversion failures directly.

## 0.1.0-preview

- Initial preview scaffold for the RLE extension with `init`, `run`, `invoke`, `deploy`, and `version` commands.