// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type deleteFlags struct {
	accountName string
	projectName string
	name        string
}

type DeleteAction struct {
	*AgentContext
	flags *deleteFlags
}

func newDeleteCommand() *cobra.Command {
	flags := &deleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a deployed agent.",
		Long: `Delete a deployed agent from the Foundry project.

Permanently removes the agent and all its versions. This action cannot be undone.
You will be prompted for confirmation unless --no-prompt is set.`,
		Example: `  # Delete an agent (will prompt for confirmation)
  azd ai agent delete --name my-agent

  # Delete without confirmation
  azd ai agent delete --name my-agent --no-prompt`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			agentContext, err := newAgentContext(ctx, flags.accountName, flags.projectName, flags.name, "")
			if err != nil {
				return err
			}

			action := &DeleteAction{
				AgentContext: agentContext,
				flags:       flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.accountName, "account-name", "a", "", "Cognitive Services account name")
	cmd.Flags().StringVarP(&flags.projectName, "project-name", "p", "", "AI Foundry project name")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Name of the agent to delete (required)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func (a *DeleteAction) Run(ctx context.Context) error {
	// Confirm deletion unless --no-prompt is set
	if !rootFlags.NoPrompt {
		fmt.Printf("Delete agent '%s'? This cannot be undone. [y/N]: ", a.Name)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	_, err = agentClient.DeleteAgent(ctx, a.Name, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to delete agent '%s': %w", a.Name, err)
	}

	// Clean up local session/conversation state
	localCtx := loadLocalContext()
	if localCtx.Sessions != nil {
		delete(localCtx.Sessions, a.Name)
	}
	if localCtx.Conversations != nil {
		delete(localCtx.Conversations, a.Name)
	}
	_ = saveLocalContext(localCtx)

	fmt.Printf("Agent '%s' deleted.\n", a.Name)
	return nil
}
