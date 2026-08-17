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
// active environment's project endpoint and the global-config project context
// in a single client lifetime. The active-env read prefers
// FOUNDRY_PROJECT_ENDPOINT and falls back to AZURE_AI_PROJECT_ENDPOINT (the key
// `azd ai agent init` / `azd add` persist). Errors talking to the daemon are
// returned only for non-Unavailable cases on the config read — Unavailable is
// treated as "no daemon" and the caller falls through to subsequent levels.
func readAzdHostedSources(ctx context.Context) (AzdHostedSources, error) {
	var out AzdHostedSources

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		// No azd client at all => no hosted sources, not an error.
		return out, nil
	}
	defer azdClient.Close()

	envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if envErr != nil && !hostedSourceAbsent(envErr) {
		// The daemon answered, but not with "there is no current environment".
		// Reading that as absence falls through to global config or the host
		// variable, which can point at a different project -- and the command
		// would then land there without anything having said so.
		return out, envErr
	}
	if envErr == nil && envResp.GetEnvironment() != nil {
		for _, key := range []string{foundryEnvKey, azureAiEnvKey} {
			envVal, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envResp.Environment.Name,
				Key:     key,
			})
			if valErr != nil {
				if !hostedSourceAbsent(valErr) {
					return out, valErr
				}
				continue
			}
			if envVal.GetValue() != "" {
				out.EnvValue = envVal.Value
				out.EnvName = envResp.Environment.Name
				break
			}
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

// hostedSourceAbsent reports whether an error from the azd daemon leaves the
// cascade free to carry on to the next level.
//
// azd answers the ordinary "nothing here" cases -- no default environment, no
// such key -- with plain Go errors, which its interceptor passes through
// untouched and grpc then encodes as Unknown. Unknown therefore has to read as
// absence: treating it as a failure would stop a project that simply has no
// environment selected from ever reaching the global config or the host
// variable, which is the whole point of the levels below.
//
// What must not read as absence is a daemon that refused to answer, or a read
// that never finished. An expired login is mapped to Unauthenticated, and a
// Ctrl-C arrives as Canceled; falling through on either would resolve quietly
// to some other project's endpoint.
func hostedSourceAbsent(err error) bool {
	return !containsGRPCCode(err, codes.Unauthenticated) &&
		!containsGRPCCode(err, codes.PermissionDenied) &&
		!containsGRPCCode(err, codes.Canceled) &&
		!containsGRPCCode(err, codes.DeadlineExceeded)
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
//  2. Active azd env value (FOUNDRY_PROJECT_ENDPOINT, then AZURE_AI_PROJECT_ENDPOINT)
//  3. Global config: extensions.ai-agents.project.context.endpoint (read-only;
//     owned by azure.ai.agents)
//  4. Host environment variable (FOUNDRY_PROJECT_ENDPOINT, then AZURE_AI_PROJECT_ENDPOINT)
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

	// Level 2: active azd environment's FOUNDRY_PROJECT_ENDPOINT (with the
	// AZURE_AI_PROJECT_ENDPOINT fallback applied in readAzdHostedSources).
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

	// Level 4: host environment variable (FOUNDRY_PROJECT_ENDPOINT, then the
	// AZURE_AI_PROJECT_ENDPOINT fallback).
	for _, key := range []string{foundryEnvKey, azureAiEnvKey} {
		envVal := os.Getenv(key)
		if envVal == "" {
			continue
		}
		normalized, _, err := Validate(envVal)
		if err != nil {
			return nil, err
		}
		return &Resolved{Endpoint: normalized, Source: SourceFoundryEnv}, nil
	}

	// Level 5: structured error.
	return nil, NoEndpointError()
}
