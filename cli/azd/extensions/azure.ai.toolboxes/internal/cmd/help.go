// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const toolboxHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select the environment for endpoint lookup.
  --environment (-e) does not override that persisted selection; use
  --project-endpoint <endpoint> to override endpoint discovery for remote
  toolbox operations (not local add or extension version).
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
  'azd ai project set <endpoint>', then the same shell keys in that order.

  Creating a toolbox records TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT in the
  active azd environment when one is available, together with a matching
  TOOLBOX_<NORMALIZED_NAME>_PROJECT_ENDPOINT marker. The normalized name is
  uppercase with non-alphanumeric character runs replaced by underscores.
  The MCP endpoint is the runtime address agents use to access the toolbox;
  the project endpoint identifies the Foundry project that owns it.

  'azd ai toolbox add' only edits a local toolbox definition. To manage a
  toolbox through deployment, declare its definition inline in an
  azure.ai.toolbox service in azure.yaml and run 'azd deploy <service>'.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
