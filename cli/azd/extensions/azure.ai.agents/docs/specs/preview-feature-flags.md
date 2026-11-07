# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent BYOAKS -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Independent, default-off agent previews, BYO AKS initialization/adoption/provisioning/preview, retained RBAC warnings, and the shared Foundry existing-ACR-reuse gate.

Keep preview opt-in decisions inside the owning extensions until the extension framework supports discovery and evaluation. The Agents extension owns its private preview evaluator; the Projects extension independently checks the shared BYO AKS switch for provisioning and provisioning preview when effective AKS hosting is enabled. Use the canonical `AZD_ALPHA_ENABLE_<NAME>` syntax so a future framework implementation can honor the same variables. General hosted agents and plain prompt functionality without new ACR reuse intent are not gated.

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

Shared Foundry ACR evaluation and read-only lifecycle-intent checks live in
`internal/synthesis/acr_feature.go`, mirrored byte-for-byte in agents and
projects. The agents registry references that shared capability's constants;
neither extension imports the other's internal packages. The boolean reader
only uses the process environment. Separate operation guards read host-provided
environment snapshots and canonical/pending lifecycle state to prevent
alias-free bindings from bypassing the gate.
`acr_parameters.go` also checks the configured Foundry on-disk parameter
source. `bicep_parameters.go` shares ARM parameter parsing, substitution,
compiled Bicep parameter envelopes, and value/default precedence with the
projects provider. Both files remain mirrored in the two extensions.

Keep the registry as a plain list, not a listing framework or plugin system. The extension stays on its pinned azd dependency, `v1.37.0`, without a local module replacement or dependency upgrade.

## API and caller contract

```go
type Feature struct {
    ID             string
    EnvVar         string
    Description    string
    DefaultEnabled bool
}

func IsEnabled(feature Feature) (bool, error)
func Require(feature Feature) error
func RequireWithWriter(feature Feature, writer io.Writer) error
func Check(feature Feature) error
func RequireWithNotice(feature Feature) (string, error)
func All() []Feature
func Lookup(id string) (Feature, bool)
```

`ID` is a capability identity, not a user-config contract. `EnvVar` is the process environment key to read, set in the shell or CI before running azd (or through scenario `env:`). `Description` is optional for evaluation and supplies a short scope summary in help. `DefaultEnabled` applies only when the variable is absent; all declared features default to `false`.

```go
if err := featureflags.RequireWithWriter(featureflags.Voice, out); err != nil {
    return err
}
```

`IsEnabled` rejects an empty `ID` or `EnvVar` before reading only the current process environment with `os.LookupEnv`. The function takes no context, azd client, selected-environment, project, or login argument. The extension inherits shell/CI process values. Lifecycle listeners do not receive selected-environment dotenv flattening for these switches, while direct `azd ai agent ...` commands still receive selected-environment values after shell/CI values. Command input validation stays with the commands; there is no flag-specific environment validation or init-scope exception.

`Require` delegates to `RequireWithWriter` using stdout. `RequireWithWriter` returns the structured `feature_not_enabled` compatibility error when the feature is off. Its suggestion names the canonical variable to set in the shell or CI. When enabled, it prints a notice naming the feature and switch and explaining that preview behavior may change. The notice is printed only when a command checks a feature it uses, and only once for that feature in an extension process. Callers with an injected output writer use `RequireWithWriter`; JSON and raw commands route notices to stderr. Passing `io.Discard` validates the feature without consuming its notice. `Check` performs the same enabled/disabled check without announcing or consuming a notice, for lifecycle stages whose output cannot be shown immediately. `RequireWithNotice` returns the plain notice text for a host-visible lifecycle response. A nil writer defaults to stdout. `IsEnabled` remains silent and side-effect free. Agent-kind and harness classification belong to `internal/project`, alongside the existing inline and `$ref` resolver, not to the flag reader.

## Feature registry and help

`registry.go` declares each feature once. `All` returns a fresh slice in stable order, and `Lookup` finds a declaration by ID. Root help at `azd ai agent --help` always renders the canonical switches and their scopes while retaining ordinary project and environment guidance.

