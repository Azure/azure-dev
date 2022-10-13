// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type pipelineConfigFlags struct {
	pipeline.PipelineManagerArgs
	global *internal.GlobalCommandOptions
}

func (pc *pipelineConfigFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(&pc.PipelineServicePrincipalName, "principal-name", "", "The name of the service principal to use to grant access to Azure resources as part of the pipeline.")
	local.StringVar(&pc.PipelineRemoteName, "remote-name", "origin", "The name of the git remote to configure the pipeline to run on.")
	local.StringVar(&pc.PipelineRoleName, "principal-role", "Contributor", "The role to assign to the service principal.")
	local.StringVar(&pc.PipelineProvider, "provider", "", "The pipeline provider to use (GitHub and Azdo supported).")
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
	flags   pipelineConfigFlags
	manager *pipeline.PipelineManager
	azdCtx  *azdcontext.AzdContext
	console input.Console
}

func newPipelineConfigAction(azdCtx *azdcontext.AzdContext, console input.Console, flags pipelineConfigFlags) *pipelineConfigAction {
	pca := &pipelineConfigAction{
		flags:   flags,
		manager: pipeline.NewPipelineManager(azdCtx, flags.global, flags.PipelineManagerArgs),
		azdCtx:  azdCtx,
		console: console,
	}

	return pca
}

// Run implements action interface
func (p *pipelineConfigAction) Run(ctx context.Context) error {
	if err := ensureProject(p.azdCtx.ProjectPath()); err != nil {
		return err
	}

	// make sure az is logged in
	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	// Read or init env
	console := input.GetConsole(ctx)
	if console == nil {
		log.Panic("missing input console in the provided context")
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &p.manager.RootOptions.EnvironmentName, p.azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	// Detect the SCM and CI providers based on the project directory
	p.manager.ScmProvider,
		p.manager.CiProvider,
		err = pipeline.DetectProviders(ctx, env, p.manager.PipelineProvider)
	if err != nil {
		return err
	}

	// set context for manager
	p.manager.Environment = env

	return p.manager.Configure(ctx)
}
