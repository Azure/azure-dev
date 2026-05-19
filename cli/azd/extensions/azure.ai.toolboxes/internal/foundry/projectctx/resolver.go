// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadAzdHostedSourcesFunc is a package-level seam so tests can stub the
// daemon-backed lookup without spinning up a real azd gRPC server.
var ReadAzdHostedSourcesFunc = readAzdHostedSources

// readAzdHostedSources dials the azd daemon (if reachable) and reads both the
// active env's AZURE_AI_PROJECT_ENDPOINT and the global-config project context
// in a single client lifetime. Errors talking to the daemon are returned only
// for non-Unavailable cases on the config read — Unavailable is treated as
// "no daemon" and the caller falls through to subsequent levels.
func readAzdHostedSources(ctx context.Context) (AzdHostedSources, error) {
	var out AzdHostedSources

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		// No azd client at all => no hosted sources, not an error.
		return out, nil
	}
	defer azdClient.Close()

	if envResp, err := azdClient.Environment().GetCurrent(
		ctx, &azdext.EmptyRequest{},
	); err == nil {
		envVal, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envResp.Environment.Name,
			Key:     "AZURE_AI_PROJECT_ENDPOINT",
		})
		if valErr == nil && envVal.Value != "" {
			out.EnvValue = envVal.Value
			out.EnvName = envResp.Environment.Name
		}
	}

	state, found, cfgErr := getProjectContext(ctx, azdClient)
	if cfgErr != nil {
		// A gRPC Unavailable code means the azd daemon is not reachable;
		// treat it the same as azdClient creation failing and fall through.
		// Any other error (e.g. parse failure) is a hard error.
		if !containsGRPCCode(cfgErr, codes.Unavailable) {
			return out, cfgErr
		}
	} else {
		out.CfgState = state
		out.CfgFound = found
	}

	return out, nil
}

// containsGRPCCode walks the error chain looking for a gRPC status with the
// specified code. fmt.Errorf("%w", ...) wraps errors without forwarding the
// GRPCStatus() method, so we must unwrap manually.
//
// Note: only follows errors.Unwrap chains; errors.Join multi-wraps are not traversed.
func containsGRPCCode(err error, code codes.Code) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if st, ok := status.FromError(err); ok && st.Code() == code {
			return true
		}
	}
	return false
}

// Resolve resolves a Foundry project endpoint using the 5-level cascade:
//
//  1. --project-endpoint flag
//  2. Active azd env value (AZURE_AI_PROJECT_ENDPOINT)
//  3. Global config: extensions.ai-agents.project.context.endpoint (read-only;
//     owned by azure.ai.agents)
//  4. Host environment variable FOUNDRY_PROJECT_ENDPOINT
//  5. Structured error with actionable suggestion
//
// Invalid values at any level produce a hard validation error (no silent fallback).
func Resolve(ctx context.Context, opts ResolveOpts) (*Resolved, error) {
	// Level 1: explicit flag.
	if opts.FlagValue != "" {
		normalized, _, err := Validate(opts.FlagValue)
		if err != nil {
			return nil, err
		}
		return &Resolved{Endpoint: normalized, Source: SourceFlag}, nil
	}

	// Levels 2 + 3: azd-hosted sources (active env, then global config).
	sources, err := ReadAzdHostedSourcesFunc(ctx)
	if err != nil {
		return nil, err
	}

	// Level 2: active azd environment's AZURE_AI_PROJECT_ENDPOINT.
	if sources.EnvValue != "" {
		normalized, _, err := Validate(sources.EnvValue)
		if err != nil {
			return nil, err
		}
		return &Resolved{
			Endpoint:   normalized,
			Source:     SourceAzdEnv,
			AzdEnvName: sources.EnvName,
		}, nil
	}

	// Level 3: global config (~/.azd/config.json).
	if sources.CfgFound && sources.CfgState.Endpoint != "" {
		normalized, _, err := Validate(sources.CfgState.Endpoint)
		if err != nil {
			return nil, err
		}
		return &Resolved{
			Endpoint: normalized,
			Source:   SourceGlobalConfig,
			SetAt:    sources.CfgState.SetAt,
		}, nil
	}

	// Level 4: host environment variable FOUNDRY_PROJECT_ENDPOINT.
	if envVal := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); envVal != "" {
		normalized, _, err := Validate(envVal)
		if err != nil {
			return nil, err
		}
		return &Resolved{Endpoint: normalized, Source: SourceFoundryEnv}, nil
	}

	// Level 5: structured error.
	return nil, NoEndpointError()
}
