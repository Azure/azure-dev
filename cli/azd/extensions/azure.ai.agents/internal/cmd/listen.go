// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "listen",
		Short: "Starts the extension and listens for events.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			projectParser := &project.FoundryParser{AzdClient: azdClient}
			// IMPORTANT: service target name here must match the name used in the extension manifest.
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget("azure.ai.agents", func() azdext.ServiceTargetProvider {
					return project.NewAgentServiceTargetProvider(azdClient)
				}).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					if err := projectParser.SetIdentity(ctx, args); err != nil {
						return fmt.Errorf("failed to set identity: %w", err)
					}

					// TODO: Move this function into its own file
					for _, svc := range args.Project.Services {
						if svc.Host != "foundry.containeragent" {
							continue
						}

						var foundryAgentConfig *project.FoundryAgentConfig
						if err := project.UnmarshalStruct(svc.Config, &foundryAgentConfig); err != nil {
							return fmt.Errorf("failed to parse foundry agent config: %w", err)
						}

						currentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
						if err != nil {
							return err
						}

						// TODO: Generate and update any missing environment variables needed by the agent
						azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
							EnvName: currentEnvResponse.Environment.Name,
							Key:     "MISSING_KEY",
							Value:   "MISSING_VALUE",
						})
					}

					return nil
				}).
				WithProjectEventHandler("postdeploy", projectParser.CoboPostDeploy)

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}

			return nil
		},
	}
}
