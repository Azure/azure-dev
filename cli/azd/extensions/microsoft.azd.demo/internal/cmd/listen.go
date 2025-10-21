// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.demo/internal/project"
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

			serviceTargetProvider := project.NewDemoServiceTargetProvider(azdClient)
			frameworkServiceProvider := project.NewDemoFrameworkServiceProvider(azdClient)
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget("demo", serviceTargetProvider).
				WithFrameworkService("rust", frameworkServiceProvider).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}).
				WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important predeploy project work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}).
				WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important postdeploy project work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}).
				WithServiceEventHandler("prepackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important prepackage service work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}, &azdext.ServerEventOptions{
					// Optionally filter your subscription by service host and/or language
					Host: "containerapp",
				}).
				WithServiceEventHandler("postpackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important postpackage service work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}, &azdext.ServerEventOptions{
					// Optionally filter your subscription by service host and/or language
					Host: "containerapp",
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
