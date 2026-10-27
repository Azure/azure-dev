# Release History

## 0.1.1-preview (Unreleased)

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
