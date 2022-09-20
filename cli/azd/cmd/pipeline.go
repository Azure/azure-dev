// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/commands/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func pipelineCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage GitHub Actions pipelines.",
		Long: `Manage GitHub Actions pipelines.

The Azure Developer CLI template includes a GitHub Actions pipeline configuration file (in the *.github/workflows* folder) that deploys your application whenever code is pushed to the main branch.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	cmd.AddCommand(pipelineConfigCmd(rootOptions))
	return cmd
}

func pipelineConfigCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		NewConfigAction(rootOptions),
		rootOptions,
		"config",
		"Create and configure your deployment pipeline by using GitHub Actions.",
		&commands.BuildOptions{
			Long: `Create and configure your deployment pipeline by using GitHub Actions.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
		})
	return cmd
}

// pipelineConfigAction defines the action for pipeline config command
type pipelineConfigAction struct {
	manager *pipeline.PipelineManager
}

// NewConfigAction creates an instance of pipelineConfigAction
func NewConfigAction(rootOptions *internal.GlobalCommandOptions) *pipelineConfigAction {
	return &pipelineConfigAction{
		manager: &pipeline.PipelineManager{
			RootOptions: rootOptions,
		},
	}
}

// SetupFlags implements action interface
func (p *pipelineConfigAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.StringVar(&p.manager.PipelineServicePrincipalName, "principal-name", "", "The name of the service principal to use to grant access to Azure resources as part of the pipeline.")
	local.StringVar(&p.manager.PipelineRemoteName, "remote-name", "origin", "The name of the git remote to configure the pipeline to run on.")
	local.StringVar(&p.manager.PipelineRoleName, "principal-role", "Contributor", "The role to assign to the service principal.")
	local.StringVar(&p.manager.PipelineProvider, "provider", "", "The pipeline provider to use (GitHub and Azdo supported).")
}

// Run implements action interface
func (p *pipelineConfigAction) Run(
	ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
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

	env, ctx, err := loadOrInitEnvironment(ctx, &p.manager.RootOptions.EnvironmentName, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	overrideProvider := strings.ToLower(p.manager.PipelineProvider)
	if overrideProvider == "" {
		// Detect the SCM and CI providers based on the project directory
		p.manager.ScmProvider,
			p.manager.CiProvider,
			err = pipeline.DetectProviders(ctx, console, env)
		if err != nil {
			return err
		}
	} else if overrideProvider == "github" {
		p.manager.ScmProvider = &pipeline.GitHubScmProvider{}
		p.manager.CiProvider = &pipeline.GitHubCiProvider{}
	} else if overrideProvider == "azdo" {
		p.manager.ScmProvider = &pipeline.AzdoHubScmProvider{
			Env:        env,
			AzdContext: azdCtx}
		p.manager.CiProvider = &pipeline.AzdoCiProvider{
			Env:        env,
			AzdContext: azdCtx,
		}
	} else {
		return fmt.Errorf("Unknown pipeline provider: %s. Supported providers: [GitHub, Azdo]", overrideProvider)
	}

	// set context for manager
	p.manager.AzdCtx = azdCtx
	p.manager.Environment = env

	return p.manager.Configure(ctx)
}
