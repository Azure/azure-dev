// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const projectContextConfigPath = "extensions.ai-projects.context"

type resolveProjectEndpointOpts struct {
	FlagValue            string
	ReadAzdHostedSources func(context.Context) (azdHostedSources, error)
}

type resolvedEndpoint struct {
	Endpoint string
}

type projectContextState struct {
	Endpoint string `json:"endpoint"`
}

type azdHostedSources struct {
	EnvValue string
	Config   projectContextState
}

func readAzdHostedSources(ctx context.Context) (azdHostedSources, error) {
	var sources azdHostedSources
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return sources, nil
	}
	defer azdClient.Close()

	if current, currentErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); currentErr == nil {
		for _, key := range []string{"FOUNDRY_PROJECT_ENDPOINT", "AZURE_AI_PROJECT_ENDPOINT"} {
			value, valueErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: current.Environment.Name,
				Key:     key,
			})
			if valueErr == nil && value.Value != "" {
				sources.EnvValue = value.Value
				break
			}
		}
	}

	config, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return sources, nil
	}
	_, err = config.GetUserJSON(ctx, projectContextConfigPath, &sources.Config)
	if err != nil && !containsGRPCCode(err, codes.Unavailable) {
		return sources, err
	}
	return sources, nil
}

func resolveProjectEndpoint(
	ctx context.Context,
	opts resolveProjectEndpointOpts,
) (*resolvedEndpoint, error) {
	if opts.FlagValue != "" {
		endpoint, err := validateProjectEndpoint(opts.FlagValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: endpoint}, nil
	}

	readSources := opts.ReadAzdHostedSources
	if readSources == nil {
		readSources = readAzdHostedSources
	}
	sources, sourcesErr := readSources(ctx)
	for _, candidate := range []string{
		sources.EnvValue,
		sources.Config.Endpoint,
		os.Getenv("FOUNDRY_PROJECT_ENDPOINT"),
		os.Getenv("AZURE_AI_PROJECT_ENDPOINT"),
	} {
		if candidate == "" {
			continue
		}
		endpoint, err := validateProjectEndpoint(candidate)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: endpoint}, nil
	}
	if sourcesErr != nil {
		return nil, sourcesErr
	}
	return nil, noProjectEndpointError()
}

func containsGRPCCode(err error, code codes.Code) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if grpcStatus, ok := status.FromError(err); ok && grpcStatus.Code() == code {
			return true
		}
	}
	return false
}
