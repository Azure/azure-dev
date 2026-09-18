// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const skillHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select one, or --environment <name> (-e)
  for a command. Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, commands
  resolve FOUNDRY_PROJECT_ENDPOINT (or legacy AZURE_AI_PROJECT_ENDPOINT)
  from the active azd environment, then the global default saved by
  'azd ai project set <endpoint>', then the same shell variables.
  Legacy skills and agents global context keys remain fallback sources.
  Skill commands read project context but do not change the global default.

  --file reads skill content at invocation time; it is not a tracked
  manifest. Download extracts to ./.agents/skills/<name>/ by default.
  Edit the downloaded files and use 'azd ai skill update <name> --file <path>'
  to create a new immutable version and make it the default.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
