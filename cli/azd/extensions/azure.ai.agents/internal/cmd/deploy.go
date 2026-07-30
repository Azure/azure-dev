// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newDeployCommand creates `azd ai agent deploy`, which now exists only to
// redirect users to the standard azd lifecycle.
//
// Prompt agents are first-class azd services (host: azure.ai.agent) created on
// the harness by the service-target provider during `azd up` / `azd deploy`,
// exactly like hosted agents. The previous standalone harness-deploy behavior
// has been removed in favor of that unified flow.
func newDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:    "deploy [name]",
		Short:  "Deprecated: use `azd up` or `azd deploy`.",
		Hidden: true,
		Long: `Deprecated. Prompt and hosted agents both deploy through the standard azd
lifecycle now.

Run 'azd up' to provision infrastructure and create the agent, or 'azd deploy'
to (re)deploy the agent once infrastructure exists.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return (&DeployAction{}).Run(ctx)
		},
	}

	return cmd
}

// DeployAction implements the deprecated deploy redirect.
type DeployAction struct{}

func (a *DeployAction) Run(_ context.Context) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"`azd ai agent deploy` has been replaced by the standard azd lifecycle",
		fmt.Sprintf(
			"run %q to provision and deploy, or %q to (re)deploy an existing project",
			"azd up", "azd deploy",
		),
	)
}
