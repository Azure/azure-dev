# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Internal opt-in mechanism, the feature registry, and design. No command adoption in this change.

Keep preview opt-in decisions inside the Microsoft Foundry agents extension, without adding an azd core feature registry or public extension SDK API. The adoption scope is limited to **managed harness agents**, **voice agents**, and **long-running agent invocation**. General hosted agents and plain prompt functionality are not gated.

## Ownership and code organization

```text
azure.ai.agents/
├── internal/
│   ├── cmd/                       # Command discovery and runtime callers
│   ├── project/                   # Project/service integration callers
│   └── featureflags/
│       ├── featureflags.go        # Validation, source reads, evaluation
│       ├── featureflags_test.go   # Source combinations and regressions
│       ├── registry.go            # Every declared feature and All
│       └── registry_test.go       # Declaration rules
└── docs/specs/
    └── preview-feature-flags.md
```

Dependency direction is `internal/cmd` or `internal/project` → `internal/featureflags` → existing azd services through `azdext.AzdClient` (`UserConfig`, `Project`, `Environment`). The helper also uses the existing `pkg/config` reader to preserve nested alpha key lookup semantics. It never imports command or project orchestration.

The package belongs to the `azureaiagent` module and is imported as `azureaiagent/internal/featureflags`. Go's `internal` boundary excludes other extension modules. It requires neither a new module nor a root-module replacement or dependency upgrade to expose the helper.

The host-sentinel compatibility tests import the pinned azd environment packages. Their transitive dependencies require indirect entries for `heredoc/v2`, `azblob`, and `azfile` in this module. The azd requirement stays at `v1.35.1`.

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

`IsEnabled` rejects a nil client or an empty `ID` or `EnvVar` before reading sources. `Description` is optional and never read by the evaluator.

## Documented enablement methods

The documented ways to opt into a feature are listed below. These opt-ins are additive: any enabling value is sufficient.

| Source | Read | Enabling value |
| --- | --- | --- |
| User config | `UserConfig.Get("alpha")` (`azd config set`) | Case-insensitive string `on` only at `alpha.<ID>` |
| Process environment | Only the declared `EnvVar` | A true value accepted by `strconv.ParseBool` |
| Selected azd environment | Exact `EnvVar` in raw `.env` values from `Environment.GetValues` (`azd env set`) | A true value accepted by `strconv.ParseBool` |

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

Command discovery and runtime enforcement are separate caller responsibilities. A future discovery check may hide a preview-only command, but that alone cannot gate preview flags on existing commands, project/service flows, or direct invocation paths. Runtime callers must check the selected capability before its side effects and forward the actual environment selection. No command registration, command behavior, or runtime gate changes in this PR.

Only the three registered capabilities are candidates for adoption. Agents-private checks do **not** automatically intercept evaluations or routines sending generic requests to existing agents. Those extensions cannot import this package, and this design makes no cross-extension enforcement claim.

The small API deliberately returns only an enablement decision and error, not a source report or policy object. OR semantics avoid accidental disabling by unrelated scopes, at the cost of having no local override for an existing opt-in. Exact optional-error matching avoids swallowing real failures but remains coupled to the existing host error contract.

The registry settles the three identities and environment keys. Exact command and runtime check locations and disabled-feature guidance belong with the behavior changes that adopt them.

## Validation

Tests cover source combinations, all supported boolean spellings and non-enabling values, nested per-feature alpha config, ignored `alpha.all` and unrelated keys, exact environment keys, explicit environment forwarding, optional scopes and sentinel compatibility, genuine failures despite positive sources, malformed responses, context forwarding, and snapshot isolation without input mutation.

Registry tests cover declaration rules, the stable copy returned by `All`, and `Lookup`.

Run focused tests and vet for `./internal/featureflags`, build the agents module against its pinned azd dependency, and apply the module's formatting/lint rules. No Azure scenario or user auth/config mutation is needed for this mechanism-only change.
