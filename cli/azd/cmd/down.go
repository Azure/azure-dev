package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/spf13/cobra"
)

type downFlags struct {
	infraDeleteFlags
}

func newDownFlags(cmd *cobra.Command, infraDeleteFlags *infraDeleteFlags, global *internal.GlobalCommandOptions) *downFlags {
	flags := &downFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "down",
		Short:   i18nGetText(i18nCmdDownShort),
		Aliases: []string{"infra delete"},
	}
}

type downAction struct {
	infraDelete *infraDeleteAction
}

func newDownAction(
	downFlags *downFlags,
	infraDelete *infraDeleteAction,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	infraDelete.flags = &downFlags.infraDeleteFlags

	return &downAction{
		infraDelete: infraDelete,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return a.infraDelete.Run(ctx)
}

func getCmdDownHelpDescription(*cobra.Command) string {
	title := i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18nCmdDownHelp),
		TemplateData: struct {
			AzdDown string
		}{
			AzdDown: output.WithHighLightFormat("azd down"),
		},
	})

	return generateCmdHelpDescription(title, nil)
}

func getCmdDownHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		i18nGetText(i18nCmdDownHelpSample):      output.WithHighLightFormat("azd down"),
		i18nGetText(i18nCmdDownHelpSampleForce): output.WithHighLightFormat("azd down --force"),
		i18nGetText(i18nCmdDownHelpSamplePurge): output.WithHighLightFormat("azd down --purge"),
	})
}
