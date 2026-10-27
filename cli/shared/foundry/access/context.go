// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"strconv"
	"strings"
)

// Environment supplies named configuration values without coupling the library
// to azd, a gRPC service, process environment variables, or local configuration files.
// CurrentName returns an empty name when no environment is selected.
type Environment interface {
	CurrentName(context.Context) (string, error)
	Value(ctx context.Context, environmentName, key string) (string, error)
}

// ContextOptions controls binding an already selected endpoint to environment values.
type ContextOptions struct {
	Endpoint        string
	EnvironmentName string
	// SkipKey is an optional caller-owned boolean opt-out key. Empty disables this lookup.
	SkipKey string
}

// ResolveEnvironment returns a matching environment-backed project target, or nil
// when no binding exists, the selected endpoint differs, or the opt-out is true.
// Read failures remain errors rather than falling back to another environment.
// ARM identity verification is performed by AzureBackend before credentials or grants.
func ResolveEnvironment(ctx context.Context, environment Environment, options ContextOptions) (*Target, error) {
	if environment == nil {
		return nil, failure(InvalidOptions, "an environment reader is required", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := options.EnvironmentName
	if name == "" {
		var err error
		name, err = environment.CurrentName(ctx)
		if err != nil {
			return nil, failure(ResolutionFailed, "resolving the selected environment", err)
		}
	}
	if name == "" {
		return nil, nil
	}
	read := func(key string) (string, error) {
		value, err := environment.Value(ctx, name, key)
		if err != nil {
			return "", failure(ResolutionFailed, "reading Foundry project context", err)
		}
		return strings.TrimSpace(value), nil
	}
	if options.SkipKey != "" {
		skip, err := read(options.SkipKey)
		if err != nil {
			return nil, err
		}
		if enabled, _ := strconv.ParseBool(skip); enabled {
			return nil, nil
		}
	}
	projectID, err := read("AZURE_AI_PROJECT_ID")
	if err != nil || projectID == "" {
		return nil, err
	}
	endpoint, err := read("FOUNDRY_PROJECT_ENDPOINT")
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		endpoint, err = read("AZURE_AI_PROJECT_ENDPOINT")
		if err != nil {
			return nil, err
		}
	}
	if endpoint == "" || !strings.EqualFold(strings.TrimRight(endpoint, "/"), strings.TrimRight(options.Endpoint, "/")) {
		return nil, nil
	}
	return &Target{ProjectID: projectID, Endpoint: options.Endpoint}, nil
}
