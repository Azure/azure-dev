# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Independent, default-off managed-harness, voice, and long-running invocation gates.

Keep preview opt-in decisions inside the Microsoft Foundry agents extension, without adding an azd core feature, registry, or public extension SDK API. General hosted agents and plain prompt functionality are not gated.

| Declaration | Feature ID | Explicit environment key |
| --- | --- | --- |
| `ManagedHarness` | `agents.managed_harness` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS` |
| `Voice` | `agents.voice` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE` |
| `LongRunning` | `agents.long_running` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING` |

Managed harness means `kind: prompt` with harness type `microsoft_copilot_preview` or legacy `github_copilot_preview`. Voice includes `voice`, `prompt-voice`, and hosted definitions advertising `invocations_ws`. Long-running means starting a background Responses invocation through `--long-running` or `--no-wait`, not ordinary invocation lifecycle commands. The obsolete `agents.hosted` opt-in does not enable any of these gates.

## Ownership and code organization

```text
azure.ai.agents/
├── internal/
│   ├── cmd/                       # Command discovery and runtime callers
│   ├── project/                   # Project/service integration callers
│   └── featureflags/
│       ├── agents.go              # Identities, classification, enforcement
│       ├── featureflags.go        # Validation, source reads, evaluation
│       └── featureflags_test.go   # Source combinations and regressions
└── docs/specs/
    └── preview-feature-flags.md
```

Dependency direction is `internal/cmd` or `internal/project` → `internal/featureflags` → existing azd services through `azdext.AzdClient` (`UserConfig`, `Project`, `Environment`). The helper also uses the existing `pkg/config` reader to preserve nested alpha key lookup semantics. It never imports command or project orchestration.

The package belongs to the `azureaiagent` module and is imported as `azureaiagent/internal/featureflags`. Go's `internal` boundary excludes other extension modules. It requires neither a new module nor a root-module replacement or dependency upgrade to expose the helper.

The host-sentinel compatibility tests import the pinned azd environment packages. Their transitive dependencies require indirect entries for `heredoc/v2`, `azblob`, and `azfile` in this module; the azd requirement stays at `v1.35.1`.

Keep validation, reads, and small evaluation helpers together in one implementation file. Splitting them into a loader framework, registry, or generic source abstraction would add indirection without another implementation to support.

## API and caller contract

```go
type Feature struct {
    ID     string
    EnvVar string
}

