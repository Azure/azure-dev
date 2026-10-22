# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Independent, default-off managed-harness, voice, and long-running invocation gates.

Keep preview opt-in decisions inside the Microsoft Foundry agents extension, without adding an azd core feature registry or public extension SDK API. General hosted agents and plain prompt functionality are not gated. The three features are declared in the shared registry described below.

Managed harness means `kind: prompt` with harness type `microsoft_copilot_preview` or legacy `github_copilot_preview`. Voice includes `voice`, `prompt-voice`, and hosted definitions advertising `invocations_ws`. Long-running means starting a background Responses invocation through `--long-running` or `--no-wait`, not ordinary invocation lifecycle commands. The obsolete `agents.hosted` opt-in does not enable any of these gates.

## Ownership and code organization

```text
azure.ai.agents/
├── internal/
│   ├── cmd/
│   │   ├── root.go                # One pre-run check and the help hook
│   │   └── preview_features.go    # Annotations, enforcement, help hiding
│   ├── project/
│   │   └── preview_features.go    # Definition classification and checks
│   └── featureflags/
│       ├── agents.go              # Require, environment binding, process read
│       ├── featureflags.go        # Validation, source reads, evaluation
│       ├── featureflags_test.go   # Source combinations and regressions
│       ├── registry.go            # Every declared feature and All
│       └── registry_test.go       # Declaration rules
└── docs/specs/
    └── preview-feature-flags.md
```

Dependency direction is `internal/cmd` or `internal/project` → `internal/featureflags` → existing azd services through `azdext.AzdClient` (`UserConfig`, `Project`, `Environment`). The helper also uses the existing `pkg/config` reader to preserve nested alpha key lookup semantics. It never imports command or project orchestration.

The package belongs to the `azureaiagent` module and is imported as `azureaiagent/internal/featureflags`. Go's `internal` boundary excludes other extension modules. It requires neither a new module nor a root-module replacement or dependency upgrade to expose the helper.

The host-sentinel compatibility tests import the pinned azd environment packages. Their transitive dependencies require indirect entries for `heredoc/v2`, `azblob`, and `azfile` in this module. The azd requirement is `v1.37.0`.

Keep validation, reads, and small evaluation helpers together in one implementation file. Splitting them into a loader framework or generic source abstraction would add indirection without another implementation to support. The registry is a plain list of declarations, not a plugin system.

## API and caller contract

```go
type Feature struct {
    ID          string
    EnvVar      string
    Description string
}

func CheckEnvironmentFlag(flags *pflag.FlagSet) error

func IsEnabled(
    ctx context.Context,
    client *azdext.AzdClient,
    feature Feature,
    environmentName string,
) (bool, error)

func All() []Feature
func Lookup(id string) (Feature, bool)
```

Callers must call `CheckEnvironmentFlag(cmd.Flags())` before `IsEnabled` and before any source read. An explicit empty or whitespace only `-e/--environment` selection is invalid. The check returns a validation error with code `invalid_parameter`. It returns nil for a nil flag set, an absent flag, an omitted flag, or a nonempty selection.

Pass the parsed `azdext.ExtensionContext.Environment` unchanged to `IsEnabled`. An empty name means the flag was omitted and lets the host select the project default. A nonempty name is passed to `Environment.GetValues`, so an explicit selection takes precedence over the project default. The helper does not infer selection from ambient `AZD_ENVIRONMENT`.

## Feature registry

`registry.go` declares every agents preview feature in one place. `All` returns them in a stable order as a fresh slice. `Lookup` finds a declaration by ID. Command help, enforcement, and listings read this list instead of declaring their own identities.

| Declaration | Feature ID | Environment key | Scope |
| --- | --- | --- | --- |
| `ManagedHarness` | `agents.managed_harness` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS` | Prompt agents that run on a Copilot harness |
| `Voice` | `agents.voice` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE` | Voice, prompt-voice, and hosted voice agents |
| `LongRunning` | `agents.long_running` | `AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING` | Long-running Responses invocations |

Each ID starts with `agents.`. Each environment key is `AZD_FOUNDRY_EXT_ENABLE_` plus the ID in upper case with dots replaced by underscores. Tests enforce both rules, unique IDs and keys, and a nonempty description. The helper still reads `EnvVar` as declared and never derives it at run time.

