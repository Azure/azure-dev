// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azure.logicappsstandard/internal/project"
)

// configureListen is called by NewListenCommand to register event handlers.
func configureListen(host *azdext.ExtensionHost) {
	host.WithFrameworkService("logicappsstandard", func() azdext.FrameworkServiceProvider {
		return project.NewLogicAppsStandardFrameworkServiceProvider()
	})
}
