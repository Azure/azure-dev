// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const connectionHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select one, or --environment <name> (-e)
  for a command. Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, commands
  resolve FOUNDRY_PROJECT_ENDPOINT (or legacy AZURE_AI_PROJECT_ENDPOINT)
  from the active azd environment, then the global default saved by
  'azd ai project set <endpoint>', then the same shell variables.

  Connections also need the project's ARM resource ID. Save
  AZURE_AI_PROJECT_ID in the selected azd environment with
  'azd env set AZURE_AI_PROJECT_ID <project-resource-id>'.
  The resource ID must match the resolved endpoint. It is required to
  create the first connection when no existing connection can supply
  the project's subscription and resource group.

  Connection credentials are separate from azd environment values.
  'azd ai connection show <name> --show-credentials' displays secret values;
  do not share that output.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
