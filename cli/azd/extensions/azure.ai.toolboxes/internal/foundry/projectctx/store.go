// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectContextConfigPath is the read-only UserConfig path for the persisted
// project context owned by azure.ai.projects. The toolboxes extension reads
// this key but never writes it.
const projectContextConfigPath = "extensions.ai-projects.context"
const legacyProjectContextConfigPath = "extensions.ai-agents.project.context"

type projectContextConfig interface {
	GetUserJSON(ctx context.Context, path string, out any) (bool, error)
}

// getProjectContext reads the persisted project context from global config.
// Returns (state, true, nil) when present, (zero, false, nil) when absent.
func getProjectContext(
	ctx context.Context, azdClient *azdext.AzdClient,
) (State, bool, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return State{}, false, fmt.Errorf("getProjectContext: %w", err)
	}

	return readProjectContext(ctx, ch)
}

func readProjectContext(ctx context.Context, config projectContextConfig) (State, bool, error) {
	var state State
	found, err := config.GetUserJSON(ctx, projectContextConfigPath, &state)
	if err != nil {
		return State{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}

	if found && state.Endpoint != "" {
		return state, true, nil
	}

	var legacy State
	legacyFound, legacyErr := config.GetUserJSON(ctx, legacyProjectContextConfigPath, &legacy)
	if legacyErr != nil || !legacyFound || legacy.Endpoint == "" {
		return State{}, false, nil
	}
	return legacy, true, nil
}
