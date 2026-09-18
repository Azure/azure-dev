// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const modelsHelpFooter = `Environments & Environment Variables:
  An azd environment is a named configuration stored in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to switch.
  Use 'azd env set <name> <value>' to save values and 'azd env get-values' to
  inspect them. Values can contain secrets; do not share the output or commit
  .azure to source control.

  Init saves AZURE_PROJECT_ENDPOINT and the Foundry project's tenant,
  subscription, resource group, account, project name and location.
  Model commands prefer --project-endpoint, then AZURE_PROJECT_ENDPOINT from
  the current azd environment. If no endpoint is stored, AZURE_ACCOUNT_NAME
  and AZURE_PROJECT_NAME are used to construct it. AZURE_SUBSCRIPTION_ID
  supplies the subscription when no explicit subscription was provided.
  Without saved context, commands prompt to select an existing project.

Learn More:
  Environment commands: 'azd env --help'
  Upload and registration: 'azd ai models create --help'`
