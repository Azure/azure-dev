# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Independent, default-off managed-harness, voice, long-running invocation, and A2A version-selection gates.

Keep preview opt-in decisions inside the Microsoft Foundry agents extension until the extension framework supports discovery and evaluation. Use the canonical `AZD_ALPHA_ENABLE_<NAME>` syntax so a future framework implementation can honor the same variables. General hosted agents and plain prompt functionality are not gated.

Managed harness means `kind: prompt` with harness type `microsoft_copilot_preview` or legacy `github_copilot_preview`. Voice includes `voice`, `prompt-voice`, and hosted definitions advertising `invocations_ws`. Long-running means starting a background Responses invocation through `--long-running` or `--no-wait`, not ordinary invocation lifecycle commands.

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
│       ├── agents.go              # Shared disabled-feature error
│       ├── featureflags.go        # Declaration validation and ENV read
│       ├── featureflags_test.go   # Process-environment contract tests
│       ├── registry.go            # Feature declarations, All, Lookup
│       └── registry_test.go       # Declaration rules
└── docs/specs/
    └── preview-feature-flags.md
```

The package belongs to the `azureaiagent` module and is imported as `azureaiagent/internal/featureflags`. Go's `internal` boundary excludes other extension modules. The evaluator uses only the Go standard library; it has no azd client, config, project, environment-loader, gRPC, or authentication dependency.

Keep the registry as a plain list, not a listing framework or plugin system. The extension stays on its pinned azd dependency, `v1.37.0`, without a local module replacement or dependency upgrade.

## API and caller contract

```go
type Feature struct {
    ID          string
    EnvVar      string
    Description string
}

func IsEnabled(feature Feature) (bool, error)
func Require(feature Feature) error
func All() []Feature
func Lookup(id string) (Feature, bool)
```

`ID` is a capability identity, not a user-config contract. `EnvVar` is the process environment key to read, including a value the azd host has already injected. `Description` is optional for evaluation and supplies a short scope summary in help.

```go
if err := featureflags.Require(featureflags.Voice); err != nil {
    return err
}
```

`IsEnabled` rejects an empty `ID` or `EnvVar` before reading the process environment. The function takes no context, azd client, selected-environment, project, or login argument. Values the host already injected are present in the process environment it reads. Command input validation stays with the commands; there is no flag-specific environment validation or init-scope exception.

`Require` delegates to `IsEnabled` and returns the structured `feature_not_enabled` compatibility error when the feature is off. Its suggestion names the canonical variable to set in the shell or CI. Agent-kind and harness classification belong to `internal/project`, alongside the existing inline and `$ref` resolver, not to the flag reader.

## Feature registry and help

`registry.go` declares each feature once. `All` returns a fresh slice in stable order, and `Lookup` finds a declaration by ID. Root help at `azd ai agent --help` always renders the canonical switches and their scopes while retaining ordinary project and environment guidance.

| Declaration | Feature ID | Process environment key | Scope |
| --- | --- | --- | --- |
| `ManagedHarness` | `agents.managed_harness` | `AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS` | Prompt agents that run on a Copilot harness |
| `Voice` | `agents.voice` | `AZD_ALPHA_ENABLE_AGENTS_VOICE` | Voice, prompt-voice, and hosted voice agents |
| `LongRunning` | `agents.long_running` | `AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING` | Long-running Responses invocations |
| `A2A` | `agents.a2a` | `AZD_ALPHA_ENABLE_AGENTS_A2A` | A2A init selection, explicit wire versions, and the 1.0 message default |

Each ID starts with `agents.`. Its canonical key is `AZD_ALPHA_ENABLE_` plus the ID in upper case with dots replaced by underscores. Registry tests enforce the identities, naming rule, unique IDs and keys, and nonempty descriptions. The evaluator reads the declared `EnvVar` rather than deriving a key at run time.

### A2A compatibility policy

Init offers `a2a` only with this preview on. Explicit parsed selections, including
repeated and comma-separated `--protocol` flags, are validated through a shared
selection check before authentication or writes. Interactive selection occurs
before project scaffolding. The independent Voice requirement for `invocations_ws`
is unchanged. Protocol help is rendered from both feature states without replacing
Voice flag annotations or pruning the static command metadata. The Hosted protocols
help block is hidden off. No-prompt init still defaults to Responses regardless of
both switches. This is an init-selection gate, not a deployment/lifecycle or
transport gate for existing A2A declarations.

Existing A2A invocation remains available with `agents.a2a` off: omitted
`--a2a-version` uses 0.3 for both messages and files, with file bytes unchanged.
Explicit `--a2a-version` requires the preview through the shared flag/root gate,
after pure local validation (including explicitly empty values and ordinary
argument conflicts). Version-specific flags, help, and examples are hidden off;
legacy A2A message and file discovery remains visible.

With the preview on, messages default to 1.0 and all A2A files require an
explicit supported version (0.3 or 1.0). Invocation entry fixes one policy and
default decision and passes the resolved version downstream. Builders, HTTP
handlers, and renderers do not read feature state. No agent metadata or file
heuristic selects the wire version.

## Process environment enablement

Set the desired variable in the shell or CI process environment before running azd. For example:

```sh
export AZD_ALPHA_ENABLE_AGENTS_VOICE=true
azd ai agent init --help
```

In PowerShell:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_VOICE = 'true'
azd ai agent init --help
```

