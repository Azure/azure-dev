// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package add configures resources without changing project files during the wizard.
package add

import (
	"io"

	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Wizard holds the entry-local configuration used by the interactive add flow.
type Wizard struct {
	// Client connects to the azd host for prompts, environment values, and Azure resource selection.
	Client *azdext.AzdClient

	// Writer receives validation feedback and informational messages; nil discards them.
	Writer io.Writer

	// ProjectDir is the project root used to resolve app code and Dockerfile paths.
	ProjectDir string

	// EntryName identifies the infrastructure layer and namespaces its resource environment keys.
	EntryName string

	// Environment selects the azd environment; empty uses the project's default.
	Environment string

	// Resources contains the selected layer's resource definitions to extend.
	Resources resources.Config

	// Services contains the project's service definitions, keyed by name, to extend or check for name conflicts.
	Services map[string]map[string]any
}

// Result contains the complete proposed configuration and deferred environment updates.
type Result struct {
	Resources   resources.Config
	Services    map[string]map[string]any
	Environment map[string]string
	Name        string
	HasService  bool
}
