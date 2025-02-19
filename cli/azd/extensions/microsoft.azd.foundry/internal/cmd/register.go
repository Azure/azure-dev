package cmd

import (
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newRegisterCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "register",
		Short: "Register the extension.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			eventStream, err := azdClient.Events().EventStream(ctx)
			if err != nil {
				return fmt.Errorf("failed to create event stream: %w", err)
			}

			err = eventStream.Send(&azdext.EventMessage{
				MessageType: &azdext.EventMessage_Subscribe{
					Subscribe: &azdext.SubscribeMessage{
						EventNames: []string{"preprovision"},
					},
				},
			})

			if err != nil {
				return fmt.Errorf("failed to send subscribe message: %w", err)
			}

			for {
				msg, err := eventStream.Recv()
				if err != nil {
					return fmt.Errorf("failed to receive message: %w", err)
				}

				switch msg.MessageType.(type) {
				case *azdext.EventMessage_Invoke:
					invokeMsg := msg.GetInvoke()

					// Simulate some work
					time.Sleep(5 * time.Second)

					err = eventStream.Send(&azdext.EventMessage{
						MessageType: &azdext.EventMessage_Status{
							Status: &azdext.StatusMessage{
								EventName: invokeMsg.EventName,
								Status:    "completed",
								Message:   "Extension hook completed",
							},
						},
					})

					if err != nil {
						return fmt.Errorf("failed to send status message: %w", err)
					}
				}
			}
		},
	}
}
