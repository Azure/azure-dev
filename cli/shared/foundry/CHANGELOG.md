# Release History

## 0.2.0 (Unreleased)

### Features Added

- Add `policy` package for shared Foundry Azure Policy inventory loading, caching, candidate/model/tool/image/agent-kind evaluation, Foundry Govern portal deep links, and runtime data-plane policy error classification.
- Add Azure Policy validation error codes to `ferrors` (`CodeAgentBlockedByPolicy`, `CodeContainerImageBlockedByPolicy`, `CodeModelBlockedByPolicy`, `CodeToolBlockedByPolicy`, `CodeToolboxBlockedByPolicy`).

## 0.1.0 (2026-11-11)

### Features Added

- Add `access` package for ARM-first Foundry project data-plane access preparation, role equivalence checks, idempotent role assignment, and bounded propagation/sync polling.
- Add `ferrors` package for portable typed errors across Foundry extensions.
