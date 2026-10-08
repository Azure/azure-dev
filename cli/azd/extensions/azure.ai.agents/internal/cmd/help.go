// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const agentHelpFooter = `Environments & Environment Variables:
  An azd environment is a named deployment configuration, such as dev or prod.
  Its values are stored locally in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one, 'azd env select <name>' to change the
  default, or --environment <name> (-e) to select one for a command.

  Use 'azd env set <name> <value>' to save a value and 'azd env get-values' to
  inspect the selected environment. Values can include secrets; do not share
  the output or commit .azure to source control.

  FOUNDRY_PROJECT_ENDPOINT identifies the Foundry project. Init records it
  when you select an existing project; generated azure.yaml files reference
  it as ${FOUNDRY_PROJECT_ENDPOINT} to keep configuration portable.
  Deploy records AGENT_<SERVICE>_NAME, AGENT_<SERVICE>_VERSION and
  AGENT_<SERVICE>_PROJECT_ENDPOINT so commands can resolve deployed agents.
  <SERVICE> is the service name in uppercase with spaces and hyphens replaced
  by underscores (for example, my-agent becomes MY_AGENT).

  For hosted agent runtime settings, use the service-level env mapping in
  azure.yaml. Reference environment values with ${VARIABLE_NAME}; setting an
  azd environment value alone does not forward every value to the agent.
  Use 'azd ai agent run' for local development and 'azd deploy' to apply changes
  to the deployed agent.

Learn More:
  Agent documentation: https://aka.ms/azd-ai-agent-docs
  Environment commands: 'azd env --help'`
