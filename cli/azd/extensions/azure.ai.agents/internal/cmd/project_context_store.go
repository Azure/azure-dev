// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

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

// setProjectContext persists a validated project endpoint to global config.
// The caller is responsible for validating the endpoint before calling this function.
// Returns the setAt timestamp that was written to config.
func setProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	endpoint string,
) (setAt string, err error) {
	ch, chErr := azdext.NewConfigHelper(azdClient)
	if chErr != nil {
		return "", fmt.Errorf("setProjectContext: %w", chErr)
	}

	state := projectContextState{
		Endpoint: endpoint,
		SetAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if err := ch.SetUserJSON(ctx, projectContextConfigPath, state); err != nil {
		return "", fmt.Errorf("setProjectContext: failed to write config: %w", err)
	}

	return state.SetAt, nil
}

// clearProjectContext removes the context subtree from global config.
// Returns the previously stored endpoint (empty if none was set).
// The operation is idempotent — calling it when no context is set is not an error.
func clearProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (previousEndpoint string, err error) {
	// Read existing state first so we can return the previous endpoint.
	state, found, err := getProjectContext(ctx, azdClient)
	if err != nil {
		return "", err
	}

	if found {
		previousEndpoint = state.Endpoint
	}

	ch, chErr := azdext.NewConfigHelper(azdClient)
	if chErr != nil {
		return "", fmt.Errorf("clearProjectContext: %w", chErr)
	}

	if err := ch.UnsetUser(ctx, projectContextConfigPath); err != nil {
		return "", fmt.Errorf("clearProjectContext: failed to clear config: %w", err)
	}

	return previousEndpoint, nil
}
