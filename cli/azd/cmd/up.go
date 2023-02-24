package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	initFlags
	infraCreateFlags
	deployFlags
	global *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.envFlag.Bind(local, global)
	u.global = global

	u.initFlags.bindNonCommon(local, global)
	u.initFlags.setCommon(&u.envFlag)
	u.infraCreateFlags.bindNonCommon(local, global)
	u.infraCreateFlags.setCommon(&u.envFlag)
	u.deployFlags.bindNonCommon(local, global)
	u.deployFlags.setCommon(&u.envFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: i18nGetText(i18nCmdUpShort),
	}
	annotateGroupCmd(cmd, cmdGroupManage)
	return cmd
}

type upAction struct {
	flags                        *upFlags
	initActionInitializer        actions.ActionInitializer[*initAction]
	infraCreateActionInitializer actions.ActionInitializer[*infraCreateAction]
	deployActionInitializer      actions.ActionInitializer[*deployAction]
	console                      input.Console
	runner                       middleware.MiddlewareContext
}

func newUpAction(
	flags *upFlags,
	initActionInitializer actions.ActionInitializer[*initAction],
	infraCreateActionInitializer actions.ActionInitializer[*infraCreateAction],
	deployActionInitializer actions.ActionInitializer[*deployAction],
	console input.Console,
	runner middleware.MiddlewareContext,
) actions.Action {
	return &upAction{
		flags:                        flags,
		initActionInitializer:        initActionInitializer,
		infraCreateActionInitializer: infraCreateActionInitializer,
		deployActionInitializer:      deployActionInitializer,
		console:                      console,
		runner:                       runner,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	err := u.runInit(ctx)
	if err != nil {
		return nil, fmt.Errorf("running init: %w", err)
	}

	infraCreateAction, err := u.infraCreateActionInitializer()
	if err != nil {
		return nil, err
	}

	infraCreateAction.flags = &u.flags.infraCreateFlags
	provisionOptions := &middleware.Options{CommandPath: "infra create", Aliases: []string{"provision"}}
	_, err = u.runner.RunChildAction(ctx, provisionOptions, infraCreateAction)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	deployAction, err := u.deployActionInitializer()
	if err != nil {
		return nil, err
	}

	deployAction.flags = &u.flags.deployFlags
	deployOptions := &middleware.Options{CommandPath: "deploy"}
	deployResult, err := u.runner.RunChildAction(ctx, deployOptions, deployAction)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

func (u *upAction) runInit(ctx context.Context) error {
	initAction, err := u.initActionInitializer()
	if err != nil {
		return err
	}

	initAction.flags = &u.flags.initFlags
	initOptions := &middleware.Options{CommandPath: "init"}
	_, err = u.runner.RunChildAction(ctx, initOptions, initAction)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	}

	return err
}

func getCmdUpHelpDescription(*cobra.Command) string {
	title := i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18nCmdUpHelp),
		TemplateData: struct {
			AzdInit      string
			AzdProvision string
			AzdDeploy    string
		}{
			AzdInit:      output.WithHighLightFormat("azd up"),
			AzdProvision: output.WithHighLightFormat("azd provision"),
			AzdDeploy:    output.WithHighLightFormat("azd deploy"),
		},
	})

	var notes []string
	notes = append(notes, formatHelpNote(i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18nCmdUpRunningNote),
		TemplateData: struct {
			AzdUp string
		}{
			AzdUp: output.WithHighLightFormat("azd up"),
		},
	})))
	notes = append(notes, formatHelpNote(i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18CmdUpViewNote),
		TemplateData: struct {
			ViewUrl string
		}{
			ViewUrl: output.WithLinkFormat(i18nGetText(i18nAwesomeAzdUrl)),
		},
	})))

	return formatHelpDescription(title, notes)
}

func getCmdUpHelpFooter(*cobra.Command) string {
	var samples []string
	samples = append(samples, getCmdHelpSample(
		i18nGetText(i18nCmdUpFooterSample),
		fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd up --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
		)),
	)
	return getCmdHelpSamplesBlock(samples)
}