func IsEnabled(
    ctx context.Context,
    client *azdext.AzdClient,
    feature Feature,
    environmentName string,
) (bool, error)
```

The private package declares the three identities explicitly; it does not register features in core or derive environment keys:

```go
enabled, err := featureflags.IsEnabled(
    extCtx.Context(),
    azdClient,
    featureflags.Voice,
    extCtx.Environment,
)
```

`IsEnabled` rejects a nil client or an empty `ID` or `EnvVar` before reading sources. Pass the parsed `azdext.ExtensionContext.Environment` unchanged. A nonempty name is forwarded to `Environment.GetValues`, so explicit `-e/--environment` takes precedence over the project default. An empty name delegates default selection to the host; the helper does not infer selection from ambient `AZD_ENVIRONMENT`.

Command callers bind the parsed environment into context with `WithEnvironment`. `Require` uses that value unchanged, including an explicit empty value. Lifecycle callbacks without that command context use the host's `AZD_ENVIRONMENT` selection.

For example, either of these enables voice:

```bash
azd config set alpha.agents.voice on
azd env set AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE true
```

The [README](../../README.md#preview-feature-opt-ins) lists the equivalent commands for all three capabilities.

## Four independent enablement sources

A feature is enabled if **any** of these sources opts in:

| Source | Read | Enabling value |
| --- | --- | --- |
| User config | `UserConfig.Get("alpha")` (`azd config set`) | Case-insensitive `on` at `alpha.<ID>` or `alpha.all`, matching core config parsing |
| Process environment | Only the declared `EnvVar` | A true value accepted by `strconv.ParseBool` |
| Selected azd environment | Exact `EnvVar` in raw `.env` values from `Environment.GetValues` (`azd env set`) | A true value accepted by `strconv.ParseBool` |
| Project service environment | Exact `EnvVar` in each `services[].env` map from `Project.Get` | A true value accepted by `strconv.ParseBool` |

All service hosts count, including non-Foundry services. Service maps stay separate: a true value in any service wins regardless of other services' values or iteration order. These are project-wide opt-ins, not per-service permissions.

Before adopting a staged manifest, `WithServices` includes its prospective service environment maps in the same service source. Definition-file `env` is included before `agent add` writes the service. This permits an authored opt-in without first writing a manifest that would otherwise be rejected. Existing config/project/environment read failures still propagate even if the prospective manifest enables the feature.

False, `off`, empty, missing, and other non-enabling values never override another source's true value. Config requires a string `on` without surrounding whitespace; booleans and other JSON types are non-enabling. Environment values accept exactly `1`, `t`, `T`, `true`, `TRUE`, or `True`; `on`, `yes`, and whitespace-padded values do not enable. There is no `ALL` environment toggle, `AZD_ALPHA_ENABLE_*` alias, or experimentation default.

## Reads, optional scopes, and errors

Each call captures the declared process value, reads user config, loads the project, and reads the selected environment when a project is available. It evaluates those per-call responses without merging source maps. This is a fresh per-invocation view, not a transaction across host reads.

| Situation | Result |
| --- | --- |
| No project, no explicit environment | Skip project and dotenv sources; process and config still apply |
| Project without a default environment, no explicit selection | Skip dotenv; process, config, and every service still apply |
| Explicit selection with no project or a missing environment | Return the error; do not fall back to the default |
| Config/project/dotenv read, decoding, permission, or transport failure | Return an error even when another source enables the feature |
| Missing config/project/environment response, or malformed config JSON | Return an error rather than treating a failed load as disabled |

Only the host's exact no-project and no-default-environment sentinel messages, transported as `Unknown` without details (or as matching plain errors), qualify as optional absence. Similar messages, `NotFound` statuses, detailed statuses, and absence at the wrong read stage remain errors. Tests compare the sentinel text with the pinned azd dependency; changes to the host contract require explicit compatibility review.

Errors retain their cause with `%w` and add read-stage/feature context. A failed call returns `false, error`; callers must not use it as a successful disabled result. User-facing orchestration can classify errors following the [extension error guidance](../../../../docs/extensions/extensions-style-guide.md#error-handling-in-extensions).

There is no global cache, process environment mutation, output, configuration write, or authentication API call. Calls use the supplied context. The helper never prints source values.

## Adoption boundary and trade-offs

Discovery and runtime enforcement are separate. The init picker omits the voice option when disabled; managed harness has no separate picker. Template catalogs lack capability metadata, so catalog entries remain visible. Their staged effective definitions are checked before adoption instead of inferring capabilities from template names.

Runtime checks cover explicit init flags, existing/local/staged manifests, `agent add`, selected-service invocation and local run, preprovision/predeploy callbacks, package/publish/deploy/resource selection, and optimize deployment. Inline definitions and root/field `$ref` includes use the existing resolver. Locally identifiable disabled inputs fail before authentication or project/environment writes. Optimize deployment also checks the definition it already fetches before creating a new version.

An explicit HTTP `--agent-endpoint` carries no kind or harness information. It remains unclassified, with no additional authenticated definition lookup, so managed-harness enforcement cannot cover that route. The deterministic long-running check applies to every endpoint before invocation, authentication, or conversation/session persistence. Text invoke does not accept voice WebSocket endpoints.

Read, show, endpoint discovery, invocation show/follow/cancel, delete, and down retain access when opt-ins are disabled. There is no state migration or automatic enablement. Agents-private checks do **not** intercept evaluations or routines sending generic requests to existing agents. Those extensions cannot import this package, and this design makes no cross-extension enforcement claim.

The small API deliberately returns only an enablement decision and error, not a source report, registry entry, or policy object. OR semantics avoid accidental disabling by unrelated scopes, at the cost of having no local override for an existing opt-in. Exact optional-error matching avoids swallowing real failures but remains coupled to the existing host error contract.

Disabled capabilities return the structured `feature_not_enabled` compatibility error with the exact config/environment enable commands. Explicit preview inputs are rejected rather than silently dropped. Enabling one capability does not enable another; `alpha.all=on` remains the shared config opt-in.

## Validation

Tests cover all 16 combinations of the four sources, all supported boolean spellings and non-enabling values, nested alpha config and `alpha.all`, exact environment keys, every service host and conflicting service order, explicit environment forwarding, optional scopes and sentinel compatibility, genuine failures despite positive sources, malformed responses, context forwarding, and snapshot isolation without input mutation.

Command/provider regressions additionally cover independent gates, obsolete hosted opt-in isolation, selected-environment forwarding, voice discovery, explicit init/add inputs, staged manifests, local invocation bypasses, unchanged inputs on disabled-feature rejection, ordinary flows, and retained read access.

Run offline tests, build, vet, and scoped lint against the pinned SDK. Tier 0 scenarios document the new opt-in UX; they require a separate scenario-run request. No Azure scenario execution or user auth/config mutation is needed for unit validation.
