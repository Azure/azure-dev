// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

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

			eventManager := azdext.NewEventManager(azdClient)
			defer eventManager.Close()

			// Register the event handlers
			err = eventManager.AddProjectEventHandler(
				ctx,
				"preprovision",
				func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				},
			)
			if err != nil {
				return fmt.Errorf("failed to add preprovision project event handler: %w", err)
			}

			err = eventManager.AddServiceEventHandler(
				ctx,
				"prepackage",
				func(ctx context.Context, args *azdext.ServiceEventArgs) error {
					for i := 1; i <= 20; i++ {
						fmt.Printf("%d. Doing important work in extension...\n", i)
						time.Sleep(250 * time.Millisecond)
					}

					return nil
				},
				nil,
			)

			if err != nil {
				return fmt.Errorf("failed to add predeploy event handler: %w", err)
			}

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := eventManager.Receive(ctx); err != nil {
				return fmt.Errorf("failed to receive events: %w", err)
			}

			return nil
		},
	}
}
