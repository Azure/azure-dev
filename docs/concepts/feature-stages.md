# Feature Stages

azd uses a three-stage lifecycle to graduate features from experimental to fully supported.

## Stages

### Alpha

- Behind a feature flag — must be explicitly opted into
- Enable per-feature: `azd config set alpha.<name> on`
- Enable all: `azd config set alpha.all on`
- In CI: set `AZD_ALPHA_ENABLE_<NAME>=true`
- No stability guarantees — may be removed or redesigned
- Discoverable via `azd config list-alpha`

### Beta

- Available by default (no feature flag needed)
- Functional and supported, but may undergo breaking changes
- Actively collecting user feedback

### Stable

- Fully supported with backward compatibility guarantees
- Complete documentation on [aka.ms/azd](https://aka.ms/azd)
- Breaking changes follow a deprecation process

## Graduation Criteria

For a feature to move from alpha → beta:

1. Feature is spec'd and approved
2. Formal PM, engineering, and design review completed
3. Documented on DevHub and in CLI help text
4. Positive user feedback signal

For the current status of all features, see [Feature Status](../reference/feature-status.md).

For implementation details on the alpha feature flag system, see [cli/azd/docs/alpha-features.md](../../cli/azd/docs/alpha-features.md).
