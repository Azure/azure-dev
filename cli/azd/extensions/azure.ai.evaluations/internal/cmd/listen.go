// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newListenCommand registers the service-target provider with azd. It is hidden
// and invoked by azd itself, not by users.
func newListenCommand() *cobra.Command {
	return azdext.NewListenCommand(configureExtensionHost)
}

// configureExtensionHost wires the azure.ai.eval service target so `azd up` and
// `azd deploy` reach this extension. The provider name must match the manifest.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()

	host.WithServiceTarget(project.EvalHost, func() azdext.ServiceTargetProvider {
		return project.NewEvalServiceTargetProvider(
			azdClient,
			func(ctx context.Context) (project.Reconciler, error) {
				return newEvalReconciler(ctx)
			},
		)
	})
}