All features default to off. Only `strconv.ParseBool` true values enable a feature: `1`, `t`, `T`, `true`, `TRUE`, or `True`. False values (`0`, `f`, `F`, `false`, `FALSE`, `False`), missing or empty values, and invalid values leave it off without an error. Values such as `on`, `yes`, mixed-case `TrUe`, and whitespace-padded strings are invalid.

Each feature is independent. There is no global toggle, experimentation default, or cross-feature opt-in.

### Evaluator source boundary

`IsEnabled` reads the declared key from the process environment with `os.Getenv`. That environment includes variables set in the shell or CI before azd starts and values the azd host has already placed there. On a normal agents command, the host appends the selected azd environment before it launches the extension. These commands do not opt out of that injection, so a value saved with `azd env set` is visible on the next `azd` invocation. The host appends those values after the parent process environment. When the same key appears twice, the child process keeps the last value, so the selected environment overrides the parent process for that key.

The evaluator does not query azd user config (including `alpha.all`), open selected or default environment files, or read project/service environment mappings. It does not filter process values by where they came from.

There is no cache, output, process-environment mutation, configuration write, authentication, or network call during evaluation. Each evaluation reads the current process value. A declaration error returns `false, error`. Valid declarations always return the process-environment decision; there is no host-read failure path.

Normal project configuration, deployment environments, runtime settings, and command input validation elsewhere in the extension are unchanged. Init retains the SDK-selected environment name when creating a project or environment, but gates and discovery do not load those scopes.

## Central enforcement

**Command layer.** Commands and flags carry annotations that name a registered feature. The root `PersistentPreRunE` calls `enforcePreviewFeatures` for the executing command with its args. Whenever the gate would reject, it first runs the command's local argument checks from `validateBeforePreview` so local conflicts are reported before `feature_not_enabled`. These checks use only flags and args, with no source reads or state changes. Existing debug logging and telemetry keep their order.

| Annotation | Placed on | Effect |
| --- | --- | --- |
| `previewFlag(cmd, name, feature)` | `invoke --long-running`, `invoke --no-wait`, `invoke --a2a-version`, `init --voice` | Setting the flag requires the feature (a bool only when true). The flag is hidden while the feature is off |
| `previewFlag(cmd, name, feature, values...)` | `init --kind prompt-voice`, `init --harness microsoft_copilot_preview` or `github_copilot_preview`, `init --protocol invocations_ws` | Only the listed values require the feature, including repeated or comma-separated slice values. The flag stays visible |
| `previewCommand(cmd, feature)` | none today | The command and its children require the feature and are hidden while off |
| `discoverCommand(cmd, feature)` | `invocations` | Hidden from help while off. The group and its subcommands still run |
| `previewHelp(cmd, feature, long, examples)` | Voice, long-running, and A2A version help blocks | The blocks are removed while off |
| `previewHelpText(cmd, feature, onText, offText)` | Existing inline preview mentions in command descriptions | Replaces the text only while off; enabled help retains its original wording |
| `previewFlagUsage(cmd, name, feature, usage)` | `init --kind`, `--protocol`, `--acr-connection`, `--model`, `--instructions` | The flag keeps working. Its usage omits the feature while off |
| `gateAgentServices(cmd)` | `invoke`, `run`, `optimize deploy`, `optimize apply`, `endpoint update` | Marks the context so shared selectors classify every selected agent service |

**Service layer.** Shared service selectors call `requireSelectedServiceFeatures`. It runs `project.RequireAgentFeatures` only when the command carries `gateAgentServices`. Read and cleanup selectors stay outside the gate. A gate failure is wrapped so callers that recover from selection errors return it unchanged instead of falling back to another lookup. Definition and service environment values do not enable a gate.

**Lifecycle layer.** The service target has one gate, `prepareGatedOperation`, used by `Package`, `Publish`, and `Deploy`. `Initialize`, `GetTargetResource`, and `Endpoints` stay outside the gate so read and cleanup flows work. The `preprovision` and `predeploy` handlers classify services before writing environment values or project settings.

### Direct checks that remain

