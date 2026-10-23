// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type environmentVariable struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
	Sensitive bool   `json:"sensitive"`
}

func newEnvVarCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	group := &cobra.Command{Use: "env-var", Short: "Inspect supported compatibility process variables safely"}
	list := &cobra.Command{
		Use: "list", Args: cobra.NoArgs, Short: "List currently set supported process variables with secrets redacted",
		Long: "List supported compatibility and inherited azd process variables that are currently set.\n" +
			"Does not read selected-environment values, host credentials, or unrelated process variables.\n" +
			"CU_API_KEY is always redacted and remains unsupported for authentication.\n" +
			"See the extension README for accepted values, defaults, and configuration precedence.",
		Example: `  azd ai cu env-var list
    Inspect supported process variables with sensitive values redacted.

  azd ai cu env-var list --output json
    Return the redacted variable list as JSON.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exterrors.FromError(writeServiceResult(cmd, extCtx.OutputFormat, listEnvironmentVariables(os.LookupEnv)))
		},
	}
	group.AddCommand(jsonCommand(list))
	return group
}

func listEnvironmentVariables(lookup func(string) (string, bool)) []environmentVariable {
	result := []environmentVariable{}
	for _, spec := range []environmentVariable{
		{Name: "CU_ENDPOINT", Scope: "service commands"},
		{Name: "CU_API_VERSION", Scope: "service commands"},
		{Name: "CU_DEFAULT_ANALYZER", Scope: "analyze"},
		{Name: "CU_AUTH_MODE", Scope: "authentication"},
		{Name: "CU_API_KEY", Scope: "unsupported key authentication", Sensitive: true},
		{Name: "CU_ON_EXISTS", Scope: "analyze"},
		{Name: "AZD_ENVIRONMENT", Scope: "azd environment selection"},
		{Name: "AZURE_TENANT_ID", Scope: "authentication"},
		{Name: "AZURE_SUBSCRIPTION_ID", Scope: "Azure resource scope"},
		{Name: "AZURE_RESOURCE_GROUP", Scope: "Azure resource scope"},
		{Name: "AZD_DEBUG", Scope: "azd diagnostics"},
		{Name: "AZD_NO_PROMPT", Scope: "azd prompts"},
		{Name: "AZD_CWD", Scope: "azd working directory"},
	} {
		value, present := lookup(spec.Name)
		if !present {
			continue
		}
		spec.Value = value
		if spec.Sensitive {
			spec.Value = "********"
		} else if spec.Name == "CU_ENDPOINT" {
			if _, err := cuclient.NormalizeEndpoint(value); err != nil {
				spec.Value = "[invalid endpoint]"
			}
		}
		result = append(result, spec)
	}
	return result
}
