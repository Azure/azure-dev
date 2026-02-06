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
- `AZD_PROMPT_TIMEOUT`: Timeout in seconds for interactive prompts. When set, prompts will timeout after the specified duration. If a default value is available, it will be auto-selected; otherwise, the command fails with a suggestion to use `--no-prompt`. Useful for automated agents (e.g., GitHub Copilot, Claude Code) that cannot respond to interactive prompts. Set to `0` or leave empty to disable (default behavior for human users).
- `AZD_SKIP_UPDATE_CHECK`: If true, skips the out-of-date update check output that is typically printed at the end of the command.

For tools that are auto-acquired by `azd`, you are able to configure the following environment variables to use a different version of the tool installed on the machine:

- `AZD_BICEP_TOOL_PATH`: The Bicep tool override path. The direct path to `bicep` or `bicep.exe`.
- `AZD_GH_TOOL_PATH`: The `gh` tool override path. The direct path to `gh` or `gh.exe`.
- `AZD_PACK_TOOL_PATH`: The `pack` tool override path. The direct path to `pack` or `pack.exe`.
