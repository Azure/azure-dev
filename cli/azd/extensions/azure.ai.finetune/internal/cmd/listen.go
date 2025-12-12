// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

	"azure.ai.finetune/internal/project"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "listen",
		Short:  "Starts the extension and listens for events.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// IMPORTANT: service target name here must match the name used in the extension manifest.
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget(AiFineTuningHost, func() azdext.ServiceTargetProvider {
					return project.NewFineTuneServiceTargetProvider(azdClient)
				}).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return preprovisionHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return predeployHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return postdeployHandler(ctx, azdClient, args)
				})

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}

			return nil
		},
	}
}

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	fmt.Println("preprovisionHandler: Starting pre-provision event handling")
	time.Sleep(2 * time.Second)
	return nil
}

func predeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	fmt.Println("predeployHandler: Starting pre-deploy event handling")
	time.Sleep(2 * time.Second)
	return nil
}

func postdeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	fmt.Println("postdeployHandler: Starting post-deploy event handling")
	time.Sleep(2 * time.Second)
	return nil
}
