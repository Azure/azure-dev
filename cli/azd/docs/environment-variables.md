# Environment Variables

## Environment variables used with azd

Environment variables that can be used to configure `azd` behavior, usually set within a shell or terminal. For environment variables that accept a boolean, the values `1, t, T, TRUE, true, True` are accepted as "true"; the values: `0, f, F, FALSE, false, False` are all accepted as "false".

- `AZD_ALPHA_ENABLE_<name>`: Enables or disables an alpha feature. `<name>` is the upper-cased name of the feature, with dot `.` characters replaced by underscore `_` characters.
- `AZD_AUTH_ENDPOINT`: The [External Authentication](./external-authentication.md) endpoint.
- `AZD_AUTH_KEY`: The [External Authentication](./external-authentication.md) shared key.
- `AZD_BUILDER_IMAGE`: The builder docker image used to perform Dockerfile-less builds.
- `AZD_CONFIG_DIR`: The file path of the user-level configuration directory.
- `AZD_DEMO_MODE`: If true, enables demo mode. This hides personal output, such as subscription IDs, from being displayed in output.
- `AZD_FORCE_TTY`: If true, forces `azd` to write terminal-style output.
- `AZD_IN_CLOUDSHELL`: If true, `azd` runs with Azure Cloud Shell specific behavior.
- `AZD_SKIP_UPDATE_CHECK`: If true, skips the out-of-date update check output that is typically printed at the end of the command.

For tools that are auto-acquired by `azd`, you are able to configure the following environment variables to use a different version of the tool installed on the machine:

- `AZD_BICEP_TOOL_PATH`: The Bicep tool override path. The direct path to `bicep` or `bicep.exe`.
- `AZD_GH_TOOL_PATH`: The `gh` tool override path. The direct path to `gh` or `gh.exe`.
- `AZD_PACK_TOOL_PATH`: The `pack` tool override path. The direct path to `pack` or `pack.exe`.

### GitHub Pipeline Scoping (optional)

When run with the optional flag `--github-use-environments`, `azd pipeline config` (GitHub provider) scopes secrets & variables to a GitHub Environment whose name matches the current azd environment (`AZURE_ENV_NAME`). The environment is created if it does not already exist, and an OIDC federated credential subject (`repo:<owner>/<repo>:environment:<AZURE_ENV_NAME>`) is added so workflows targeting that environment can obtain Azure tokens. If multiple azd environments are detected (multiple `.azure/<env>/.env` files) a strategy matrix is emitted and each job targets the corresponding GitHub Environment. Without the flag, the generated workflow remains in the legacy form (no `environment:` key or matrix) and variables & secrets are written at the repository level.

The previously documented `AZD_GITHUB_ENV` override has been removed and is ignored if set.

Migration & cleanup: Re-running `azd pipeline config` with the flag toggled on or off will update an existing workflow to add or remove the `environment` and matrix sections to match current usage – no manual edits required. When the flag is enabled, azd also:

- Migrates known Azure pipeline variables (`AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, plus resource-group / terraform remote state vars when present) into the GitHub Environment scope (creating/updating them there) and deletes any duplicate repository-level copies.
- Emits only the single environment-scoped OIDC federated credential subject; legacy branch (`repo:<slug>:ref:refs/heads/<branch>`) and pull request (`repo:<slug>:pull_request`) subjects are automatically pruned for service principal based auth (MSI pruning will be added in a future update).

Disabling the flag returns to repository-level scoping (existing environment variables/secrets remain but are unused by the legacy workflow).
