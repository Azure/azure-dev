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
const projectContextConfigPath = configPathPrefix + ".context"

type projectContextConfig interface {
	GetUserJSON(ctx context.Context, path string, out any) (bool, error)
	UnsetUser(ctx context.Context, path string) error
}

// projectContextState is the JSON shape stored at extensions.ai-projects.context
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

	return readProjectContext(ctx, ch, projectContextConfigPath)
}

func readProjectContext(
	ctx context.Context,
	ch projectContextConfig,
	path string,
) (projectContextState, bool, error) {
	var state projectContextState
	found, err := ch.GetUserJSON(ctx, path, &state)
	if err != nil {
		return projectContextState{}, false,
			fmt.Errorf("getProjectContext: failed to read config: %w", err)
	}

	if !found || state.Endpoint == "" {
		return projectContextState{}, false, nil
	}

	return state, true, nil
}

// getLegacyAgentsProjectContext reads the project context from
// legacyAgentsContextPath. Read errors are swallowed: a malformed legacy
// blob must never break resolution from the new key, explicit flag, or
// FOUNDRY_PROJECT_ENDPOINT.
func getLegacyAgentsProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (projectContextState, bool) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return projectContextState{}, false
	}

	state, found, err := readProjectContext(ctx, ch, legacyAgentsContextPath)
	if err != nil {
		return projectContextState{}, false
	}

	return state, found
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
// Returns the previously stored endpoint (empty if none was set or if the
// stored value could not be decoded). The operation is idempotent.
//
// The read of the previous value is best-effort so a malformed persisted
// blob does not block `unset` from clearing it.
func clearProjectContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (previousEndpoint string, err error) {
	ch, chErr := azdext.NewConfigHelper(azdClient)
	if chErr != nil {
		return "", fmt.Errorf("clearProjectContext: %w", chErr)
	}

	return clearProjectContextFromConfig(ctx, ch)
}

func clearProjectContextFromConfig(
	ctx context.Context,
	ch projectContextConfig,
) (previousEndpoint string, err error) {
	if state, found, readErr := readProjectContext(ctx, ch, projectContextConfigPath); readErr == nil && found {
		previousEndpoint = state.Endpoint
	} else if state, found, readErr := readProjectContext(ctx, ch, legacyAgentsContextPath); readErr == nil && found {
		previousEndpoint = state.Endpoint
	}

	for _, path := range []string{projectContextConfigPath, legacyAgentsContextPath} {
		if err := ch.UnsetUser(ctx, path); err != nil {
			return "", fmt.Errorf("clearProjectContext: failed to clear config at %q: %w", path, err)
		}
	}

	return previousEndpoint, nil
}
