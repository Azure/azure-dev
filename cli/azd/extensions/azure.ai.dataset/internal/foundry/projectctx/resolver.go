// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"os"
	"strings"

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

// azd's sentinels for the two absences that reach us without a status.
//
// `pkg/environment` declares these with errors.New, and the daemon's
// error-wrapping interceptor passes an error carrying no suggestion and no auth
// failure through untouched, so grpc encodes both as Unknown -- the same code a
// failure to load project state or the environment manager arrives under. The
// message is the only thing left to tell them apart.
//
// Matched rather than imported: taking a dependency on the environment manager
// for two strings costs more than it settles, and a rename fails closed. The
// command would report the daemon error instead of resolving quietly to a
// lower-priority endpoint, which is the direction to fail in.
const (
	azdNoDefaultEnvironment = "default environment not found"
	azdNoSuchEnvironment    = "environment not found"
)

// hostedSourceAbsent reports whether an error from the azd daemon leaves the
// cascade free to carry on to the next level.
//
// Unavailable is no daemon at all. NotFound is a daemon with nothing under that
// name. Unknown is the one that is not obvious: azd answers the ordinary
// absences -- no default environment, no such environment -- with plain Go
// errors that reach us with no status, and without letting those through, a
// project that simply has no environment selected could never reach the global
// config or the host variable. It is admitted only for those two, because
// Unknown is equally what a failure to load project state arrives as.
//
// Everything else is a failure to answer rather than an answer of "nothing":
// an expired login, a denial, a cancellation, or a server fault. Falling
// through on any of those would resolve quietly to a lower-priority endpoint
// that can belong to a different project.
func hostedSourceAbsent(err error) bool {
	if containsGRPCCode(err, codes.Unavailable) || containsGRPCCode(err, codes.NotFound) {
		return true
	}
	if !containsGRPCCode(err, codes.Unknown) {
		return false
	}
	msg := status.Convert(err).Message()
	return strings.Contains(msg, azdNoDefaultEnvironment) || strings.Contains(msg, azdNoSuchEnvironment)
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
