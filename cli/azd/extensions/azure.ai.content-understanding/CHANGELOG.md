# Release History

## 0.1.1-preview (Unreleased)

- Accept `infra generate --subscription` with a subscription name or ID, overriding
  azd defaults without prompting. Reject missing or ambiguous matches instead of
  falling back to another subscription.
- Explain infrastructure generation and each interactive choice on screen,
  before subscription selection and without requiring `?` hints. Guide users through environment, region,
  resource naming, and access permissions, then explain the next steps.
- Follow azd's yellow warning and red error conventions for human-readable
  diagnostics, including provisioning guidance, prompt validation, schema
  suggestions, and doctor readiness. Preserve uncolored JSON/none diagnostics,
  structured error reporting, and `NO_COLOR` behavior.
- Keep resource choices in each azd environment and use shared Bicep parameter
  substitutions, preventing generation for one environment from retargeting another.
  Migrate older shared literal choices without changing other environments'
  effective resource parameters. Persist postprovision choices in azd environment
  config instead of dedicated environment variables. Prefix all Bicep deployment outputs with
  `_CU_PROVISIONED_` to distinguish them from runtime settings. Retain
  old-output fallbacks for existing generated projects and standard azd inputs.
  Save runtime `CU_ENDPOINT` through postprovision configuration, not Bicep.
- Show model-picker validation errors and unavailable recommendations directly
  in the prompt without requiring `?`, retaining selections when retrying.
- Resolve the postprovision credential's user-access tenant through azd rather
  than using deployment resource-tenant metadata.
- Support optional CU data-plane API-key authentication through process
  `CU_API_KEY` only, without saving keys in azd configuration or requiring
  azd sign-in. Keep Entra ID as the default and for provisioning and
  cross-resource copy.
- Remove `CU_AUTH_MODE`; existing process or saved-environment values are ignored.
  Use process `CU_API_KEY` to opt into key authentication, or unset it to use
  Entra ID.
- List all supported process environment variables, accepted values, defaults,
  scopes, and precedence in `azd ai cu env-var` / `env-var --help`.
  Keep `env-var list` for inspecting set values with secrets redacted.
- Add interactive environment, region, resource-name prefix, and role-assignment
  prompts to infrastructure generation. Explicit inputs take precedence;
  `--no-prompt` and `--output json` skip the new wizard.
- Show supported models together with recommended selections, allow multiple
  selections and correction of conflicting versions, and require confirmation
  before deploying interactively selected models.
- Show a readable infrastructure generation summary by default. Use
  `--output json` to retain the structured result.
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
