package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/spf13/cobra"
)

type provisionFlags struct {
	infraCreateFlags
}

func newProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *provisionFlags {
	flags := &provisionFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "provision",
		Aliases: []string{"infra create"},
		Short:   i18nGetText(i18nCmdProvisionShort),
	}
}

type provisionAction struct {
	infraCreate *infraCreateAction
}

func newProvisionAction(
	provisionFlags *provisionFlags,
	infraCreate *infraCreateAction,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	infraCreate.flags = &provisionFlags.infraCreateFlags

	return &provisionAction{
		infraCreate: infraCreate,
	}
}

func (a *provisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return a.infraCreate.Run(ctx)
}

func getCmdProvisionHelpDescription(*cobra.Command) string {
	title := i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18nCmdProvisionHelp),
		TemplateData: struct {
			Command string
		}{
			Command: output.WithHighLightFormat("azd provision"),
		},
	})
	return generateCmdHelpDescription(title, []string{
		formatHelpNote(i18nGetText(i18nCmdProvisionHelpNoteEnv)),
		formatHelpNote(i18nGetText(i18nCmdProvisionHelpNoteLocation)),
		formatHelpNote(i18nGetText(i18nCmdProvisionHelpNoteSubscription)),
	})
}
