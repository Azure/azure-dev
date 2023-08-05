package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	provisionFlags
	deployFlags
	global *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.envFlag.Bind(local, global)
	u.global = global

	u.provisionFlags.bindNonCommon(local, global)
	u.provisionFlags.setCommon(&u.envFlag)
	u.deployFlags.bindNonCommon(local, global)
	u.deployFlags.setCommon(&u.envFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision Azure resources, and deploy your project with a single command.",
	}
}

type upAction struct {
	flags                      *upFlags
	env                        *environment.Environment
	projectConfig              *project.ProjectConfig
	packageActionInitializer   actions.ActionInitializer[*packageAction]
	provisionActionInitializer actions.ActionInitializer[*provisionAction]
	deployActionInitializer    actions.ActionInitializer[*deployAction]
	console                    input.Console
	runner                     middleware.MiddlewareContext
	prompters                  prompt.Prompter
	provisioningManager        *provisioning.Manager
}

func newUpAction(
	flags *upFlags,
	env *environment.Environment,
	_ auth.LoggedInGuard,
	projectConfig *project.ProjectConfig,
	packageActionInitializer actions.ActionInitializer[*packageAction],
	provisionActionInitializer actions.ActionInitializer[*provisionAction],
	deployActionInitializer actions.ActionInitializer[*deployAction],
	console input.Console,
	runner middleware.MiddlewareContext,
	prompters prompt.Prompter,
	provisioningManager *provisioning.Manager,
) actions.Action {
	return &upAction{
		flags:                      flags,
		env:                        env,
		projectConfig:              projectConfig,
		packageActionInitializer:   packageActionInitializer,
		provisionActionInitializer: provisionActionInitializer,
		deployActionInitializer:    deployActionInitializer,
		console:                    console,
		runner:                     runner,
		prompters:                  prompters,
		provisioningManager:        provisioningManager,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if u.flags.provisionFlags.noProgress {
		fmt.Fprintln(
			u.console.Handles().Stderr,
			//nolint:lll
			output.WithWarningFormat(
				"WARNING: The '--no-progress' flag is deprecated and will be removed in a future release.",
			),
		)
		// this flag actually isn't used by the provision command, we set it to false to hide the extra warning
		u.flags.provisionFlags.noProgress = false
	}

	if u.flags.deployFlags.serviceName != "" {
		fmt.Fprintln(
			u.console.Handles().Stderr,
			//nolint:lll
			output.WithWarningFormat("WARNING: The '--service' flag is deprecated and will be removed in a future release."))
	}

	err := u.provisioningManager.Initialize(ctx, u.projectConfig.Path, u.projectConfig.Infra)
	if err != nil {
		return nil, err
	}

	startTime := time.Now()

	packageAction, err := u.packageActionInitializer()
	if err != nil {
		return nil, err
	}
	packageOptions := &middleware.Options{CommandPath: "package"}
	_, err = u.runner.RunChildAction(ctx, packageOptions, packageAction)
	if err != nil {
		return nil, err
	}

	provision, err := u.provisionActionInitializer()
	if err != nil {
		return nil, err
	}

	provision.flags = &u.flags.provisionFlags
	provisionOptions := &middleware.Options{CommandPath: "provision"}
	provisionRes, err := u.runner.RunChildAction(ctx, provisionOptions, provision)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	deploy, err := u.deployActionInitializer()
	if err != nil {
		return nil, err
	}

	deploy.flags = &u.flags.deployFlags
	// move flag to args to avoid extra deprecation flag warning
	if deploy.flags.serviceName != "" {
		deploy.args = []string{deploy.flags.serviceName}
		deploy.flags.serviceName = ""
	}
	deployOptions := &middleware.Options{CommandPath: "deploy"}
	_, err = u.runner.RunChildAction(ctx, deployOptions, deploy)
	if err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was provisioned and deployed to Azure in %s.",
				ux.DurationAsText(since(startTime))),
			FollowUp: provisionRes.Message.FollowUp,
		},
	}, nil
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Executes the %s and %s commands in a single step.",
			output.WithHighLightFormat("azd provision"),
			output.WithHighLightFormat("azd deploy")), nil)
}
