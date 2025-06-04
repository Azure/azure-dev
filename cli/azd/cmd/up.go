// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	cmd.ProvisionFlags
	cmd.DeployFlags
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.EnvFlag.Bind(local, global)
	u.global = global

	u.ProvisionFlags.BindNonCommon(local, global)
	u.ProvisionFlags.SetCommon(&u.EnvFlag)
	u.DeployFlags.BindNonCommon(local, global)
	u.DeployFlags.SetCommon(&u.EnvFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision and deploy your project to Azure with a single command.",
	}
}

type upAction struct {
	flags               *upFlags
	console             input.Console
	env                 *environment.Environment
	projectConfig       *project.ProjectConfig
	provisioningManager *provisioning.Manager
	envManager          environment.Manager
	prompters           prompt.Prompter
	importManager       *project.ImportManager
	workflowRunner      *workflow.Runner
}

var defaultUpWorkflow = &workflow.Workflow{
	Name: "up",
	Steps: []*workflow.Step{
		{AzdCommand: workflow.Command{Args: []string{"package", "--all"}}},
		{AzdCommand: workflow.Command{Args: []string{"provision"}}},
		{AzdCommand: workflow.Command{Args: []string{"deploy", "--all"}}},
	},
}

func newUpAction(
	flags *upFlags,
	console input.Console,
	env *environment.Environment,
	_ auth.LoggedInGuard,
	projectConfig *project.ProjectConfig,
	provisioningManager *provisioning.Manager,
	envManager environment.Manager,
	prompters prompt.Prompter,
	importManager *project.ImportManager,
	workflowRunner *workflow.Runner,
) actions.Action {
	   // DEBUG: Print the types of all arguments received by newUpAction
	   fmt.Printf("[azd debug] newUpAction args: flags=%T, console=%T, env=%T, projectConfig=%T, provisioningManager=%T, envManager=%T, prompters=%T, importManager=%T, workflowRunner=%T\n",
			   flags, console, env, projectConfig, provisioningManager, envManager, prompters, importManager, workflowRunner)

	   return &upAction{
			   flags:               flags,
			   console:             console,
			   env:                 env,
			   projectConfig:       projectConfig,
			   provisioningManager: provisioningManager,
			   envManager:          envManager,
			   prompters:           prompters,
			   importManager:       importManager,
			   workflowRunner:      workflowRunner,
	   }
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	infra, err := u.importManager.ProjectInfrastructure(ctx, u.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	// TODO(weilim): remove this once we have decided if it's okay to not set AZURE_SUBSCRIPTION_ID and AZURE_LOCATION
	// early in the up workflow in #3745
	// Only handle Bicep-specific error if the provider is Bicep, otherwise skip
	if infra.Options.Provider == provisioning.Bicep {
		err = u.provisioningManager.Initialize(ctx, u.projectConfig.Path, infra.Options)
		if errors.Is(err, bicep.ErrEnsureEnvPreReqBicepCompileFailed) {
			// If bicep is not available, we continue to prompt for subscription and location unfiltered
			err = provisioning.EnsureSubscriptionAndLocation(
				ctx, u.envManager, u.env, u.prompters, provisioning.EnsureSubscriptionAndLocationOptions{})
			if err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	} else {
		err = u.provisioningManager.Initialize(ctx, u.projectConfig.Path, infra.Options)
		if err != nil {
			return nil, err
		}
	}

	startTime := time.Now()

	upWorkflow, has := u.projectConfig.Workflows["up"]
	if !has {
		upWorkflow = defaultUpWorkflow
	} else {
		u.console.Message(ctx, output.WithGrayFormat("Note: Running custom 'up' workflow from azure.yaml"))
	}

	if u.flags.EnvironmentName != "" {
		ctx = context.WithValue(ctx, envFlagCtxKey, u.flags.EnvFlag)
	}

	if err := u.workflowRunner.Run(ctx, upWorkflow); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your up workflow to provision and deploy to Azure completed in %s.",
				ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		heredoc.Docf(
			`Runs a workflow to %s, %s and %s your application in a single step.

			The %s workflow can be customized by adding a %s section to your %s.

			For example, modify the workflow to provision before packaging and deploying:

			-------------------------
			%s
			workflows:
			  up:
				- azd: provision
				- azd: package --all
				- azd: deploy --all
			-------------------------

			Any azd command and flags are supported in the workflow steps.`,
			output.WithHighLightFormat("package"),
			output.WithHighLightFormat("provision"),
			output.WithHighLightFormat("deploy"),
			output.WithHighLightFormat("up"),
			output.WithHighLightFormat("workflows"),
			output.WithHighLightFormat("azure.yaml"),
			output.WithGrayFormat("# azure.yaml"),
		),
		nil,
	)
}
