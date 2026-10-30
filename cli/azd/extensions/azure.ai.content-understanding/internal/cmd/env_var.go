// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strings"

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

type environmentVariableSpec struct {
	Name, Description, Values, Default, Scope, Precedence string
	Sensitive                                             bool
}

var environmentVariables = []environmentVariableSpec{
	{
		Name: "CU_ENDPOINT", Description: "CU resource endpoint.",
		Values: "HTTPS resource URL under services.ai.azure.com or cognitiveservices.azure.com", Default: "required",
		Scope: "service commands", Precedence: "--endpoint > process > azd environment > user config",
	},
	{
		Name: "CU_API_VERSION", Description: "CU service API version; offline schema reads only --api-version and process.",
		Values: "2025-11-01 or YYYY-MM-DD-preview", Default: cuclient.DefaultAPIVersion,
		Scope: "service commands and offline schema", Precedence: "--api-version > process > azd environment > user config",
	},
	{
		Name: "CU_DEFAULT_ANALYZER", Description: "Default analyzer for analysis.",
		Values: "valid analyzer ID", Default: "prebuilt-layout",
		Scope: "analyze", Precedence: "--analyzer > process > azd environment > user config",
	},
	{
		Name: "CU_API_KEY", Description: "Optional resource API key; never persisted and always redacted.",
		Values: "nonempty key without whitespace or controls", Default: "unset; use Microsoft Entra ID",
		Scope: "CU data-plane authentication", Precedence: "process only; conflicts with process CU_AUTH_MODE=login",
		Sensitive: true,
	},
	{
		Name: "CU_AUTH_MODE", Description: "Explicitly require azd Microsoft Entra ID authentication.",
		Values: "login", Default: "process CU_API_KEY if set, otherwise azd Microsoft Entra ID",
		Scope: "authentication", Precedence: "process or azd environment; process login conflicts with process CU_API_KEY",
	},
	{
		Name: "CU_ON_EXISTS", Description: "Policy for analysis output files that already exist.",
		Values: "error, skip, or reanalyze", Default: "error",
		Scope: "analyze", Precedence: "--on-existing > process",
	},
	{
		Name: "CU_DISABLE_AUTO_CONFIG", Description: "Skip automatic CU configuration after provisioning, not model setup.",
		Values: "1, true, yes, on (case-insensitive) enable; other values disable", Default: "false",
		Scope: "infra postprovision", Precedence: "process only",
	},
	{
		Name: "AZD_ENVIRONMENT", Description: "Select an azd project environment.",
		Values: "existing environment name", Default: "current environment, or projectless with an endpoint",
		Scope: "azd environment selection", Precedence: "--environment > process > current environment",
	},
	{
		Name: "AZURE_TENANT_ID", Description: "User-access tenant for Entra authentication; no tenant lookup in key mode.",
		Values: "Azure tenant ID", Default: "deployment context or subscription lookup, otherwise azd authentication",
		Scope: "authentication", Precedence: "process > azd environment > resolved tenant",
	},
	{
		Name: "AZURE_SUBSCRIPTION_ID", Description: "Subscription for resource scope and Entra tenant lookup.",
		Values: "Azure subscription ID", Default: "selected deployment scope when applicable",
		Scope: "Azure resource scope", Precedence: "explicit resource selectors > process > azd environment > deployment",
	},
	{
		Name: "AZURE_RESOURCE_GROUP", Description: "Resource group for Azure resource discovery.",
		Values: "Azure resource-group name", Default: "selected deployment scope when applicable",
		Scope: "Azure resource scope", Precedence: "explicit resource selectors > process > azd environment > deployment",
	},
	{
		Name: "AZD_DEBUG", Description: "Enable inherited azd diagnostic logging.",
		Values: "true or false", Default: "false",
		Scope: "azd diagnostics", Precedence: "--debug > process",
	},
	{
		Name: "AZD_NO_PROMPT", Description: "Disable interactive prompts; does not imply --yes.",
		Values: "true or false", Default: "false",
		Scope: "azd prompts", Precedence: "--no-prompt > process",
	},
	{
		Name: "AZD_CWD", Description: "Working directory inherited by the extension.",
		Values: "directory path", Default: "process working directory",
		Scope: "azd working directory", Precedence: "--cwd > process",
	},
}

func environmentVariableHelp() string {
	var help strings.Builder
	help.WriteString("Discover supported process environment variables and inspect values that are set.\n\n" +
		"Supported environment variables (including unset variables):\n")
	for _, spec := range environmentVariables {
		fmt.Fprintf(&help, "\n%s\n  %s\n  Values: %s. Default: %s.\n  Scope: %s.\n  Precedence: %s.\n",
			spec.Name, spec.Description, spec.Values, spec.Default, spec.Scope, spec.Precedence)
	}
	return help.String()
}

func newEnvVarCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	group := &cobra.Command{
		Use: "env-var", Short: "Discover supported environment variables and inspect set values",
		Long: environmentVariableHelp(),
	}
	list := &cobra.Command{
		Use: "list", Args: cobra.NoArgs, Short: "List currently set supported process variables with secrets redacted",
		Long: "List supported compatibility and inherited azd process variables that are currently set.\n" +
			"Does not read selected-environment values, host credentials, or unrelated process variables.\n" +
			"CU_API_KEY is always redacted and is supported only as a process input.\n" +
			"Use azd ai cu env-var --help for all supported variables, values, defaults, and precedence.",
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
	for _, spec := range environmentVariables {
		value, present := lookup(spec.Name)
		if !present {
			continue
		}
		variable := environmentVariable{Name: spec.Name, Value: value, Scope: spec.Scope, Sensitive: spec.Sensitive}
		if spec.Sensitive {
			variable.Value = "********"
		} else if spec.Name == "CU_ENDPOINT" {
			if _, err := cuclient.NormalizeEndpoint(value); err != nil {
				variable.Value = "[invalid endpoint]"
			}
		}
		result = append(result, variable)
	}
	return result
}
