// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templateversion

import (
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// RegisterServices registers the template version services with the IoC container
func RegisterServices(container *ioc.NestedContainer) {
	container.MustRegisterSingleton(func(console input.Console, runner exec.CommandRunner) *Manager {
		return NewManager(console, runner)
	})
}
