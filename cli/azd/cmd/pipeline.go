// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/commands/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type pipelineConfigFlags struct {
	pipeline.PipelineManagerArgs
	global *internal.GlobalCommandOptions
	*envFlag
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
	// default provider is empty because it can be set from azure.yaml. By letting default here be empty, we know that
	// there no customer input using --provider
	local.StringVar(&pc.PipelineProvider, "provider", "",
		"The pipeline provider to use (github for Github Actions and azdo for Azure Pipelines).")
	pc.global = global
}

func pipelineActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("pipeline", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "pipeline",
			Short: "Manage and configure your deployment pipelines.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdPipelineHelpDescription,
			Footer:      getCmdPipelineHelpFooter,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupMonitor,
		},
	})

	group.Add("config", &actions.ActionDescriptorOptions{
		Command:        newPipelineConfigCmd(),
		FlagsResolver:  newPipelineConfigFlags,
		ActionResolver: newPipelineConfigAction,
	})

	return group
}

func newPipelineConfigFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *pipelineConfigFlags {
	flags := &pipelineConfigFlags{
		envFlag: newEnvFlag(cmd, global),
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newPipelineConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Create and configure your deployment pipeline by using GitHub or Azure Pipelines.",
	}
}

// pipelineConfigAction defines the action for pipeline config command
type pipelineConfigAction struct {
	flags              *pipelineConfigFlags
	manager            *pipeline.PipelineManager
	azCli              azcli.AzCli
	azdCtx             *azdcontext.AzdContext
	env                *environment.Environment
	console            input.Console
	commandRunner      exec.CommandRunner
	credentialProvider account.SubscriptionCredentialProvider
}

func newPipelineConfigAction(
	azCli azcli.AzCli,
	credentialProvider account.SubscriptionCredentialProvider,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	flags *pipelineConfigFlags,
	commandRunner exec.CommandRunner,
) actions.Action {
	pca := &pipelineConfigAction{
		flags:              flags,
		azCli:              azCli,
		credentialProvider: credentialProvider,
		manager: pipeline.NewPipelineManager(
			azCli, azdCtx, env, flags.global, commandRunner, console, flags.PipelineManagerArgs,
		),
		azdCtx:        azdCtx,
		env:           env,
		console:       console,
		commandRunner: commandRunner,
	}

	return pca
}

// Run implements action interface
func (p *pipelineConfigAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	p.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Configure your azd pipeline",
	})

	credential, err := p.credentialProvider.CredentialForSubscription(ctx, p.env.GetSubscriptionId())
	if err != nil {
		return nil, err
	}

	// Detect the SCM and CI providers based on the project directory
	p.manager.ScmProvider,
		p.manager.CiProvider,
		err = pipeline.DetectProviders(
		ctx, p.azdCtx, p.env, p.manager.PipelineProvider, p.console, credential, p.commandRunner,
	)
	if err != nil {
		return nil, err
	}

	pipelineResult, err := p.manager.Configure(ctx)
	if err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your azd pipeline has been configured!",
			FollowUp: heredoc.Docf(`
			Link to view your new repo: %s
			Link to view your pipeline status: %s`,
				output.WithLinkFormat("%s", pipelineResult.RepositoryLink),
				output.WithLinkFormat("%s", pipelineResult.PipelineLink)),
		},
	}, nil
}

func getCmdPipelineHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage integrating your application with build pipelines.",
		[]string{
			formatHelpNote(fmt.Sprintf("The Azure Developer CLI template includes a GitHub Actions pipeline"+
				" configuration file (in the %s folder) that deploys your application whenever code is pushed"+
				" to the main branch.", output.WithLinkFormat(".github/workflows"))),
			formatHelpNote(fmt.Sprintf("For more information, go to: %s.",
				output.WithLinkFormat("https://aka.ms/azure-dev/pipeline"))),
		})
}

func getCmdPipelineHelpFooter(c *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Walk through the steps required " +
			"to set up your deployment pipeline.": output.WithHighLightFormat("azd pipeline config"),
	})
}