| Declaration | Feature ID | Process environment key | Scope |
| --- | --- | --- | --- |
| `ManagedHarness` | `agents.managed_harness` | `AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS` | Prompt agents that run on a Copilot harness |
| `Voice` | `agents.voice` | `AZD_ALPHA_ENABLE_AGENTS_VOICE` | Voice, prompt-voice, and hosted voice agents |
| `LongRunning` | `agents.long_running` | `AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING` | Long-running Responses invocations |
| `A2A` | `agents.a2a` | `AZD_ALPHA_ENABLE_AGENTS_A2A` | A2A init selection, explicit wire versions, and the 1.0 message default |
| `MemorySnapshot` | `agents.memory_snapshot` | `AZD_ALPHA_ENABLE_AGENTS_MEMORY_SNAPSHOT` | Hosted agent memory snapshots |
| `BYOAKS` | `agents.byo_aks` | `AZD_ALPHA_ENABLE_AGENTS_BYO_AKS` | Existing AKS project initialization, adoption, provisioning, and provisioning preview |
| `FoundryACRReuse` | `foundry.acr_reuse` | `AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE` | External source-build ACR reuse in new Foundry projects, including preview and cleanup |
| `RBACWarnings` | `agents.rbac_warnings` | `AZD_ALPHA_ENABLE_AGENTS_RBAC_WARNINGS` | Retained developer RBAC deployment warnings through beta service events |

Agent-specific IDs start with `agents.`; the shared ACR capability uses
`foundry.acr_reuse`. Each canonical key is `AZD_ALPHA_ENABLE_` plus the ID in
upper case with dots replaced by underscores. Registry tests enforce those
namespaces, unique IDs and keys, and nonempty descriptions. The evaluator reads
the declared `EnvVar` rather than deriving a key at run time.

### Shared Foundry ACR reuse policy

`AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE` is one default-off rollout switch for both
extensions, independent of all agent-kind previews. It gates both
`--acr-resource-id` entrypoints, greenfield environment-only selections,
canonical reuse bindings, unfinished access operations, template/source init,
project setup, synthesis, provision, preview, down, recovery, eject, and agent
package/publish/deploy operations that encounter new reuse intent.

Lifecycle guards honor the command's `--environment` selection, forwarded by
the host as `AZD_ENVIRONMENT`, before consulting the saved default. Values,
registry bindings, and pending operations are read from that same environment.
An unreadable explicit selection is an error, not a fallback to the default.
Prompt-agent cleanup during down reads its deployment target and ownership
markers from that selected environment as well, never from another default.
Infrastructure eject uses the same resolved environment for reuse checks and
the values supplied to generated files.
Template and repository initialization read only the destination project's
selected environment, not the caller's saved default. A pending operation in
that destination is gated even without mode, registry, or binding aliases.

Attempting gated reuse while off returns a `feature_not_enabled` error naming the
enabling variable, before changes to project files, environment/config values,
saved context, or Azure resources. Bicep tool installation and module
restoration may populate dependency caches before parameter evaluation.
Pure local flag validation still takes precedence, including explicit empty
values. Help and examples hide the selector off, while full static metadata
retains it. There is no auto-enable, ignored selector, create fallback,
ownership conversion, or deployed-registry migration.

Unlike the older agent-kind previews, **cleanup is also gated**. An
already-reuse-bound environment needs the switch enabled for down, preview,
recovery, and eject, even if its legacy mode/registry aliases were removed.
Re-enable the switch in the shell or CI before cleanup. Turning it off leaves
the resources, grants, files, and saved state intact.
Down checks recorded environment selectors, canonical bindings, unfinished
access operations, and recovered deployment facts. It does not compile
current infrastructure or evaluate current parameter files. A source-only
selector that has not been deployed does not block teardown.

