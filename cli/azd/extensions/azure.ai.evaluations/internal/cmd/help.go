// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const evalHelpFooter = `Project Context:
  Commands that contact Foundry resolve the project endpoint in this order:
  --project-endpoint; the selected azd environment; the global configuration
  key extensions.ai-projects.context.endpoint; then process environment
  variables. Environment lookups prefer FOUNDRY_PROJECT_ENDPOINT and fall back
  to AZURE_AI_PROJECT_ENDPOINT.

Environments & Environment Variables:
  An azd environment is a named deployment configuration, such as dev or prod.
  Use 'azd env new <name>' to create one, 'azd env select <name>' to change the
  default, or --environment <name> (-e) to select one for a command.
  Use 'azd env set FOUNDRY_PROJECT_ENDPOINT <endpoint>' to save the endpoint.
  Values can include secrets; do not share 'azd env get-values' output or
  commit .azure to source control.

  Evaluation configuration is located using --path, then the project service
  declaration, then a recorded EVAL_CONFIG_PATH in the selected environment,
  and finally ./evals. APPLICATIONINSIGHTS_CONNECTION_STRING in that environment
  lets init and generate default to trace-based data sources.
  AZURE_AI_PROJECT_ID supplies the project resource ID for portal links.
  Reconciliation state is private environment configuration under eval.state,
  not user-set environment variables.

Learn More:
  Environment commands: 'azd env --help'
  Dataset commands: 'azd ai dataset --help'
  Run results: 'azd ai eval run output'`
