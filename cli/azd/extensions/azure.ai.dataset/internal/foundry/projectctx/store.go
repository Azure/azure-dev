// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectContextConfigPath is the read-only UserConfig path for the persisted
// project context owned by azure.ai.agents. The toolboxes extension reads this
// key but never writes it (§ 6 of the design spec).
const projectContextConfigPath = "extensions.ai-agents.project.context"

// getProjectContext reads the persisted project context from global config.
// Returns (state, true, nil) when present, (zero, false, nil) when absent.
func getProjectContext(
	ctx context.Context, azdClient *azdext.AzdClient,
) (State, bool, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return State{}, false, fmt.Errorf("getProjectContext: %w", err)
	}

	var state State
	found, err := ch.GetUserJSON(ctx, projectContextConfigPath, &state)
	if err != nil {
		return State{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}

	if !found || state.Endpoint == "" {
		return State{}, false, nil
	}

	return state, true, nil
}