For greenfield reuse, other pre-operation guards inspect the active
`existingAcrResourceId` even when it
exists only in an on-disk parameter file and all aliases, bindings, and
pending state are absent. Discovery follows the configured
`microsoft.foundry` root or layer path and module; unrelated root/layer
files are not selected. `.bicepparam` takes precedence over
`.bicep` plus `.parameters.json`. JSON substitutions use host-provided
environment values, while Bicep resolves expressions before hook or
independent agent-operation writes. Parameter evaluation and deployment
retain normal Bicep installation, version checks, and module restoration
with the switch either off or on. Dependency caches may change, but project
files, environment/config values, saved context, and Azure resources remain
unchanged when reuse is rejected. Invalid parameters are errors, not a
non-reuse fallback. An invalid explicit `AZD_BICEP_TOOL_PATH` fails rather
than falling back to another tool.

Provider initialization remains compiler-free. For on-disk infrastructure,
prompted subscription and location values remain staged until deployment
has compiled and validated the effective template. Preview and deployment
still reject disabled parameter-only reuse before Azure mutations.

The `azure.ai.projects.acr_remote_build` provision validation rule uses the
same gate before querying registry permissions. Its diagnostic preserves
structured enablement guidance. Mandatory prepackage, preprovision, and
predeploy hooks do not depend on that optional validation rule running.

Demand-driven automatic `none`/`create`, verified legacy CREATE outputs, code,
prebuilt images, and old existing-project connection workflows without new
reuse intent remain available. Existing create ownership and teardown safety
checks stay active even while the preview is off.

The source-build demand consistency correction is intentionally **always on**.
A bare `image` still requires automatic ACR provisioning when package/deploy
would build from source, including no-prompt flows. Explicit
`docker.imagePassthrough`, registry connections, code configuration, and the
legacy prebuilt-image marker retain their no-ACR behavior. The rollout switch
does not restore the older provision/deploy mismatch.

New canonical binding creation is preview-only. Fresh automatic create with
the switch off returns normal provisioning outputs without a binding-only
project identity lookup, `acrBinding` config write, or new
`AZD_FOUNDRY_ACR_MODE` write. A previously saved create binding remains subject
to immutable identity validation and normal maintenance. Create ownership
recovered from deployment evidence remains available for safe provisioning
and teardown, but stays transient off: it is neither serialized as a new
binding nor unset as if a persisted binding existed. Ownership evidence,
custom resource identities, and safe cleanup checks are not disabled.

Legacy existing-project `reuse-connect` environments without canonical reuse
bindings or pending operations remain supported without the switch. Binding
validation and the canonical reuse/pending gate run before that exemption;
adding `USE_EXISTING_AI_PROJECT=true` or an endpoint cannot bypass them.
Greenfield `reuse-connect` intent still requires the preview.

The supported paths are embedded and currently generated ejected Bicep using
`microsoft.foundry`. Manually changing the Foundry layer to core
`infra.provider: bicep` is unsupported: its preview skips extension hooks,
and optional validation cannot enforce a mandatory gate. Such native-provider
runs and arbitrary direct external IaC execution are outside this contract.

### Retained RBAC deployment warnings

The listener reads `RBACWarnings` during host configuration, before opening event
streams. With the switch off, it registers `predeploy` with the stable
`WithServiceEventHandler` API. With the switch on, it registers that handler
only with `WithBetaServiceEventHandler` so the host can collect structured
warnings in final output and JSON `messages`. There is no beta event
subscription while the switch is off, and no duplicate stable subscription
while it is on. `postdeploy` and project listeners remain stable.

Both paths share the existing predeploy validation, telemetry, environment
updates, RBAC checks, automatic assignment, and skip rules. The stable path
prints the returned safe warning messages and suggestions in temporary hook
output. The host can discard that output while deployment progress suppresses
the previewer; the stable path does not guarantee warning visibility or
retention after deployment. Host output handling is unchanged. The beta path
returns messages to the host without also printing them.
Both preserve handler errors. This switch gates retention, not deployment or
RBAC checking, so a disabled switch does not reject ordinary deployment.

### BYO AKS initialization, adoption, and provisioning

The BYO AKS identity and environment key are declared in the aligned synthesis copies. Agents uses the existing `Require` evaluator for project init/adoption. Project classification resolves inline and `$ref` configuration using the existing service resolver. A service `env:` entry does not control this switch.

