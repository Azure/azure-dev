# Agents extension preview feature flags

<!-- cspell:ignore featureflags strconv azureaiagent -->

## Status and goal

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Scope:** Internal process-environment opt-in mechanism, a small feature registry, and root command help. No command adoption or new runtime gates.

Keep preview opt-in decisions inside the Microsoft Foundry agents extension until the extension framework supports discovery and evaluation. Use the canonical `AZD_ALPHA_ENABLE_<NAME>` syntax now so a future framework implementation can honor the same variables.

The adoption scope remains **managed harness agents**, **voice agents**, and **long-running agent invocation**. Regular hosted agents and plain prompt functionality are outside those scopes. Declaring these switches and documenting them does not enforce them; actual command and runtime checks belong to a separate adoption change.

## Ownership and code organization

```text
azure.ai.agents/
├── internal/
│   ├── cmd/                       # Help and future command callers
│   ├── project/                   # Future project/service callers
│   └── featureflags/
│       ├── featureflags.go        # Declaration validation and ENV read
│       ├── featureflags_test.go   # Process-only contract tests
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
func All() []Feature
func Lookup(id string) (Feature, bool)
```

`ID` is a capability identity, not a user-config contract. `EnvVar` is the exact process environment key to read. `Description` is optional for evaluation and supplies a short scope summary in help.

```go
enabled, err := featureflags.IsEnabled(featureflags.Voice)
if err != nil {
    return err
}
```

`IsEnabled` rejects an empty `ID` or `EnvVar` before reading the process environment. It does not require a context, azd client, selected environment, project, or login. Command input validation stays with the commands; there is no flag-specific `CheckEnvironmentFlag` helper.

## Feature registry and help

`registry.go` declares each feature once. `All` returns a fresh slice in stable order, and `Lookup` finds a declaration by ID. Root help at `azd ai agent --help` renders the canonical switches and their scopes from these declarations while retaining the ordinary project and environment guidance.

| Declaration | Feature ID | Process environment key | Scope |
| --- | --- | --- | --- |
| `ManagedHarness` | `agents.managed_harness` | `AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS` | Prompt agents that run on a Copilot harness |
| `Voice` | `agents.voice` | `AZD_ALPHA_ENABLE_AGENTS_VOICE` | Voice, prompt-voice, and hosted voice agents |
| `LongRunning` | `agents.long_running` | `AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING` | Long-running Responses invocations |

Each ID starts with `agents.`. Its canonical key is `AZD_ALPHA_ENABLE_` plus the ID in upper case with dots replaced by underscores. Registry tests enforce the identities, naming rule, unique IDs and keys, and nonempty descriptions. The evaluator reads the declared `EnvVar` rather than deriving a key at run time.

## Process-only enablement

We recommend setting the desired variable in the shell or CI process environment before running azd. For example:

```sh
export AZD_ALPHA_ENABLE_AGENTS_VOICE=true
azd ai agent --help
```

In PowerShell:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_VOICE = 'true'
azd ai agent --help
```

This example demonstrates the switch syntax and where to discover it; it does not demonstrate an implemented voice runtime gate.

All three features default to off. Only `strconv.ParseBool` true values enable a feature: `1`, `t`, `T`, `true`, `TRUE`, or `True`. False values (`0`, `f`, `F`, `false`, `FALSE`, `False`), missing or empty values, and invalid values leave it off without an error. Values such as `on`, `yes`, mixed-case `TrUe`, and whitespace-padded strings are invalid.

Each feature is independent. There is no global toggle, experimentation default, or cross-feature opt-in.

### Evaluator source boundary

The evaluator reads the extension's own process environment. It does not query azd user config (including `alpha.all`), load selected or default azd environment files, or read project/service environment mappings itself.

Values that the azd host has already injected into the extension's process environment are still read, regardless of their original source. This boundary does not guarantee that the full azd launch path excludes dotenv values or that `--environment` cannot influence them. The evaluator does not filter the origin of process values.

Normal project configuration, deployment environments, runtime settings, and command input validation elsewhere in the extension are unchanged.

## Compatibility and future framework adoption

This intentionally narrows the earlier multi-source implementation:

- `AZD_FOUNDRY_EXT_ENABLE_*` names are no longer recognized; there is no legacy alias.
- The evaluator no longer explicitly reads user config or loads selected/default azd environment files.
- Feature evaluation no longer loads project/service scopes or calls the azd host.
- The old context/client/environment parameters, host-error sentinels, and flag-specific environment validation are removed.

The canonical syntax matches core azd alpha environment switches, but these three extension capabilities are not registered with core alpha features. Core alpha behavior is unchanged. Do not use core `FeatureManager` here: it would also read azd config.

Discoverability through `azd config list-alpha`, a public SDK API, and framework/RPC support remain deferred. Root extension help is the discovery surface for now. A future framework implementation can replace the private evaluator while retaining the canonical variables and capability identities; it should introduce any broader source semantics explicitly.

## Adoption boundary and side effects

Only the three registered capabilities are candidates for future checks. Command discovery and runtime enforcement are separate caller responsibilities. A discovery check alone cannot gate preview flags, project/service flows, or direct invocation paths. Adoption should check the relevant capability before side effects without gating regular hosted or plain prompt agents.

There is no cache, output, process-environment mutation, configuration write, authentication, or network call. Each evaluation reads the current process value. A declaration error returns `false, error`; valid declarations always return the process-only decision without a host-read failure path.

## Validation

Tests cover all three canonical keys, default-off behavior, accepted boolean forms, non-enabling values, independent feature combinations, ignored old/global/unrelated keys, and optional descriptions. Temporary local fixtures verify that direct evaluator calls do not load or modify user config, default/selected dotenv files, or project/service mappings. These tests do not exercise the host's extension launch path or rule out values it injects into the process environment. Repeated calls verify fresh reads and no process or declaration mutation.

Registry tests cover the stable copy returned by `All` and `Lookup`. Help tests cover registry-derived switches and scopes, color/no-color headings, root-only placement, unchanged command discovery, and updated snapshots.

Run focused feature-flag and help tests, scoped vet/lint/formatting/modernization/spelling checks, and the full agents module build with Go 1.26.4. The Tier 0 `tests/cli-interactive-tester-scenarios/tier0/0.02-help-root.yaml` scenario includes the switch discovery contract; scenario execution is opt-in through the `foundry-extension-scenario-orchestrator` agent. No Azure scenarios or user auth/config mutations are required for this mechanism-only change.
