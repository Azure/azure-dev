// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectContextConfigPath is the read-only UserConfig path for the persisted
// project context owned by azure.ai.projects. The evaluations extension reads
// this key but never writes it (§ 6 of the design spec).
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

	// The migration deletes this key, so every current config reaches here
	// having already missed. Not finding it is the ordinary case, not a problem.
	var legacy State
	legacyFound, legacyErr := config.GetUserJSON(ctx, legacyProjectContextConfigPath, &legacy)
	if legacyErr != nil {
		// A key that is present but unreadable is not absence. Reading a
		// missing key answers (false, nil), so an error here means the context
		// is there and could not be understood -- and stepping over it resolves
		// the endpoint from a lower-priority source, possibly another project.
		// That is the same fall-through readEnvHostedSource refuses to make.
		return State{}, false, messages.ProjectContextRead(legacyErr)
	}
	if !legacyFound || legacy.Endpoint == "" {
		return State{}, false, nil
	}
	return legacy, true, nil
}