An absent variable disables BYO AKS use, while an explicit value uses `strconv.ParseBool`. Disabled checks return the `feature_not_enabled` compatibility error and shell/CI suggestion. Foundry provisioning without effective AKS hosting does not require this switch.

The Projects extension owns `microsoft.foundry`. Its `Deploy` and `Preview` methods call `prepareBYO`, which validates the effective `agentHosting` value after parameter merging, including template defaults and case-insensitive parameter names with value envelopes. When effective `enabled=true`, it checks the current process value of the shared synthesis environment key with `strconv.ParseBool` after local resource-ID validation and before credential initialization or BYO AKS, role, or identity reads. This covers embedded provisioning, on-disk `.parameters.json`, `.bicepparam`, and template defaults, including hosting enabled without an `azure.yaml` declaration. Effective `enabled=false` skips the gate and binding reads. Structured gate errors are returned unchanged.

Provider initialization, infrastructure state and parameter queries, synthesis, lifecycle hooks, and Bicep eject do not check the BYO AKS switch. BYO AKS infrastructure uses the standard Foundry `azd down` path without requiring the switch, including its confirmation, `--force` behavior, and target resource-group deletion.

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

Local A2A transport is not preview-gated; the same wire-version rules apply.
A2A-only `run` targets open no browser client. Their invocation guidance includes
`--a2a-version 1.0` only when the preview is enabled.

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

All features are disabled when their variables are absent. When a variable is explicitly set, only `strconv.ParseBool` true values enable a feature: `1`, `t`, `T`, `true`, `TRUE`, or `True`. False values (`0`, `f`, `F`, `false`, `FALSE`, `False`), empty values, and invalid values disable it without an error, including BYO AKS. Values such as `on`, `yes`, mixed-case `TrUe`, and whitespace-padded strings are invalid.

Each feature is independent. There is no global toggle, experimentation default, or cross-feature opt-in.

### Evaluator source boundary

`IsEnabled` reads only the declared key from the current process environment with `os.LookupEnv`, using `DefaultEnabled` only when the key is absent. Set switches in the shell or CI before running azd, or through scenario YAML `env:`. The extension inherits those process values at startup.

The azd host listen/extension startup path does not flatten `.azure/<env>/.env` into the extension process for these switches. It may set `AZD_ENVIRONMENT` to the selected environment name, but that does not load its dotenv values. Persisting `AZD_ALPHA_ENABLE_*` with `azd env set` therefore does not enable or override these switches for the listener. Direct `azd ai agent ...` command launches still append selected-environment values after shell/CI values, so persisted dotenv values can override shell opt-ins there. This boundary is the same for Voice, A2A, RBAC warnings, and the other Agents preview switches.

The evaluator does not query azd user config (including `alpha.all`), open selected or default environment files, or read project/service environment mappings. It does not filter process values by where they came from.

There is no cache, output, process-environment mutation, configuration write, authentication, or network call during evaluation. Each evaluation reads the current process value. A declaration error returns `false, error`. Valid declarations always return the process-environment decision; there is no host-read failure path.

Normal project configuration, deployment environments, runtime settings, and command input validation elsewhere in the extension are unchanged. Init retains the SDK-selected environment name when creating a project or environment, but gates and discovery do not load those scopes.

## Central enforcement

**Command layer.** Commands and flags carry annotations that name a registered feature. The root `PersistentPreRunE` calls `enforcePreviewFeatures` for the executing command with its args. Whenever the gate would reject, it first runs the command's local argument checks from `validateBeforePreview` so local conflicts are reported before `feature_not_enabled`. These checks use only flags and args, with no source reads or state changes. Existing debug logging and telemetry keep their order.

| Annotation | Placed on | Effect |
| --- | --- | --- |
| `previewFlag(cmd, name, feature)` | `invoke --long-running`, `invoke --no-wait`, `invoke --a2a-version`, `init --voice`, `init --acr-resource-id` | Setting the flag requires the feature (a bool only when true). The flag is hidden while the feature is off |
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

