// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const routineHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select the environment for endpoint lookup.
  --environment (-e) does not override that persisted selection; use
  --project-endpoint <endpoint> to override endpoint discovery for remote
  routine operations. --project-endpoint and --timeout are not supported by
  local add, context, or version commands.
  Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, with an
  available azd environment, commands look up AZURE_AI_PROJECT_ENDPOINT:
  its persisted value wins, or the azd host's shell value is used if the key
  is absent from .env. An explicitly empty persisted value prevents this
  shell fallback at this stage.
  If this lookup yields no endpoint (including when no environment is
  available), commands try the global default saved by
  'azd ai project set <endpoint>', then legacy agent global context, then
  the FOUNDRY_PROJECT_ENDPOINT shell variable. A shell
  AZURE_AI_PROJECT_ENDPOINT can therefore win before global config and
  FOUNDRY_PROJECT_ENDPOINT when the persisted key is absent.
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
