# Shared Foundry feature access

<!-- cspell:ignore featureflags strconv -->

`featureflags` is the common opt-in reader for first-party Foundry extensions. It is self-contained and needs no azd core change. It reads only through existing azd gRPC services (`UserConfig`, `Environment`, `Project`).

It belongs to the root `cli/azd` Go module, like the shared `extensions/telemetry` package. An extension module imports it as `github.com/azure/azure-dev/cli/azd/extensions/featureflags` once its `github.com/azure/azure-dev/cli/azd` requirement points at a version that contains this package.

This package adds access only. No existing command or feature uses it yet.

## API

```go
var hosted = featureflags.Feature{
    ID:     "agents.hosted",
    EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_HOSTED",
}

enabled, err := featureflags.IsEnabled(
    extCtx.Context(),
    azdClient,
    hosted,
    extCtx.Environment,
)
```

Each extension declares its own `Feature`. There is no shared registry and no change to the core alpha feature list.

Pass the parsed `azdext.ExtensionContext.Environment` rather than discovering the default yourself. A nonempty name is forwarded to `Environment.GetValues` unchanged, so `-e/--environment` takes precedence over the project default. An invalid explicit selection is an error. An empty name uses the default environment if there is one.

## Independent enablement sources

A feature is enabled if **any** of these sources opts in:

| Source | Read | Enabling value |
| --- | --- | --- |
| Global user config | `UserConfig.Get("alpha")` (`azd config set`) | Case-insensitive `on` at `alpha.<ID>` or `alpha.all`, same as azd core |
| Process environment | Only `EnvVar` | A true value accepted by `strconv.ParseBool` |
| Selected azd environment | `EnvVar` in its `.env` values from `Environment.GetValues` (`azd env set`) | A true value accepted by `strconv.ParseBool` |
| Project service environment | `EnvVar` in each `services[].env` map from `Project.Get` | A true value accepted by `strconv.ParseBool` |

Canonical environment names are:

- `AZD_FOUNDRY_EXT_ENABLE_<FEATURE>` for shared capabilities.
- `AZD_FOUNDRY_EXT_ENABLE_AGENTS_<FEATURE>` for agent-specific capabilities.
- `AZD_FOUNDRY_EXT_ENABLE_MODELS_<FEATURE>` for model-specific capabilities.

All service hosts count. Service maps stay separate, so a true value in any service wins regardless of other services' values or order.

False, empty, missing, and unrecognized values contribute no signal and never override another source's opt-in. Non-string config values are also non-enabling. There is no `ALL` environment toggle, `AZD_ALPHA_ENABLE_*` alias, or experimentation default.

## Scope and errors

Each call loads fresh snapshots and discards them. There is no global cache, process environment mutation, output, authentication call, or configuration write.

Outside a project, only process environment and user config apply. In a project without a default environment, process, config, and service opt-ins still apply. Only the host's exact no-project and no-default-environment errors count as absence, and only when no environment name was supplied. Other project, dotenv, config, decoding, and transport errors are returned with context, even when another source opts in.
