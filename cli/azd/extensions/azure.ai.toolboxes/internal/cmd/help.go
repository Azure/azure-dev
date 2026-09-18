// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const toolboxHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select one, or --environment <name> (-e)
  for a command. Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, commands
  resolve FOUNDRY_PROJECT_ENDPOINT (or legacy AZURE_AI_PROJECT_ENDPOINT)
  from the active azd environment, then the global default saved by
  'azd ai project set <endpoint>', then the same shell variables.

  Creating a toolbox records TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT in the
  active azd environment when one is available, together with a matching
  TOOLBOX_<NORMALIZED_NAME>_PROJECT_ENDPOINT marker. The normalized name is
  uppercase with non-alphanumeric character runs replaced by underscores.
  The MCP endpoint is the runtime address agents use to access the toolbox;
  the project endpoint identifies the Foundry project that owns it.

  'azd ai toolbox add' only edits a local toolbox definition. To manage a
  toolbox through deployment, declare its definition inline in an
  azure.ai.toolbox service in azure.yaml and run 'azd deploy <service>'.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
