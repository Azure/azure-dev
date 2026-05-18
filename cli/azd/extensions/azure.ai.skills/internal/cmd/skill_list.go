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
	top             int
	orderBy         string
	output          string
	projectEndpoint string
}

type listAction struct{ flags *listFlags }

func (a *listAction) Run(ctx context.Context) error {
	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}
	items, err := skillCtx.client.ListAll(
		ctx,
		skill_api.ListOptions{Top: a.flags.top, OrderBy: a.flags.orderBy},
		a.flags.top,
	)
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
		Long: `List skills in the resolved Foundry project.

Without --top, the CLI iterates all pages transparently into one flat list.
With --top, the CLI stops once that many items have been collected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.output = extCtx.OutputFormat
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().IntVar(&flags.top, "top", 0, "Return up to N skills (default: all)")
	cmd.Flags().StringVar(&flags.orderBy, "orderby", "", "Sort order forwarded to the service (e.g. 'asc' or 'desc')")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
