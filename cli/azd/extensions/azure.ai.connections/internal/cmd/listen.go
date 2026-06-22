// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.connections/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// configureExtensionHost registers the azure.ai.connection service target on
// the supplied host. It is passed to azdext.NewListenCommand from the root
// command, which handles the surrounding setup (access token, AzdClient
// creation, and the host.Run lifecycle).
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()

	// IMPORTANT: the host name must match the provider name in extension.yaml.
	host.WithServiceTarget(project.ConnectionHost, func() azdext.ServiceTargetProvider {
		return project.NewConnectionServiceTargetProvider(azdClient)
	})
}
