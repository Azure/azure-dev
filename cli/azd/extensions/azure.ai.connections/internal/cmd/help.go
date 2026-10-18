// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

const connectionHelpFooter = `Environments & Environment Variables:
  An azd environment stores deployment values in .azure/<environment>/.env.
  Use 'azd env select <name>' to select the environment for endpoint lookup.
  Connection list, show, create, and update read that persisted selection;
  --environment (-e) does not override their endpoint lookup. Connection
  delete honors --environment. Use --project-endpoint <endpoint> to override
  endpoint discovery for connection operations (not context or version).
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

  Connections also need the project's ARM resource ID. Save
  AZURE_AI_PROJECT_ID in the selected azd environment with
  'azd env set AZURE_AI_PROJECT_ID <project-resource-id>'.
  The resource ID must match the resolved endpoint. It is required to
  create the first connection when no existing connection can supply
  the project's subscription and resource group.

  Connection credentials are separate from azd environment values.
  'azd ai connection show <name> --show-credentials' displays secret values;
  do not share that output.

Learn More:
  Project context: 'azd ai project --help'
  Environment commands: 'azd env --help'`
