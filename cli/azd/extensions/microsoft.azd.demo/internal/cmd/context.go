// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newContextCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "Get the context of the AZD project & environment.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			getConfigResponse, err := azdClient.UserConfig().Get(ctx, &azdext.GetUserConfigRequest{
				Path: "",
			})

			// Intentionally continue if user config retrieval fails
			if err == nil {
				if getConfigResponse.Found {
					color.HiWhite("User Config")
					var userConfig map[string]string
					err := json.Unmarshal(getConfigResponse.Value, &userConfig)
					if err == nil {
						jsonBytes, err := json.MarshalIndent(userConfig, "", "  ")
						if err == nil {
							fmt.Println(string(jsonBytes))
						}
					}
				}
			}

			getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err == nil {
				color.Cyan("Project:")

				projectValues := map[string]string{
					"Name": getProjectResponse.Project.Name,
					"Path": getProjectResponse.Project.Path,
				}

				for key, value := range projectValues {
					fmt.Printf("%s: %s\n", color.HiWhiteString(key), value)
				}
				fmt.Println()
			} else {
				color.Yellow("WARNING: No azd project found in current working directory")
				fmt.Printf("Run %s to create a new project.\n", color.CyanString("azd init"))
				return nil
			}

			var currentEnvName string

			getEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if err == nil {
				currentEnvName = getEnvResponse.Environment.Name
			} else {
				color.Yellow("WARNING: No azd environment(s) found.")
				fmt.Printf("Run %s to create a new environment.\n", color.CyanString("azd env new"))
				return nil
			}

			var environments []string
			envListResponse, err := azdClient.Environment().List(ctx, &azdext.EmptyRequest{})
			if err == nil {
				for _, env := range envListResponse.Environments {
					environments = append(environments, env.Name)
				}
			}

			if len(environments) == 0 {
				fmt.Println("No environments found")
				return nil
			}

			if currentEnvName != "" {
				color.Cyan("Environments:")
				for _, env := range environments {
					envLine := env
					if env == currentEnvName {
						envLine += color.HiWhiteString(" (selected)")
					}

					fmt.Printf("- %s\n", envLine)
				}

				fmt.Println()

				getValuesResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
					Name: currentEnvName,
				})
				if err == nil {
					color.Cyan("Environment values:")
					for _, pair := range getValuesResponse.KeyValues {
						fmt.Printf("%s: %s\n", color.HiWhiteString(pair.Key), color.HiBlackString(pair.Value))
					}
					fmt.Println()
				}

				deploymentContextResponse, err := azdClient.Deployment().GetDeploymentContext(ctx, &azdext.EmptyRequest{})
				if err == nil {
					scopeMap := map[string]string{
						"Tenant ID":       deploymentContextResponse.AzureContext.Scope.TenantId,
						"Subscription ID": deploymentContextResponse.AzureContext.Scope.SubscriptionId,
						"Location":        deploymentContextResponse.AzureContext.Scope.Location,
						"Resource Group":  deploymentContextResponse.AzureContext.Scope.ResourceGroup,
					}

					color.Cyan("Deployment Context:")
					for key, value := range scopeMap {
						if value == "" {
							value = "N/A"
						}

						fmt.Printf("%s: %s\n", color.HiWhiteString(key), value)
					}
					fmt.Println()

					color.Cyan("Provisioned Azure Resources:")
					for _, resourceId := range deploymentContextResponse.AzureContext.Resources {
						resource, err := arm.ParseResourceID(resourceId)
						if err == nil {
							fmt.Printf(
								"- %s (%s)\n",
								resource.Name,
								color.HiBlackString(resource.ResourceType.String()),
							)
						}
					}
					fmt.Println()
				}
			}

			return nil
		},
	}
}
