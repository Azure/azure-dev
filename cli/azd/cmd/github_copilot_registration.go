// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build ghCopilot

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
)

// registerGitHubCopilotProvider registers the GitHub Copilot LLM provider
// This function is only compiled when the 'copilot' build tag is used
func registerGitHubCopilotProvider(container *ioc.NestedContainer) {
	container.MustRegisterNamedSingleton("github-copilot", llm.NewGitHubCopilotModelProvider)
}
