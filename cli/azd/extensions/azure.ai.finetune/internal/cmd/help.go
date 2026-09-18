// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const finetuningHelpFooter = `Environments & Environment Variables:
  An azd environment is a named configuration stored in .azure/<environment>/.env.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to switch.
  Use 'azd env set <name> <value>' to save values and 'azd env get-values' to
  inspect them. Values can contain secrets; do not share the output or commit
  .azure to source control.

  Init saves AZURE_TENANT_ID, AZURE_SUBSCRIPTION_ID, AZURE_RESOURCE_GROUP_NAME,
  AZURE_ACCOUNT_NAME, AZURE_PROJECT_NAME and AZURE_LOCATION for the Foundry
  project. Job commands use the current azd environment; --project-endpoint
  and --subscription support initialization when it is not configured.
  'azd ai finetuning jobs deploy' uses the saved subscription, resource group,
  account and tenant to deploy a fine-tuned model.

Learn More:
  Environment commands: 'azd env --help'
  Fine-tuning job commands: 'azd ai finetuning jobs --help'`
