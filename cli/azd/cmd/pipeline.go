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
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	local.StringVar(&pc.PipelineRoleName, "principal-role", "contributor", "The role to assign to the service principal.")
	local.StringVar(&pc.PipelineProvider, "provider", "github",
		"The pipeline provider to use (github for Github Actions and azdo for Azure Pipelines).")
	pc.envFlag.Bind(local, global)
	pc.global = global
}

func pipelineActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	infraCmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage GitHub Actions or Azure Pipelines.",
		//nolint:lll
		Long: `Manage GitHub Actions or Azure Pipelines.

The Azure Developer CLI template includes a GitHub Actions and an Azure Pipeline configuration file in the ` + output.WithBackticks(`.github/workflows`) + ` and ` + output.WithBackticks(`.azdo/pipelines`) + ` directories respectively. The configuration file deploys your app whenever code is pushed to the main branch.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}

	group := root.Add("pipeline", &actions.ActionDescriptorOptions{
		Command: infraCmd,
	})

	group.Add("config", &actions.ActionDescriptorOptions{
		Command:        newPipelineConfigCmd(),
		FlagsResolver:  newPipelineConfigFlags,
		ActionResolver: newPipelineConfigAction,
	})

	return group
}

func newPipelineConfigFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *pipelineConfigFlags {
	flags := &pipelineConfigFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newPipelineConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Create and configure your deployment pipeline by using GitHub Actions or Azure Pipelines.",
		Long: `Create and configure your deployment pipeline by using GitHub Actions or Azure Pipelines.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}
}

// pipelineConfigAction defines the action for pipeline config command
type pipelineConfigAction struct {
	flags         *pipelineConfigFlags
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
	flags *pipelineConfigFlags,
	commandRunner exec.CommandRunner,
) actions.Action {
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
	env, err := loadOrInitEnvironment(ctx, &p.flags.environmentName, p.azdCtx, p.console, p.azCli)
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