```go
if err := featureflags.CheckEnvironmentFlag(cmd.Flags()); err != nil {
    return err
}
enabled, err := featureflags.IsEnabled(
    extCtx.Context(),
    azdClient,
    featureflags.Voice,
    extCtx.Environment,
)
```

`IsEnabled` returns a bound environment validation error first. It then rejects a nil client or an empty `ID` or `EnvVar` before reading sources. `Description` is optional and never read by the evaluator.

`IsEnabled` itself never reads `AZD_ENVIRONMENT`. The root pre-run binds the parsed `ExtensionContext.Environment` with `WithEnvironment` and the result of `CheckEnvironmentFlag` with `WithEnvironmentError`. The SDK falls back to `AZD_ENVIRONMENT` when the flag is omitted. `Require` returns the bound validation error before any source read. An explicit empty or whitespace only value cannot fall back to the default environment. Otherwise `Require` passes `Environment(ctx)` to `IsEnabled`. Without a bound value, it uses `AZD_ENVIRONMENT`, which azd sets for lifecycle callbacks.

`Require` delegates all persisted-source reads to `IsEnabled` and returns the common disabled-feature error. Agent-kind and harness classification belong to `internal/project`, alongside the existing inline and `$ref` resolver, not to the flag reader. The context holds the selected environment, its validation error, and the service gate marker.

## Central enforcement

Enforcement has two layers. Each layer has one check.

**Command layer.** Commands and flags carry annotations that name a registered feature. The root `PersistentPreRunE` runs after the SDK pre-run, so `-e` and `AZD_ENVIRONMENT` are already applied. It calls `enforcePreviewFeatures` for the executing command with its args. Whenever the gate would reject, it first runs the command's local argument checks from `validateBeforePreview` so local conflicts are reported before `feature_not_enabled` or an empty environment error. These checks use only flags and args, with no source reads or state changes. The existing debug logging and telemetry steps keep their order. The gate makes no host reads for a command without a preview annotation or a set preview flag. A bound environment error only fails commands that perform a preview check.

| Annotation | Placed on | Effect |
| --- | --- | --- |
| `previewFlag(cmd, name, feature)` | `invoke --long-running`, `invoke --no-wait`, `init --voice` | Setting the flag requires the feature (a bool only when true). The flag is hidden while the feature is off |
| `previewFlag(cmd, name, feature, values...)` | `init --kind prompt-voice`, `init --harness microsoft_copilot_preview` or `github_copilot_preview`, `init --protocol invocations_ws` | Only the listed values require the feature, including entries in repeated or comma-separated slice flags. The flag stays visible |
| `previewCommand(cmd, feature)` | none today | The command and its children require the feature and are hidden while off |
| `discoverCommand(cmd, feature)` | `invocations` | Hidden from help while off. The group and its subcommands still run |
| `previewHelp(cmd, feature, long, examples)` | `init` voice section and examples, `invoke` voice and long-running sections and examples, voice notes in `add`, `delete`, and `endpoint show` | The blocks are removed from help while off |
| `previewFlagUsage(cmd, name, feature, usage)` | `init --kind`, `init --protocol`, `init --acr-connection`, `init --model` | The flag keeps working. Its usage text is swapped for one that omits the feature while off |
| `gateAgentServices(cmd)` | `invoke`, `run`, `optimize deploy`, `optimize apply`, `endpoint update` | Marks the context so the shared service selector classifies every selected agent service |

**Service layer.** The shared selector `resolveAgentService` and the deployed-name fallback call `requireSelectedServiceFeatures`. It runs `project.RequireAgentFeatures` only when the running command carries `gateAgentServices`. Show, delete, endpoint show, invocations, eval, and other selectors stay outside the gate. A gate failure is wrapped so callers that recover from selection errors (deployed-name lookup, optimize project fallback, protocol resolution) return it unchanged.

**Lifecycle layer.** The service target has one gate, `prepareGatedOperation`, used by `GetTargetResource`, `Package`, `Publish`, and `Deploy`. `Initialize` and `Endpoints` stay outside the gate so read and cleanup flows work. The `preprovision` and `predeploy` handlers call the same project classification before they write environment values or project settings.

