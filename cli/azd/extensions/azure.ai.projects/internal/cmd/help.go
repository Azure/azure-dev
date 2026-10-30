// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/spf13/cobra"
)

const projectACRHelp = "Use --new-project --acr-resource-id to reuse an external registry for\n" +
	"source-built agents with Bicep. Project-only setup saves the selection\n" +
	"until agents are added. Code, prebuilt-image, and non-container-only\n" +
	"projects cannot use the selector. A deployed registry binding cannot\n" +
	"be changed; repeating the same selection preserves the project.\n" +
	"The registry must be in the project's subscription and outside its\n" +
	"resource group. Cross-subscription reuse is not supported."

const projectACRExample = `  # Reuse an externally owned registry for source-built agents (Bicep only)
  azd ai project add --new-project --acr-resource-id "<registry-resource-id>"

`

const projectACRFooter = `  With --new-project, --acr-resource-id selects an externally owned registry
  for source-built agents (Bicep only). The selection is saved even before
  agents are added. Registry migration after provisioning is not supported.
  The registry must be in the project's subscription and outside its resource
  group. Cross-subscription reuse is not supported.
`

func applyProjectACRVisibility(cmd *cobra.Command) {
	if flag := cmd.Flags().Lookup("acr-resource-id"); flag != nil {
		flag.Hidden = !synthesis.ACRReuseEnabled()
		if flag.Hidden {
			cmd.Long = strings.ReplaceAll(cmd.Long, "\n\n"+projectACRHelp, "")
			cmd.Example = strings.ReplaceAll(cmd.Example, projectACRExample, "")
		}
	}
	for _, child := range cmd.Commands() {
		applyProjectACRVisibility(child)
	}
}

func projectHelpFooter() string {
	text := projectHelpFooterBase
	if !synthesis.ACRReuseEnabled() {
		text = strings.ReplaceAll(text, projectACRFooter, "")
	}
	return text
}

const projectHelpFooterBase = `Environments & Environment Variables:
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
` + projectACRFooter + `  Use 'azd env set <key> <value>' to save a value and 'azd env get-values'
  to inspect values. They may contain secrets; do not share the output or
  commit .azure to source control.

Preview Features:
  Existing ACR reuse is disabled by default.
  Set AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE=true in the process running azd.
  Re-enable it for every reuse operation, including preview, down, and eject.

Learn More:
  Environment commands: 'azd env --help'
  Managed model deployments: 'azd ai project deployment --help'`
