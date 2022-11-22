// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type pipelineConfigFlags struct {
	pipeline.PipelineManagerArgs
	global *internal.GlobalCommandOptions
	envFlag
}

func (pc *pipelineConfigFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&pc.PipelineServicePrincipalName,
		"principal-name",
		"",
		"The name of the service principal to use to grant access to Azure resources as part of the pipeline.",
	)
	local.StringVar(
		&pc.PipelineRemoteName,
		"remote-name",
		"origin",
		"The name of the git remote to configure the pipeline to run on.",
	)
	local.StringVar(
		&pc.PipelineAuthTypeName,
		"auth-type",
		"",
		"The authentication type used between the pipeline provider and Azure for deployment (Only valid for GitHub provider)",
	)
	local.StringVar(&pc.PipelineRoleName, "principal-role", "Contributor", "The role to assign to the service principal.")
	local.StringVar(&pc.PipelineProvider, "provider", "", "The pipeline provider to use (GitHub and Azdo supported).")
	pc.envFlag.Bind(local, global)
	pc.global = global
}

func pipelineCmd(global *internal.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage GitHub Actions pipelines.",
		//nolint:lll
		Long: `Manage GitHub Actions pipelines.

The Azure Developer CLI template includes a GitHub Actions pipeline configuration file (in the *.github/workflows* folder) that deploys your application whenever code is pushed to the main branch.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	cmd.AddCommand(BuildCmd(global, pipelineConfigCmdDesign, initPipelineConfigAction, nil))
	return cmd
}

func pipelineConfigCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *pipelineConfigFlags) {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Create and configure your deployment pipeline by using GitHub Actions.",
		Long: `Create and configure your deployment pipeline by using GitHub Actions.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}

	flags := &pipelineConfigFlags{}
	flags.Bind(cmd.Flags(), global)

	return cmd, flags
}

// pipelineConfigAction defines the action for pipeline config command
type pipelineConfigAction struct {
	flags         pipelineConfigFlags
	manager       *pipeline.PipelineManager
	azCli         azcli.AzCli
	azdCtx        *azdcontext.AzdContext
	console       input.Console
	credential    azcore.TokenCredential
	commandRunner exec.CommandRunner
}

func newPipelineConfigAction(
	azCli azcli.AzCli,
	credential azcore.TokenCredential,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	flags pipelineConfigFlags,
	commandRunner exec.CommandRunner,
) *pipelineConfigAction {
	pca := &pipelineConfigAction{
		flags:      flags,
		azCli:      azCli,
		credential: credential,
		manager: pipeline.NewPipelineManager(
			azCli, azdCtx, flags.global, commandRunner, console, flags.PipelineManagerArgs,
		),
		azdCtx:        azdCtx,
		console:       console,
		commandRunner: commandRunner,
	}

	return pca
}

// Run implements action interface
func (p *pipelineConfigAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(p.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &p.flags.environmentName, p.azdCtx, p.console, p.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	// Detect the SCM and CI providers based on the project directory
	p.manager.ScmProvider,
		p.manager.CiProvider,
		err = pipeline.DetectProviders(
		ctx, p.azdCtx, env, p.manager.PipelineProvider, p.console, p.credential, p.commandRunner,
	)
	if err != nil {
		return nil, err
	}

	// set context for manager
	p.manager.Environment = env

	return nil, p.manager.Configure(ctx)
}
