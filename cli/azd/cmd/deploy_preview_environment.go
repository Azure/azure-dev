// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/spf13/cobra"
)

func newCommandEnvironmentService(
	command *cobra.Command,
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyEnv *lazy.Lazy[*environment.Environment],
) azdext.EnvironmentServiceServer {
	if isDeploymentPreview(command) {
		return grpcserver.NewEnvironmentServiceWithEnvironment(lazyAzdContext, lazyEnvManager, lazyEnv)
	}
	return grpcserver.NewEnvironmentService(lazyAzdContext, lazyEnvManager)
}
