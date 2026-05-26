// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type deleteFlags struct {
	name            string
	force           bool
	noPrompt        bool
	output          string
	projectEndpoint string
}

type deleteAction struct{ flags *deleteFlags }

// deleteResult is the JSON shape printed when --output=json.
type deleteResult struct {
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

func (a *deleteAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	if !a.flags.force {
		if a.flags.noPrompt {
			return exterrors.Validation(
				exterrors.CodeMissingForceFlag,
				fmt.Sprintf("deleting %q requires confirmation", a.flags.name),
				"pass --force to skip confirmation in non-interactive mode",
			)
		}
		confirmed, err := a.confirmDelete(ctx)
		if err != nil {
			return err
		}
		if !confirmed {
			return a.printResult(deleteResult{Name: a.flags.name, Cancelled: true})
		}
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}
	if _, err := skillCtx.client.DeleteSkill(ctx, a.flags.name); err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpDeleteSkill)
	}
	return a.printResult(deleteResult{Name: a.flags.name, Deleted: true})
}

func (a *deleteAction) confirmDelete(ctx context.Context) (bool, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return false, fmt.Errorf("create azd client for confirmation: %w", err)
	}
	defer azdClient.Close()

	defaultValue := false
	resp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      fmt.Sprintf("Delete skill %q?", a.flags.name),
			DefaultValue: &defaultValue,
		},
	})
	if err != nil {
		return false, err
	}
	if resp.Value == nil {
		return false, nil
	}
	return *resp.Value, nil
}

func (a *deleteAction) printResult(res deleteResult) error {
	if a.flags.output == outputJSON {
		return printJSON(res)
	}
	if res.Cancelled {
		fmt.Printf("Skill %q deletion cancelled.\n", res.Name)
	} else {
		fmt.Printf("Skill %q deleted.\n", res.Name)
	}
	return nil
}

func newDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &deleteFlags{}
	action := &deleteAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a Foundry skill.",
		Long: `Delete a skill from the resolved Foundry project.

By default the CLI prompts for confirmation. Pass --force to skip the prompt.
In --no-prompt mode (set globally), --force is required.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().BoolVar(&flags.force, "force", false, "Skip the confirmation prompt")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
