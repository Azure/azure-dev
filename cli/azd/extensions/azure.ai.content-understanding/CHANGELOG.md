# Release History

## 0.1.1-preview (Unreleased)

### Breaking Changes

- Remove `CU_AUTH_MODE`; existing process or saved-environment values are ignored.
  Use process `CU_API_KEY` to opt into key authentication, or unset it to use
  Entra ID.

### Features Added

- Accept `infra generate --subscription` with a subscription name or ID, overriding
  azd defaults without prompting. Reject missing or ambiguous matches instead of
  falling back to another subscription.
- Add an interactive infrastructure generation wizard covering subscription,
  environment, region, resource naming, and access permissions. Explain the
  workflow before subscription selection, then summarize generated files and
  deployment steps. Explicit inputs take precedence; `--no-prompt` skips prompts.
  Show a readable generation summary by default and a structured result with
  `--output json`.
- Select supported models in a single picker with recommended selections,
  validation feedback, and confirmation before deployment. Retain selections
  when correcting conflicting model versions.
- Support optional CU data-plane API-key authentication through process
  `CU_API_KEY` only, without saving keys in azd configuration or requiring
  azd sign-in. Keep Entra ID as the default and for provisioning and
  cross-resource copy.
- List all supported process environment variables, accepted values, defaults,
  scopes, and precedence in `azd ai cu env-var` / `env-var --help`.
  Use `env-var list` to inspect set values with secrets redacted.

### Bugs Fixed

- Fix postprovision authentication for guest users by resolving the user-access
  tenant through azd.
- Report unset model defaults as incomplete readiness in `doctor`, rather than
  a generic invalid-request failure, including when the service wraps the
  defaults error in a broader error code.

### Other Changes

- Prefix saved provisioning inputs with `CU_`. Regeneration migrates older
  `FOUNDRY_*` and `AZD_ASSIGN_ROLES` inputs per environment while preserving
  existing new-key values; older projects continue to work without regeneration.
- Save postprovision model-selection and configuration choices in per-environment
  azd configuration instead of dedicated environment values. Continue to read
  older saved values when the new settings are absent.
- Prefix Bicep deployment outputs with `_CU_PROVISIONED_` to distinguish them
  from runtime settings. Postprovision saves runtime `CU_ENDPOINT` separately.
  Existing generated projects and their saved output names remain supported.
- Follow azd's yellow warning and red error conventions for human-readable
  diagnostics and bright-blue highlights for follow-up actions, with gray inline
  comments beside next-step commands. Separate interactive input, results, and
  next steps with blank lines. Honor `NO_COLOR` and keep JSON/none diagnostics
  uncolored.
- Include safe service codes and per-input analysis failure reasons, with
  actionable guidance for missing analyzers and conflicting analyzer IDs.
- Locate offline schema validation errors by line and column without exposing
  authored field names or rejected values.

## 0.1.0-preview (2026-10-27)

- Initial preview of Azure Content Understanding commands under `azd ai cu`,
  using Azure Developer CLI sign-in and environments.
- Analyze documents and media from local files or URLs, including batch
  processing and saved results. Create, manage, validate, and test custom
  analyzers.
- Save global CU defaults, configure resource model mappings, check setup
  readiness, and generate an azd/Bicep project for resource provisioning and
  model setup.
