// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const routineHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select one, or --environment <name> (-e)
  for a command. Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, routine
  commands resolve AZURE_AI_PROJECT_ENDPOINT from the active azd
  environment, then the global default saved by
  'azd ai project set <endpoint>', then the FOUNDRY_PROJECT_ENDPOINT
  shell variable. Legacy agent global context remains a fallback.
  Use 'azd env set AZURE_AI_PROJECT_ENDPOINT <endpoint>' to persist the
  endpoint this extension reads from the active environment.

  'azd ai routine add' only updates an azure.ai.routine service in
  azure.yaml. Run 'azd deploy <name>' or 'azd up' to apply that service.
  'azd ai routine create' instead creates a routine immediately.
  Routine execution is asynchronous; use 'azd ai routine run list <name>'
  to inspect execution history after dispatch.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
