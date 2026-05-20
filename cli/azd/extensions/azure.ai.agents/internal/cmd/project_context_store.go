// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectsExtensionContextPath is the canonical UserConfig path for the
// project endpoint, written by `azd ai project set` in the azure.ai.projects
// extension.
const projectsExtensionContextPath = "extensions.ai-projects.context"

// projectContextConfigPath is the legacy UserConfig path used by the removed
// `azd ai agent project set` command. Read as a fallback only.
const projectContextConfigPath = configPathPrefix + ".project.context"

// projectContextState is the JSON shape stored at both
// projectsExtensionContextPath and projectContextConfigPath.
type projectContextState struct {
	Endpoint string `json:"endpoint"`
	SetAt    string `json:"setAt"`
}

// getProjectContext reads the persisted project context, preferring the new
// canonical key and falling back to the legacy key. Returns (state, true, nil)
// when present, (zero, false, nil) when absent.
func getProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (projectContextState, bool, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return projectContextState{}, false, fmt.Errorf("getProjectContext: %w", err)
	}

	var state projectContextState
	found, err := ch.GetUserJSON(ctx, projectsExtensionContextPath, &state)
	if err != nil {
		return projectContextState{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}
	if found && state.Endpoint != "" {
		return state, true, nil
	}

	// Legacy fallback. Errors are swallowed so a malformed legacy blob does
	// not block resolution from FOUNDRY_PROJECT_ENDPOINT or an explicit flag.
	var legacy projectContextState
	legacyFound, legacyErr := ch.GetUserJSON(ctx, projectContextConfigPath, &legacy)
	if legacyErr != nil || !legacyFound || legacy.Endpoint == "" {
		return projectContextState{}, false, nil
	}

	return legacy, true, nil
}
