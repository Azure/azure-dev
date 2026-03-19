# Alpha Features

Alpha features are experimental capabilities under active development. They are gated behind feature flags and carry no stability guarantees.

## Enabling Alpha Features

### Per-feature

```bash
azd config set alpha.<featureName> on
azd config set alpha.<featureName> off
```

### All alpha features

```bash
azd config set alpha.all on
```

### In CI/CD

Set an environment variable for each feature:

```bash
export AZD_ALPHA_ENABLE_<FEATURE_NAME>=true
```

## Discovering Available Features

```bash
azd config list-alpha
```

This lists all currently available experimental features and their enabled/disabled status.

## What to Expect

- **No stability guarantees** — APIs, behavior, and configuration may change without notice
- **Possible removal** — Features may be pulled entirely if the approach doesn't work out
- **Feedback welcome** — Alpha features exist to collect signal; report issues and share impressions

## Adding a New Alpha Feature

When implementing a new alpha feature:

1. Register the feature flag in the alpha features configuration
2. Guard all feature codepaths behind the flag check
3. Document the feature in `azd config list-alpha` output
4. Add a note in release changelog under the experiments section

For implementation details, see [cli/azd/docs/alpha-features.md](../../cli/azd/docs/alpha-features.md).
