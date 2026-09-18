// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const projectHelpFooter = `Environments & Environment Variables:
  An azd environment is a named deployment configuration, such as dev or prod.
  Its values are stored locally in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to select it.
  Use --environment <name> (-e) to select an environment for a command.

  'azd ai project show' resolves FOUNDRY_PROJECT_ENDPOINT in the active
  azd environment, then the default saved by 'azd ai project set <endpoint>'
  in azd global config, then the
  FOUNDRY_PROJECT_ENDPOINT shell variable. The global default is separate
  from environment values; 'azd ai project unset' only clears that default.

  'azd ai project add' configures azure.yaml and environment values for
  provisioning. An existing project records FOUNDRY_PROJECT_ENDPOINT.
  With --project-id, it also records AZURE_AI_PROJECT_ID, the project's
  ARM resource ID. An endpoint-only reference does not record an ARM ID.
  Use 'azd env set <name> <value>' to save a value and 'azd env get-values'
  to inspect values. They may contain secrets; do not share the output or
  commit .azure to source control.

Learn More:
  Environment commands: 'azd env --help'
  Managed model deployments: 'azd ai project deployment --help'`
