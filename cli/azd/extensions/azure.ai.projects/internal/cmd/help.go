// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const projectHelpFooter = `Environments & Environment Variables:
  An azd environment is a named deployment configuration, such as dev or prod.
  Its values are stored locally in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to select it.
  --environment <name> (-e) selects the environment for project add and
  deployment add. Project show instead reads the persisted selection from
  'azd env select <name>'; --environment does not override its endpoint lookup.

  With an available azd environment, 'azd ai project show' looks up
  FOUNDRY_PROJECT_ENDPOINT: its persisted value wins, or the azd host's shell
  value is used if the key is absent from .env. An explicitly empty persisted
  value prevents this shell fallback at this stage.
  If this lookup yields no endpoint (including when no environment is
  available), show tries the default saved by 'azd ai project set <endpoint>'
  in azd global config, then the FOUNDRY_PROJECT_ENDPOINT shell variable.
  Thus, with an active environment, a shell value can win before global
  config when the persisted key is absent. The global default is separate
  from environment values; 'azd ai project unset' only clears that default.

  'azd ai project add' configures azure.yaml and environment values for
  provisioning. An existing project records FOUNDRY_PROJECT_ENDPOINT.
  With --project-id, it also records AZURE_AI_PROJECT_ID, the project's
  ARM resource ID. An endpoint-only reference does not record an ARM ID.
  Use 'azd env set <key> <value>' to save a value and 'azd env get-values'
  to inspect values. They may contain secrets; do not share the output or
  commit .azure to source control.

Learn More:
  Environment commands: 'azd env --help'
  Managed model deployments: 'azd ai project deployment --help'`
