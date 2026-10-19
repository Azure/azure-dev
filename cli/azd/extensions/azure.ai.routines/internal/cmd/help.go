// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const routineHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select the environment for endpoint lookup.
  --environment (-e) does not override that persisted selection; use
  --project-endpoint <endpoint> to override endpoint discovery for remote
  routine operations. --project-endpoint, --tenant-id, and --timeout are
  only supported by remote routine commands. Local commands such as add,
  context, and version reject these flags.
  Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, with an
  available azd environment, commands look up FOUNDRY_PROJECT_ENDPOINT
  first, then AZURE_AI_PROJECT_ENDPOINT as a legacy fallback only after a
  successful empty canonical lookup. For each key, the persisted value
  wins, or the azd host's shell value is used if the key is absent from .env.
  An explicitly empty persisted value prevents this shell fallback at this
  stage.
  A failed endpoint lookup stops the operation instead of choosing another
  project. If both lookups succeed but return empty values, or no
  environment is available, commands try the global default saved by
  'azd ai project set <endpoint>', then legacy agent global context, then
  the FOUNDRY_PROJECT_ENDPOINT shell variable.
  Use 'azd env set FOUNDRY_PROJECT_ENDPOINT <endpoint>' to persist the
  endpoint this extension reads from the active environment. The
  AZURE_AI_PROJECT_ENDPOINT environment key remains supported as a legacy
  fallback.

  'azd ai routine add' only updates an azure.ai.routine service in
  azure.yaml. Run 'azd deploy <name>' or 'azd up' to apply that service.
  'azd ai routine create' instead creates a routine immediately.
  Routine execution is asynchronous; use 'azd ai routine run list <name>'
  to inspect execution history after dispatch.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
