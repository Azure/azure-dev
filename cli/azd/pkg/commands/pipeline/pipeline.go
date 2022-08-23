// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type subareaProvider interface {
	requiredTools() []tools.ExternalTool
}

type scmProvider interface {
	subareaProvider
}

type ciProvider interface {
	subareaProvider
}

// pipelineConfigAction defines the action for pipeline config command
type pipelineConfigAction struct {
	manager *pipelineManager
}

// NewConfigAction creates an instance of pipelineConfigAction
func NewConfigAction(rootOptions *commands.GlobalCommandOptions) *pipelineConfigAction {
	return &pipelineConfigAction{
		manager: &pipelineManager{
			rootOptions: rootOptions,
		},
	}
}

// SetupFlags implements action interface
func (p *pipelineConfigAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.StringVar(&p.manager.pipelineServicePrincipalName, "principal-name", "", "The name of the service principal to use to grant access to Azure resources as part of the pipeline.")
	local.StringVar(&p.manager.pipelineRemoteName, "remote-name", "origin", "The name of the git remote to configure the pipeline to run on.")
	local.StringVar(&p.manager.pipelineRoleName, "principal-role", "Contributor", "The role to assign to the service principal.")
}

// Run implements action interface
func (p *pipelineConfigAction) Run(
	ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {

	// TODO: Providers can be init at this point either from azure.yaml or from command args
	// Using GitHub by default for now. To be updated to either GitHub or Azdo.
	// The CI provider might need to have a reference to the SCM provider if its implementation
	// will depend on where is the SCM. For example, azdo support any SCM source.
	p.manager.scmProvider = &gitHubScmProvider{}
	p.manager.ciProvider = &gitHubCiProvider{}

	// set context for manager
	p.manager.console = input.NewConsole(!p.manager.rootOptions.NoPrompt)
	p.manager.azdCtx = azdCtx

	return p.manager.configure(ctx)
}