### Direct checks that remain

These entry points classify content that only exists at run time, so an annotation cannot express them.

| Call site | Why it stays direct |
| --- | --- |
| `init` existing project and explicit local `azure.yaml` | The services come from a file read during the command |
| `init` staged template adoption (`runInitFromAzureYaml`, `preflightAzdTemplate`) | The definition exists only after staging |
| `init` picker (`promptInitMode`) | Discovery only. It omits the voice choice while voice is off |
| Hosted `init` protocol picker | It omits `invocations_ws` while voice is off and validates the selection before creating a project or environment. Explicit ordinary protocols and the no-prompt Responses default need no feature read |
| `agent add` | The definition comes from `--file` content |
| `invoke --local` (`selectLocalService`) | Local invoke does not select a service otherwise. It calls the gated selector |
| `optimize deploy` remote definition | The definition is fetched from the service before a new version is created |

## Help hiding

`NewRootCommand` builds the tree without an azd client, and help rendering skips `PersistentPreRunE`. The root help function resolves feature states when help is shown. Cobra has already parsed flags, so `-e` is honored. Without `-e`, `AZD_ENVIRONMENT` is used. An explicit empty or whitespace only selection uses process values only and makes no host reads. For other selections, the evaluator reads user config, the process environment, and the selected azd environment through a client created at that moment with a ten second bound. When `AZD_SERVER` is absent or a read fails, help falls back to the process value only. Help never fails because of a flag read. The states are applied to the whole tree once (`Hidden` on commands and flags, usage text swaps, and help block removal) before the default help renders.

Trade-offs:

- Help makes a few host reads. Commands that run without `--help` pay nothing extra unless a preview flag or annotation applies.
- Extension metadata and shell completion are built from the static tree, so they still list preview flags and the `invocations` group. azd caches metadata, so it cannot follow a per-environment state.
- Hidden commands and flags still parse and run. Hiding is discovery only. Enforcement is the pre-run check.
- `--harness` stays hidden in all states, as on `main`.

For example, either of these enables voice:

```bash
azd config set alpha.agents.voice on
azd env set AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE true
```

