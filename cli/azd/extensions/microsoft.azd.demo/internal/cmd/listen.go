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

			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget("demo", func() azdext.ServiceTargetProvider {
					return project.NewDemoServiceTargetProvider(azdClient)
				}).
				WithFrameworkService("rust", func() azdext.FrameworkServiceProvider {
					return project.NewDemoFrameworkServiceProvider(azdClient)
				}).
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
						fmt.Printf("Service: %s, Artifacts: %d\n", args.Service.Name, len(args.ServiceContext.Package))
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}, nil).
				WithServiceEventHandler("postpackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("Service: %s, Artifacts: %d\n", args.Service.Name, len(args.ServiceContext.Package))
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				}, nil)

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}

			return nil
		},
	}
}
