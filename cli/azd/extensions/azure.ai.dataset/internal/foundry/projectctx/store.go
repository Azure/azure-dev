// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"

	"azureaidataset/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectContextConfigPath is the read-only UserConfig path for the persisted
// project context owned by azure.ai.projects. The dataset extension reads this
// key but never writes it (§ 6 of the design spec).
const projectContextConfigPath = "extensions.ai-projects.context"

// legacyProjectContextConfigPath is where azure.ai.agents kept the same state.
// `azd ai project show` migrates it to the key above and deletes it, so this is
// a best-effort fallback for a config that has not been migrated yet.
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
		return State{}, false, messages.ProjectContextClient(err)
	}

	return readProjectContext(ctx, ch)
}

func readProjectContext(ctx context.Context, config projectContextConfig) (State, bool, error) {
	var state State
	found, err := config.GetUserJSON(ctx, projectContextConfigPath, &state)
	if err != nil {
		return State{}, false, messages.ProjectContextRead(err)
	}

	if found && state.Endpoint != "" {
		return state, true, nil
	}

	// A failure on the legacy key is absence, not an error: the migration
	// deletes it, so every current config reaches here having already missed.
	var legacy State
	legacyFound, legacyErr := config.GetUserJSON(ctx, legacyProjectContextConfigPath, &legacy)
	if legacyErr != nil || !legacyFound || legacy.Endpoint == "" {
		return State{}, false, nil
	}
	return legacy, true, nil
}
