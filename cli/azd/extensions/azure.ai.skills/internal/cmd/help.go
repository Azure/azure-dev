// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const skillHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select the environment for endpoint lookup.
  --environment (-e) does not override that persisted selection; use
  --project-endpoint <endpoint> to override endpoint discovery for skill
  operations (not context or version).
  Values may contain secrets; do not share the output of
  'azd env get-values' or commit .azure to source control.

  --project-endpoint overrides endpoint discovery. Otherwise, with an
  available azd environment, lookup checks FOUNDRY_PROJECT_ENDPOINT and
  then legacy AZURE_AI_PROJECT_ENDPOINT. Each key uses its persisted value,
  or the azd host's shell value if the key is absent from .env.
  An explicitly empty persisted value skips that key without a shell fallback
  at this stage. A non-empty shell value can therefore win before global
  config, and a canonical shell value can win before a persisted legacy key.
  If this lookup yields no endpoint (including when no environment is
  available), commands try the global default saved by
  'azd ai project set <endpoint>', then legacy skills and agents global
  context, then the same shell keys in that order.
  Skill commands read project context but do not change the global default.

  --file reads skill content at invocation time; it is not a tracked
  manifest. Download extracts to ./.agents/skills/<name>/ by default.
  Edit the downloaded files and use 'azd ai skill update <name> --file <path>'
  to create a new immutable version and make it the default.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
