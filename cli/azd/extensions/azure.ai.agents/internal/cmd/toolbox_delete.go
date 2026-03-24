// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newToolboxDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a toolbox from the Foundry project.",
		Long: `Delete a toolbox by name from the current Azure AI Foundry project.

You will be prompted to confirm before deleting (use --no-prompt to auto-confirm).`,
		Example: `  # Delete a toolbox (with confirmation prompt)
  azd ai agent toolbox delete my-toolbox

  # Delete without prompting
  azd ai agent toolbox delete my-toolbox --no-prompt`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			endpoint, err := resolveAgentEndpoint(ctx, "", "")
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return exterrors.Auth(
					exterrors.CodeCredentialCreationFailed,
					fmt.Sprintf("failed to create credential: %s", err),
					"Run 'azd auth login' to authenticate",
				)
			}

			// Prompt for confirmation
			if !rootFlags.NoPrompt {
				azdClient, azdErr := azdext.NewAzdClient()
				if azdErr != nil {
					return fmt.Errorf("failed to create azd client for prompting: %w", azdErr)
				}
				defer azdClient.Close()

				resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message: fmt.Sprintf("Are you sure you want to delete Toolbox '%s'?", name),
					},
				})
				if promptErr != nil {
					if exterrors.IsCancellation(promptErr) {
						return exterrors.Cancelled("toolbox deletion cancelled")
					}
					return fmt.Errorf("failed to prompt for confirmation: %w", promptErr)
				}
				if resp == nil || resp.Value == nil || !*resp.Value {
					fmt.Println("toolbox deletion cancelled.")
					return nil
				}
			}

			client := agent_api.NewAgentClient(endpoint, credential)

			_, err = client.DeleteToolbox(ctx, name, agent_api.ToolboxAPIVersion)
			if err != nil {
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) && respErr.StatusCode == 404 {
					return exterrors.Validation(
						exterrors.CodeToolboxNotFound,
						fmt.Sprintf("Toolbox '%s' not found", name),
						"Run 'azd ai agent toolbox list' to see available toolboxes",
					)
				}
				return exterrors.ServiceFromAzure(err, exterrors.OpDeleteToolbox)
			}

			fmt.Printf("Toolbox '%s' deleted successfully.\n", name)
			return nil
		},
	}

	return cmd
}
