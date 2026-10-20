# Alpha Features

The Azure Developer CLI includes a `alpha feature toggle` which allows the cli to enable functionality in `alpha` mode (experimental). This document provides the feature design for it.

## Main requirement

As a customer, I can explicitly ask `azd` to unblock and show any _alpha_ functionality. The features becoming available are not finalized, so they can be changed or removed in future releases.

The strategy to ask `azd` to `toggle` between modes is:

- Enable _all_ experimental features: `azd config set alpha.all on`
- Enable _specific_ experimental feature: `azd config set alpha.featureName on`
- Disable _all_ experimental features: `azd config unset alpha.all` or `azd config set alpha.all off`
- Disable _specific_ experimental feature: `azd config unset alpha.featureName` or `azd config set alpha.featureName off`

In CI, the on/off mode can be configured using environment variables with the following scheme:

- `AZD_ALPHA_ENABLE_<name>`: where `<name>` is the upper-cased name of the feature, with dot `.` characters replaced by underscore `_` characters.

## Additive extension opt-ins

Extension features can reuse `resources/alpha_features.yaml`, the existing
`alpha.<feature-id>` user configuration, and `pkg/alpha` without changing core
`FeatureManager.IsEnabled` behavior. A declaration can optionally set `envVar`
to its canonical environment-variable name. Without it, additive evaluation
uses the existing dot and underscore `AZD_ALPHA_ENABLE_<ID>` names. An explicit
`envVar` is the only environment-variable name evaluated for that declaration.
This support does not register or enable any new production feature.

Foundry extension declarations use `AZD_FOUNDRY_EXT_ENABLE_<FEATURE>`:
extension-specific suffixes start with `AGENTS_` or `MODELS_`, and a shared
feature uses one common name. There is no Foundry `ALL` environment toggle or
implicit alias.

`alpha.GetFeature(id)` retrieves a declaration from the registry and reports
whether it exists. `Feature.IsEnabledWithSources(userConfig, envSources...)`
is a pure, additive evaluator over caller-provided snapshots:

- Environment values use the [standard boolean parser](https://pkg.go.dev/strconv#ParseBool);
  only its true values enable.
- User configuration enables with case-insensitive `on` at `alpha.<feature-id>`
  or `alpha.all`. No other value contributes an opt-in.
- A true signal in any source wins over false, off, empty, missing, or other
  non-enabling values in every other source. Pass each service map separately;
  do not merge them before evaluation.
- Process environment and experimentation defaults are not read implicitly.
  The existing core manager retains its environment false overrides and
  experimentation defaults.

An extension-side caller supplies the process environment, the selected azd
environment's own dotenv values, global user configuration, and the `env` map
from **every** service in the current `azure.yaml`, regardless of service host.
The existing `Environment.GetValues` RPC returns the dotenv snapshot;
`GetValue` and `LookupEnv` include process fallback and are not independent
source reads. Use the existing User Config and Project APIs for the other
snapshots. Propagate configuration, file, decoding, and transport errors;
only intentionally non-enabling flag values are treated as no signal.

After obtaining a registered `feature` and loading those snapshots:

```go
envSources := []map[string]string{processEnv, dotenv}
envSources = append(envSources, serviceEnvMaps...)
enabled := feature.IsEnabledWithSources(userConfig, envSources...)
```

Tests can construct `alpha.Feature{Id: id, EnvVar: envName}` directly without
adding a test-only entry to the production registry.

### Host launch requirement

The shared extension runner preserves true
`AZD_FOUNDRY_EXT_ENABLE_<FEATURE>` process values when project dotenv values
would otherwise replace them before extension startup. This applies to both
direct commands and lifecycle `listen` processes. It does not change merging
for ordinary variables, core Alpha flags, other `AZD_FOUNDRY_*` settings, or
`AZD_AGENT_RESUME_SESSION_ON_DEPLOY`. It neither mutates the host environment
nor caches project state.

The guarantee requires a **host** containing this runner change, not just a
new SDK in the extension binary. The release baseline is azd 1.37.0:
an adopting extension must declare `requiredAzdVersion: ">=1.37.0"` if this
change ships in that release, or the first containing version if shipped later.
Older hosts cannot recover a machine opt-in already overwritten at launch.
No additional gRPC service is required.

## Alpha feature

All features start as alpha features (e.g., experimental). In this phase, the goal is to receive sufficient usage to get meaningful feedback around the feature’s design, functionality and user experience.

### Definition

-	These features are under active development.
-	Hidden behind a feature flag which interested users must explicitly opt into (these flags should be documented in the help text/docs of the product for easy discovery).
-	There are no guarantees about the long-term stability or supportability of experimental features (may contain bugs or expose bugs if used).
-	No commitment by the team that the feature is something we plan to advance to preview or stable stage (it’s an experiment).
-	A reasonable outcome of an experiment is “it didn’t work” and we rip out the code.
-	Recommended for non-business-critical uses because of potential for incompatible changes in subsequent releases

### Advancement criteria (how to reach preview).

-	The feature has been properly spec’d (initial draft) and approved by PM/eng/design.
-	PM + engineering team has had a formal review meeting to sign off on feature advancement to next phase.
-	Feature is documented on DevHub + there is help text in product.
-	We’ve received signal that the UX is successful via sufficient user feedback (could just be via internal feedback; depends on feature).


## Announcing in-preview features

The Azure Developer CLI can list available experimental features running:

> azd config list-alpha

### Changelog notes

Customer can refer to the release notes to discover changes for the list of experiments.
