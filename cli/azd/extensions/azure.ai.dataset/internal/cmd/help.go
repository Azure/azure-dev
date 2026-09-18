// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const datasetHelpFooter = `Project Context:
  Commands that contact Foundry resolve the project endpoint in this order:
  --project-endpoint; the selected azd environment; the global configuration
  key extensions.ai-projects.context.endpoint; then process environment
  variables. Environment lookups prefer FOUNDRY_PROJECT_ENDPOINT and fall back
  to AZURE_AI_PROJECT_ENDPOINT.
  Dataset commands can run without an azd project when an endpoint is supplied.

Environments & Environment Variables:
  An azd environment is a named deployment configuration, such as dev or prod.
  Use 'azd env new <name>' to create one, 'azd env select <name>' to change the
  default, or --environment <name> (-e) to select one for a command.
  Use 'azd env set FOUNDRY_PROJECT_ENDPOINT <endpoint>' to save the endpoint.
  Values can include secrets; do not share 'azd env get-values' output or
  commit .azure to source control.

  Create and update record EVAL_DATASET_VERSION in the selected environment
  after publishing. AZURE_AI_PROJECT_ID supplies the project resource ID for
  portal links. Dataset generation is provided by the evaluations extension,
  not by this dataset-management extension.

Learn More:
  Environment commands: 'azd env --help'
  Dataset generation: see the azure.ai.evaluations extension documentation.`
