// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type listFlags struct {
	output          string
	projectEndpoint string
}

type listAction struct{ flags *listFlags }

func (a *listAction) Run(ctx context.Context) error {
	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}
	items, err := skillCtx.client.ListAllSkills(ctx, skill_api.ListOptions{}, 0)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListSkills)
	}
	return printSkillList(items, a.flags.output)
}

func newListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &listFlags{}
	action := &listAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Foundry skills in the project.",
		Long:  `List all skills in the resolved Foundry project.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.output = extCtx.OutputFormat
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
