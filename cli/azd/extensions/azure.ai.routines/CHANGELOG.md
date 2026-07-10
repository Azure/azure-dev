# Release History

## 1.0.0-beta.3 (Unreleased)

### Bugs Fixed

- [[#9079]](https://github.com/Azure/azure-dev/pull/9079) Resolve routine input `${VAR}` references from the service-level `env` object forwarded by azd core. Existing services without `env` keep falling back to the active azd environment.

## 1.0.0-beta.2 (2026-07-09)

### Bugs Fixed

- [[#8419]](https://github.com/Azure/azure-dev/issues/8419) Increase the default routines write timeout and add `--timeout` / `AZURE_AI_ROUTINES_HTTP_TIMEOUT` overrides so cold recurring routine creates are not cancelled before AgentIdentity binding completes.
- [[#8986]](https://github.com/Azure/azure-dev/pull/8986) Fix `azd ai routine` commands failing to decode routine responses when the service returns numeric (Unix epoch) timestamp values for fields such as schedule `created_at` and timer `triggers.<name>.at`.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) `azd deploy` now expands `${VAR}` references in a routine's `action.input` against the azd environment, leaving Foundry server-side `${{...}}` expressions untouched.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.
- [[#8651]](https://github.com/Azure/azure-dev/pull/8651) Update Go to 1.26.4 and bump golang.org/x/crypto and golang.org/x/net. Thanks @hemarina for the contribution!

### Bugs Fixed

- [[#8790]](https://github.com/Azure/azure-dev/pull/8790) Fix hidden command visibility nit and add unit tests for the `azure.ai.routines` client.

## 0.1.0-preview (2026-05-28)

- Initial preview release of the `azure.ai.routines` extension for managing
  Microsoft Foundry Routines from the CLI.
- Adds the `azd ai routine` command group with `create`, `update`, `show`,
  `list`, `delete`, `enable`, `disable`, `dispatch`, and `run list` commands,
  plus YAML/JSON manifest-based authoring with timer, recurring, GitHub issue,
  and custom triggers and `agent-response` / `agent-invoke` actions.
- Uses the shared Foundry project-endpoint resolution cascade with
  `azure.ai.projects` and includes improved guidance when the extension is run
  outside an initialized azd project.