These entry points classify content that only exists at run time, so an annotation cannot express them.

| Call site | Why it stays direct |
| --- | --- |
| `init` existing project and explicit local `azure.yaml` | Services come from content read during the command |
| `init` staged template adoption | Effective definitions are checked after staging, before adoption. Explicit remote `--template` inputs are staged and checked before Azure authentication; adoption reuses the validated directory |
| `init` picker | Discovery only. It omits the voice choice while off |
| Hosted `init` protocol picker | It omits `invocations_ws` while voice is off and validates selection before project/environment creation. Explicit ordinary protocols and the no-prompt Responses default need no feature check |
| `agent add` | Definitions come from `--file` content |
| `invoke --local` | The local path uses the gated selector when an agent service exists. A web-only project retains ordinary local invocation |
| `optimize` submission | The actual target is resolved before checking its local service. Matching config-file targets are checked; independent remote targets retain their authored identity and version |
| `optimize deploy` remote definition | The definition is already fetched before creating a new version |

Manifest decoding reuses the service decoder used by `agent add`. Inline definitions and root/field `$ref` includes use the existing resolver. Template catalogs lack capability metadata, so entries remain visible; staged definitions are checked instead of inferring features from names.

## Help hiding

`NewRootCommand` builds the tree without an azd client. Help skips `PersistentPreRunE`, so its hook reads the process switches directly before applying command/flag visibility, usage text, and help blocks. It does not query the host or load an environment, and it needs no timeout or fallback. The root switch list remains visible regardless of enabled state.

Help filtering only hides existing preview content. Enabling a switch preserves the original descriptions, examples, and visibility; it does not add help text or expose previously hidden flags.

Extension metadata and shell completion are built from the complete static tree. Hidden commands and flags still parse; hiding is discovery only, with enforcement at the operation entry points. `--harness` stays hidden in every state, as on `main`.

## Enforcement limits

An explicit HTTP `--agent-endpoint` carries no kind or harness information. It remains unclassified, with no additional authenticated definition lookup, so managed-harness enforcement cannot cover that route. The deterministic long-running check applies to every endpoint before invocation, authentication, or conversation/session persistence. Text invoke does not accept voice WebSocket endpoints.

Read, show, endpoint discovery, invocation show/follow/cancel, delete, and down retain access when opt-ins are disabled. There is no state migration or automatic enablement. Agents-private checks do not intercept evaluations or routines sending generic requests to existing agents. Those extensions cannot import this package, and this design makes no cross-extension enforcement claim.

## Compatibility and future framework adoption

This intentionally adopts the process-only mechanism in place of the earlier multi-source implementation:

- `AZD_FOUNDRY_EXT_ENABLE_*` names are no longer recognized; there is no legacy alias.
- The evaluator no longer reads user config or opens selected/default azd environment files. The host may still inject selected-environment values before launch.
- Feature evaluation no longer loads project/service scopes or calls the host.
- Old context/client/environment parameters, host-error sentinels, and flag-specific scope validation are removed.

Explicit disabled-preview inputs are rejected rather than silently dropped. The canonical syntax matches core azd alpha environment switches, but these capabilities are not registered with core alpha features. Core alpha behavior is unchanged. Do not use core `FeatureManager` here: it would also read azd config.

Discoverability through `azd config list-alpha`, a public SDK API, and framework/RPC support remain deferred. A future framework implementation can replace the private evaluator while retaining canonical variables and capability identities; broader source semantics would be an explicit change.

## Validation

Evaluator tests cover canonical keys, default-off behavior, accepted boolean forms, non-enabling values, all independent combinations, ignored old/global/unrelated keys, optional descriptions, fresh reads, and no input mutation. Temporary local fixtures verify that direct calls do not load configuration or dotenv files. These tests do not launch through the host.

Root-command regressions cover parsed flags, local-validation precedence, independent gates, ignored persisted config, no host reads for evaluation/help, fresh and existing init scopes, preserved SDK-selected creation names, disabled no-auth/no-write rejection, protocol pickers, staged manifests, local invocation, fallback error propagation, and retained read access. Project tests cover inline and referenced definitions, harness whitespace normalization, and lifecycle entry points. Help tests preserve the always-visible switch list while checking conditional command discovery; snapshots cover enabled and disabled states.

Remote-template regressions cover inline and `$ref` previews, rejection before Azure authentication, retained authentication when enabled, single-download adoption, and temporary-directory cleanup on failure.

Run offline tests, the full agents module build, and scoped static checks with Go 1.26.4. Tier 0 scenarios cover switch discovery and enabled/disabled help. Scenario execution remains opt-in through the `foundry-extension-scenario-orchestrator` agent; no Azure scenarios are required for this integration.
