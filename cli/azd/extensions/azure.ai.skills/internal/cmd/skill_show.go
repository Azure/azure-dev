// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type showFlags struct {
	name            string
	output          string
	projectEndpoint string
}

type showAction struct{ flags *showFlags }

func (a *showAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}
	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}
	s, err := skillCtx.client.GetSkill(ctx, a.flags.name)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetSkill)
	}
	return printSkillDetail(s, a.flags.output)
}

func newShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &showFlags{}
	action := &showAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show metadata for a Foundry skill.",
		Long: `Show the metadata returned by the service for a skill — including
default_version and latest_version. To retrieve skill content, use
'azd ai skill download <name>'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}