Read, show, endpoint discovery, invocation show/follow/cancel, delete, and down
retain access when agent-kind opt-ins are disabled. BYO AKS infrastructure
state and parameter queries, Bicep eject, and teardown do not require its
switch; teardown follows the standard Foundry
`azd down` path and deletes the target resource group with all resources inside
it. **The shared ACR preview
is stricter:** down and recovery for a new reuse binding also require its
switch, as described above. There is no state migration or automatic
enablement. Agents-private kind checks do not intercept evaluations or routines
sending generic requests to existing agents. Those extensions cannot import
this package; only the explicitly shared Foundry ACR policy spans agents and
projects.

## Compatibility and future framework adoption

This intentionally adopts the process-only mechanism in place of the earlier multi-source implementation:

- `AZD_FOUNDRY_EXT_ENABLE_*` names are no longer recognized; there is no legacy alias.
- The evaluator no longer reads user config or opens selected/default azd environment files. The extension inherits shell/CI process values at launch. Lifecycle listeners do not receive selected-environment dotenv flattening for preview switches, while direct command launches still append selected-environment values after shell/CI values.
- Feature evaluation no longer loads project/service scopes or calls the host.
- Old context/client/environment parameters, host-error sentinels, and flag-specific scope validation are removed.

Explicit disabled-preview inputs are rejected rather than silently dropped. The canonical syntax matches core azd alpha environment switches, but these capabilities are not registered with core alpha features. Core alpha behavior is unchanged. Do not use core `FeatureManager` here: it would also read azd config.

Discoverability through `azd config list-alpha`, a public SDK API, and framework/RPC support remain deferred. A future framework implementation can replace the private evaluator while retaining canonical variables and capability identities; broader source semantics would be an explicit change.

## Validation

Evaluator tests cover canonical keys, feature defaults, accepted boolean forms, non-enabling values, all independent combinations, ignored old/global/unrelated keys, optional descriptions, fresh reads, and no input mutation. Temporary local fixtures verify that direct calls do not load configuration or dotenv files. These tests do not launch through the host.

Feature gate tests verify that enabled previews are announced once when used and disabled previews remain silent. Root-command regressions cover parsed flags, local-validation precedence, independent gates, ignored persisted config, no host reads for evaluation/help, fresh and existing init scopes, preserved SDK-selected creation names, disabled no-auth/no-write rejection, protocol pickers, staged manifests, local invocation, fallback error propagation, and retained read access. Project tests cover inline and referenced definitions, harness whitespace normalization, and lifecycle entry points. Help tests preserve the always-visible switch list while checking conditional command discovery; snapshots cover enabled and disabled states.

Remote-template regressions cover inline and `$ref` previews, rejection before Azure authentication, retained authentication when enabled, single-download adoption, and temporary-directory cleanup on failure.

BYO AKS regressions cover default-off and boolean-value semantics and inline and referenced project configuration during init/adoption. Projects provisioning tests cover process-only enablement, rejection before credentials or BYO resource reads, unchanged structured errors and parameters, and the effective embedded, on-disk JSON, Bicep parameter, and template-default paths for both provision and preview. Disabled effective hosting and ordinary Foundry provisioning remain available without opt-in. Infrastructure tests also cover Bicep eject, Terraform rejection without file changes, standard Project and registry outputs, and the ARM what-if path.

RBAC listener regressions exercise stable-only hosts with the switch absent,
false, empty, or invalid, and beta hosts with true and `1` values. They check
exclusive registration, per-service warning and suggestion delivery, preserved
validation failures, and non-disclosure of transport credentials. The opt-in
`tier2/2.17b-deploy-rbac-warnings.yaml` scenario enables the switch through scenario process `env:` for its
readable and JSON retention checks.

Run offline tests, the full agents module build, and scoped static checks with Go 1.26.4. Tier 0 scenarios cover switch discovery and enabled/disabled help. Scenario execution remains opt-in through the `foundry-extension-scenario-orchestrator` agent; no Azure scenarios are required for this integration.
