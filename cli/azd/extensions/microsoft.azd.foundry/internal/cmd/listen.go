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
			receiveChan := eventManager.Receive(ctx)

			// Register the event handler(s) (synchronously).
			err = eventManager.AddProjectEventHandler(
				"preprovision",
				func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					fmt.Printf("Received preprovision event for project '%s' in extension", args.Project.Name)
					time.Sleep(5 * time.Second)
					return nil
				},
			)
			if err != nil {
				return fmt.Errorf("failed to add preprovision project event handler: %w", err)
			}

			err = eventManager.AddServiceEventHandler(
				"prepackage",
				func(ctx context.Context, args *azdext.ServiceEventArgs) error {
					fmt.Printf("Received prepackage event for service '%s' in extension", args.Service.Name)
					time.Sleep(5 * time.Second)
					return nil
				},
				nil,
			)

			if err != nil {
				return fmt.Errorf("failed to add predeploy event handler: %w", err)
			}

			// Block until the Receive function returns, meaning the stream has ended.
			err = <-receiveChan

			return err
		},
	}
}