The [README](../../README.md#preview-feature-opt-ins) lists the equivalent commands for all three capabilities.

## Documented enablement methods

The documented ways to opt into a feature are listed below. These opt-ins are additive: any enabling value is sufficient.

| Source | Read | Enabling value |
| --- | --- | --- |
| User config | `UserConfig.Get("alpha")` (`azd config set`) | Case-insensitive string `on` only at `alpha.<ID>` |
| Process environment | Only the declared `EnvVar` | A true value accepted by `strconv.ParseBool` |
| Selected azd environment | Exact `EnvVar` in raw `.env` values from `Environment.GetValues` (`azd env set`) | A true value accepted by `strconv.ParseBool` |

Manifest decoding reuses the existing service decoder used by `agent add`, rather than maintaining a second interpretation of service fields.

Unrelated config keys cannot enable a preview or cancel an opt-in from another source.

False, `off`, empty, missing, and other non-enabling values never override another source's true value. Config requires a string `on` without surrounding whitespace. Booleans and other JSON types are non-enabling. Environment values accept exactly `1`, `t`, `T`, `true`, `TRUE`, or `True`. Values such as `on`, `yes`, and whitespace-padded values do not enable. There is no `ALL` environment toggle or experimentation default.

### Relation to azd alpha features

Agents previews reuse the `alpha.<id>` config key shape for `azd config set`, but are not part of the azd alpha feature set. Neither `alpha.all` nor `AZD_ALPHA_ENABLE_<NAME>`, such as `AZD_ALPHA_ENABLE_AGENTS_VOICE`, turns them on. Each preview must be turned on with its own `alpha.agents.<name>` key or its `AZD_FOUNDRY_EXT_ENABLE_*` variable. Each preview is chosen on its own, so a global switch cannot opt a user into a preview they did not choose. Core azd alpha feature behavior is unchanged.

## Reads, optional scopes, and errors

Each call captures the declared process value, reads user config, loads the project, and reads the selected environment when a project is available. It evaluates those per-call responses without merging source maps. This is a fresh per-invocation view, not a transaction across host reads.

| Situation | Result |
| --- | --- |
| No project, no explicit environment | Skip project and dotenv sources. Process and config still apply |
| Project without a default environment, no explicit selection | Skip dotenv. Process and config still apply |
| Explicit selection with no project or a missing environment | Return the error. Do not fall back to the default |
| Config/project/dotenv read, decoding, permission, or transport failure | Return an error even when another source enables the feature |
| Missing config/project/environment response, or malformed config JSON | Return an error rather than treating a failed load as disabled |

Only the host's exact no-project and no-default-environment sentinel messages, transported as `Unknown` without details (or as matching plain errors), qualify as optional absence. Similar messages, `NotFound` statuses, detailed statuses, and absence at the wrong read stage remain errors. Tests compare the sentinel text with the pinned azd dependency. Changes to the host contract require explicit compatibility review.

Errors retain their cause with `%w` and add read-stage/feature context. A failed call returns `false, error`. Callers must not use it as a successful disabled result. User-facing orchestration can classify errors following the [extension error guidance](../../../../docs/extensions/extensions-style-guide.md#error-handling-in-extensions).

There is no global cache, process environment mutation, output, configuration write, or authentication API call. Calls use the supplied context. The helper never prints source values.

## Adoption boundary and trade-offs

Discovery and runtime enforcement are separate. Help hides disabled commands, flags, and sections. The init picker omits the voice option when disabled. Managed harness has no separate picker. Template catalogs lack capability metadata, so catalog entries remain visible. Their staged effective definitions are checked before adoption instead of inferring capabilities from template names.

Runtime checks cover explicit init flags, existing/local/staged manifests, `agent add`, selected-service invocation and local run, optimize apply and endpoint update service selection, preprovision/predeploy callbacks, package/publish/deploy/resource selection, and optimize deployment. Inline definitions and root/field `$ref` includes use the existing resolver. Locally identifiable disabled inputs fail before authentication or project/environment writes. Optimize deployment also checks the definition it already fetches before creating a new version.

### Enforcement limits

An explicit HTTP `--agent-endpoint` carries no kind or harness information. It remains unclassified, with no additional authenticated definition lookup, so managed-harness enforcement cannot cover that route. The deterministic long-running check applies to every endpoint before invocation, authentication, or conversation/session persistence. Text invoke does not accept voice WebSocket endpoints.

Read, show, endpoint discovery, invocation show/follow/cancel, delete, and down retain access when opt-ins are disabled. There is no state migration or automatic enablement. Agents-private checks do **not** intercept evaluations or routines sending generic requests to existing agents. Those extensions cannot import this package, and this design makes no cross-extension enforcement claim.

The small API deliberately returns only an enablement decision and error, not a source report or policy object. OR semantics avoid accidental disabling by unrelated scopes, at the cost of having no local override for an existing opt-in. Exact optional-error matching avoids swallowing real failures but remains coupled to the existing host error contract.

Disabled capabilities return the structured `feature_not_enabled` compatibility error with the exact per-feature config/environment enable commands. Explicit preview inputs are rejected rather than silently dropped. Enabling one capability does not enable another.

## Validation

Tests cover source combinations, all supported boolean spellings and non-enabling values, nested per-feature alpha config, ignored `alpha.all` and unrelated keys, exact environment keys, explicit environment forwarding, optional scopes and sentinel compatibility, genuine failures despite positive sources, malformed responses, context forwarding, and snapshot isolation without input mutation. Registry tests cover declaration rules, the stable copy returned by `All`, and `Lookup`.

Command regressions run through the root command so the central pre-run applies. They cover which parsed flags and values require which feature, inherited command annotations, service gate placement, independent gates, obsolete hosted opt-in isolation, selected-environment forwarding for enforcement and help, help hiding per feature with a process fallback, hidden commands that stay reachable, staged manifests, local invocation, deployed-name fallback, unchanged inputs on rejection, ordinary flows, and retained read access. Help snapshots cover the full surface with every feature on and the default surface with every feature off. A walk over every command with every feature off rejects help text that names a disabled preview.

Run offline tests, build, vet, and scoped lint against the pinned SDK. Tier 0 scenarios cover the hidden and enabled help states.
