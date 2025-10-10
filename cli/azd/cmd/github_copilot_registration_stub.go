// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !ghCopilot

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// registerGitHubCopilotProvider is a no-op when GitHub Copilot is not enabled
// This function is only compiled when the 'with-gh-copilot' build tag is not used
func registerGitHubCopilotProvider(container *ioc.NestedContainer) {
	// No-op: GitHub Copilot provider is not registered when with-gh-copilot build tag is not set
}
