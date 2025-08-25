// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type PublishFlags struct {
	deploy *DeployFlags
}

func (f *PublishFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.deploy.BindNonCommon(local, global)
	f.deploy.bindCommon(local, global)

	// The publish-only flag is not needed for the publish command.
	// We can't remove it since it's part of the DeployFlags struct, but we can hide it.
	_ = local.MarkHidden("publish-only")

	// Update the help text for the --from-package flag
	if fromPackageFlag := local.Lookup("from-package"); fromPackageFlag != nil {
		fromPackageFlag.Usage = "Publishes the service from a container image (image tag)."
	}
}

func (f *PublishFlags) SetCommon(envFlag *internal.EnvFlag) {
	f.deploy.SetCommon(envFlag)
}

func NewPublishFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *PublishFlags {
	flags := &PublishFlags{
		deploy: &DeployFlags{},
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func NewPublishCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <service>",
		Short: "Publish your project to Azure Container Registry. An alias for `azd deploy --publish-only`.",
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

func NewPublishAction(
	flags *PublishFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	environment *environment.Environment,
	accountManager account.Manager,
	cloud *cloud.Cloud,
	azCli *azapi.AzureClient,
	commandRunner exec.CommandRunner,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	// Set publish-only flag to true
	flags.deploy.PublishOnly = true

	// Reuse the deploy action
	return NewDeployAction(
		flags.deploy,
		args,
		projectConfig,
		projectManager,
		serviceManager,
		resourceManager,
		azdCtx,
		environment,
		accountManager,
		cloud,
		azCli,
		commandRunner,
		console,
		formatter,
		writer,
		alphaFeatureManager,
		importManager,
	)
}

func GetCmdPublishHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Publish your project to Azure Container Registry.",
		[]string{
			formatHelpNote("This command is an alias for `azd deploy --publish-only`."),
		})
}

func GetCmdPublishHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Publish all services in the current project.": output.WithHighLightFormat(
			"azd publish --all",
		),
		"Publish the service named 'api'.": output.WithHighLightFormat(
			"azd publish api",
		),
		"Publish the service named 'api' from a previously generated package.": output.WithHighLightFormat(
			"azd publish api --from-package <image-tag>",
		),
	})
}
