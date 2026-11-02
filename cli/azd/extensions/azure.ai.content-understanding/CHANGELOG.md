# Release History

## 0.1.1-preview (Unreleased)

- Accept `infra generate --subscription` with a subscription name or ID, overriding
  azd defaults without prompting. Reject missing or ambiguous matches instead of
  falling back to another subscription.
- Add an interactive infrastructure generation wizard covering subscription,
  environment, region, resource naming, and access permissions. Explain the
  workflow before subscription selection, then summarize generated files and
  deployment steps. Explicit inputs take precedence; `--no-prompt` skips prompts.
- Select supported models in a single picker with recommended selections,
  validation feedback, and confirmation before deployment. Retain selections
  when correcting conflicting model versions.
- Follow azd's yellow warning and red error conventions for human-readable
  diagnostics. Honor `NO_COLOR` and keep JSON/none diagnostics uncolored.
- Prevent generation for one azd environment from changing another environment's
  target resource. Save resource, model-selection, and configuration choices
  per environment; preserve effective resource choices when migrating older
  generated projects.
- Prefix Bicep deployment outputs with `_CU_PROVISIONED_` to distinguish them
  from runtime settings. Postprovision saves runtime `CU_ENDPOINT` separately.
  Existing generated projects and their saved output names remain supported.
- Fix postprovision authentication for guest users by resolving the user-access
  tenant through azd.
- Support optional CU data-plane API-key authentication through process
  `CU_API_KEY` only, without saving keys in azd configuration or requiring
  azd sign-in. Keep Entra ID as the default and for provisioning and
  cross-resource copy.
- Remove `CU_AUTH_MODE`; existing process or saved-environment values are ignored.
  Use process `CU_API_KEY` to opt into key authentication, or unset it to use
  Entra ID.
- List all supported process environment variables, accepted values, defaults,
  scopes, and precedence in `azd ai cu env-var` / `env-var --help`.
  Use `env-var list` to inspect set values with secrets redacted.
- Show a readable infrastructure generation summary by default and a structured
  result with `--output json`.
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
