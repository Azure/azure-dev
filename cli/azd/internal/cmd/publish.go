// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
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
	Image  string
	Tag    string
	deploy *DeployFlags
}

func (f *PublishFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.deploy.BindNonCommon(local, global)
	f.deploy.bindCommon(local, global)

	// Add the --image flag specific to publish command
	local.StringVar(
		&f.Image,
		"image",
		"",
		"Specifies a custom image name for the container published to the registry.",
	)

	// Add the --tag flag specific to publish command
	local.StringVar(
		&f.Tag,
		"tag",
		"",
		"Specifies a custom tag for the container image published to the registry.",
	)

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
		Short: "Publish a service to a container registry.",
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

	// Create the deploy action
	deployAction := NewDeployAction(
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

	// If no custom tags are provided, return the deploy action directly
	if flags.Image == "" && flags.Tag == "" {
		return deployAction
	}

	// Wrap the deploy action to add custom tags to the context and validate
	return &publishActionWrapper{
		deployAction: deployAction,
		flags:        flags,
		args:         args,
	}
}

// publishActionWrapper wraps the deploy action to add custom tags to the context and validate flags
type publishActionWrapper struct {
	deployAction actions.Action
	flags        *PublishFlags
	args         []string
}

func (p *publishActionWrapper) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Validate that --tag or --image requires a specific service
	targetServiceName := p.flags.deploy.ServiceName
	if len(p.args) == 1 {
		targetServiceName = p.args[0]
	}

	if p.flags.deploy.All && (p.flags.Image != "" || p.flags.Tag != "") {
		return nil, errors.New(
			//nolint:lll
			"'--tag' and '--image' cannot be specified when '--all' is set. Specify a specific service by passing a <service>")
	}

	if targetServiceName == "" && (p.flags.Image != "" || p.flags.Tag != "") {
		return nil, errors.New(
			//nolint:lll
			"'--tag' and '--image' cannot be specified when publishing all services. Specify a specific service by passing a <service>",
		)
	}

	ctx = project.WithImageName(ctx, p.flags.Image)
	ctx = project.WithImageTag(ctx, p.flags.Tag)
	return p.deployAction.Run(ctx)
}

func GetCmdPublishHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Publish a service to a container registry.",
		[]string{
			formatHelpNote("Only works with Container App services."),
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
		"Publish the service named 'api' with image name 'app/api' and tag 'prod'.": output.WithHighLightFormat(
			"azd publish api --image app/api --tag prod",
		),
		"Publish the service named 'api' from a previously generated package.": output.WithHighLightFormat(
			"azd publish api --from-package <image-tag>",
		),
	})
}
