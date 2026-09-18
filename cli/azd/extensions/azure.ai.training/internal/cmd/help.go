// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const trainingHelpFooter = `Environments & Environment Variables:
  An azd environment is a named configuration stored in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to switch.
  Use 'azd env set <name> <value>' to save values and 'azd env get-values' to
  inspect them. Values can contain secrets; do not share the output or commit
  .azure to source control.

  Init saves AZURE_TENANT_ID, AZURE_SUBSCRIPTION_ID, AZURE_RESOURCE_GROUP_NAME,
  AZURE_LOCATION, AZURE_ACCOUNT_NAME and AZURE_PROJECT_NAME for the selected
  Foundry project. Job commands use that project context; --project-endpoint
  and --subscription can select a project during implicit initialization.
  AZURE_AI_TRAINING_HAS_UAMI records whether the project has a user-assigned
  managed identity. Training job submission requires that identity.
  'azd ai training job validate' validates YAML offline without Azure setup.

Learn More:
  Environment commands: 'azd env --help'
  Training job commands: 'azd ai training job --help'`
