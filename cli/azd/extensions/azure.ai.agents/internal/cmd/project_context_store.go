// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectContextConfigPath is the UserConfig path for the persisted project context.
const projectContextConfigPath = configPathPrefix + ".project.context"

// projectContextState is the JSON shape stored at extensions.ai-agents.context
// in ~/.azd/config.json.
type projectContextState struct {
	Endpoint string `json:"endpoint"`
	SetAt    string `json:"setAt"`
}

// getProjectContext reads the persisted project context from global config.
// Returns (state, true, nil) when present, (zero, false, nil) when absent.
func getProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (projectContextState, bool, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return projectContextState{}, false, fmt.Errorf("getProjectContext: %w", err)
	}

	var state projectContextState
	found, err := ch.GetUserJSON(ctx, projectContextConfigPath, &state)
	if err != nil {
		return projectContextState{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}

	if !found || state.Endpoint == "" {
		return projectContextState{}, false, nil
	}

	return state, true, nil
}
