// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectsExtensionContextPath is the UserConfig path where the
// `azure.ai.projects` extension persists the project endpoint via
// `azd ai project set`. This is now the canonical location.
const projectsExtensionContextPath = "extensions.ai-projects.context"

// projectContextConfigPath is the legacy UserConfig path used by the (removed)
// `azd ai agent project set` command. It is read as a fallback so existing
// users who set their endpoint before the command moved keep working.
const projectContextConfigPath = configPathPrefix + ".project.context"

// projectContextState is the JSON shape stored at extensions.ai-projects.context
// (and at the legacy extensions.ai-agents.project.context) in ~/.azd/config.json.
type projectContextState struct {
	Endpoint string `json:"endpoint"`
	SetAt    string `json:"setAt"`
}

// getProjectContext reads the persisted project context from global config.
// It prefers the new `extensions.ai-projects.context` key written by
// `azd ai project set`, and falls back to the legacy
// `extensions.ai-agents.project.context` key for users who set their endpoint
// before the command moved to the azure.ai.projects extension.
// Returns (state, true, nil) when present, (zero, false, nil) when absent.
func getProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (projectContextState, bool, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return projectContextState{}, false, fmt.Errorf("getProjectContext: %w", err)
	}

	// New canonical location (written by `azd ai project set`).
	var state projectContextState
	found, err := ch.GetUserJSON(ctx, projectsExtensionContextPath, &state)
	if err != nil {
		return projectContextState{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}
	if found && state.Endpoint != "" {
		return state, true, nil
	}

	// Legacy location (written by the removed `azd ai agent project set`).
	// Read errors are best-effort: a malformed legacy blob must not break
	// resolution from FOUNDRY_PROJECT_ENDPOINT or an explicit flag.
	var legacy projectContextState
	legacyFound, legacyErr := ch.GetUserJSON(ctx, projectContextConfigPath, &legacy)
	if legacyErr != nil || !legacyFound || legacy.Endpoint == "" {
		return projectContextState{}, false, nil
	}

	return legacy, true, nil
}
