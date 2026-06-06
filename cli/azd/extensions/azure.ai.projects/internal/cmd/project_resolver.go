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

// resolveProjectEndpointOpts controls the 5-level endpoint resolution cascade.
type resolveProjectEndpointOpts struct {
	// FlagValue is the value of an explicit endpoint flag (level 1).
	// Empty means the flag was not provided.
	FlagValue string

	// ReadAzdHostedSources lets callers inject a stub for the azd-hosted
	// lookup (levels 2 + 3). Production callers leave this nil; the resolver
	// then uses the real [readAzdHostedSources].
	ReadAzdHostedSources func(context.Context) (azdHostedSources, error)
}

// resolvedEndpoint holds the result of resolveProjectEndpoint.
type resolvedEndpoint struct {
	Endpoint   string
	Source     EndpointSource
	AzdEnvName string
	SetAt      string // RFC3339 timestamp, only meaningful when Source == SourceGlobalConfig
	// FromLegacyAgentsConfig is true when Source == SourceGlobalConfig and the
	// value was read from the legacy `extensions.ai-agents.project.context`
	// key. The resolver migrates the value to the new key in the same call,
	// so this flag is only true on the first run that observes the legacy
	// key — callers can use it to surface a one-time confirmation notice.
	FromLegacyAgentsConfig bool
}

// azdHostedSources holds the values that the resolver reads from azd-managed
// sources (the active azd environment and ~/.azd/config.json). It is returned
// as a single struct so that tests can stub the whole lookup via
// resolveProjectEndpointOpts.ReadAzdHostedSources.
type azdHostedSources struct {
	// EnvValue is the FOUNDRY_PROJECT_ENDPOINT value from the active azd
	// env, or "" if not set / no active env / no azd client available.
	EnvValue string
	// EnvName is the active azd env name. Only meaningful when EnvValue != "".
	EnvName string
	// CfgState is the project context persisted in global config.
	CfgState projectContextState
	// CfgFound indicates whether a non-empty endpoint was found in global config.
	CfgFound bool
	// CfgFromLegacyAgents reports whether CfgState was sourced from the
	// legacy `extensions.ai-agents.project.context` key. Only meaningful
	// when CfgFound is true.
	CfgFromLegacyAgents bool
}

// readAzdHostedSources dials the azd daemon (if reachable) and reads both
// the active env's FOUNDRY_PROJECT_ENDPOINT and the global-config project
// context in a single client lifetime. Errors talking to the daemon are
// returned only for non-Unavailable cases on the config read — Unavailable
// is treated as "no daemon" and the caller falls through to subsequent levels.
func readAzdHostedSources(ctx context.Context) (azdHostedSources, error) {
	var out azdHostedSources

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
			Key:     "FOUNDRY_PROJECT_ENDPOINT",
		})
		if valErr == nil && envVal.Value != "" {
			out.EnvValue = envVal.Value
			out.EnvName = envResp.Environment.Name
		}
	}

	state, found, cfgErr := getProjectContext(ctx, azdClient)
	if cfgErr != nil {
		// A gRPC Unavailable code means the azd daemon is not reachable;
		// treat it the same as azdClient creation failing and fall through
		// to the host-environment level.  Any other error (e.g. parse
		// failure) is a hard error that callers should surface.
		if !containsGRPCCode(cfgErr, codes.Unavailable) {
			return out, cfgErr
		}
	} else {
		out.CfgState = state
		out.CfgFound = found
	}

	// Legacy fallback: read the key written by the removed
	// `azd ai agent project set` command. Errors are swallowed (best-effort)
	// so a malformed legacy blob does not block FOUNDRY_PROJECT_ENDPOINT.
	if !out.CfgFound {
		if legacyState, legacyFound := getLegacyAgentsProjectContext(ctx, azdClient); legacyFound {
			out.CfgState = legacyState
			out.CfgFound = true
			out.CfgFromLegacyAgents = true

			// Auto-migrate the legacy value into the new key so subsequent
			// invocations resolve from `extensions.ai-projects.context`
			// directly. Best-effort: failures are intentionally swallowed
			// so a transient config write does not break resolution.
			_ = migrateLegacyAgentsProjectContext(ctx, azdClient, legacyState)
		}
	}

	return out, nil
}

// containsGRPCCode walks the error chain looking for a gRPC status with the
// specified code. Because fmt.Errorf("%w", ...) wraps errors without forwarding
// the GRPCStatus() method, we must unwrap manually.
// Note: only follows errors.Unwrap chains; errors.Join multi-wraps are not traversed.
func containsGRPCCode(err error, code codes.Code) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if st, ok := status.FromError(err); ok && st.Code() == code {
			return true
		}
	}
	return false
}

// resolveProjectEndpoint resolves a Foundry project endpoint using the 5-level
// cascade defined in the design spec:
//
//  1. Explicit flag (--project-endpoint, if exposed by the caller)
//  2. Active azd env value (FOUNDRY_PROJECT_ENDPOINT)
//  3. Global config: extensions.ai-projects.context.endpoint in ~/.azd/config.json
//  4. Host environment variable FOUNDRY_PROJECT_ENDPOINT
//  5. Structured error with actionable suggestion
//
// Invalid values at any level produce a hard validation error (no silent fallback).
func resolveProjectEndpoint(
	ctx context.Context,
	opts resolveProjectEndpointOpts,
) (*resolvedEndpoint, error) {
	// Level 1: explicit flag.
	if opts.FlagValue != "" {
		normalized, _, err := validateProjectEndpoint(opts.FlagValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint: normalized,
			Source:   SourceFlag,
		}, nil
	}

	// Levels 2 + 3: azd-hosted sources (active env, then global config).
	readSources := opts.ReadAzdHostedSources
	if readSources == nil {
		readSources = readAzdHostedSources
	}
	sources, err := readSources(ctx)
	if err != nil {
		return nil, err
	}

	// Level 2: active azd environment's FOUNDRY_PROJECT_ENDPOINT.
	if sources.EnvValue != "" {
		normalized, _, err := validateProjectEndpoint(sources.EnvValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint:   normalized,
			Source:     SourceAzdEnv,
			AzdEnvName: sources.EnvName,
		}, nil
	}

	// Level 3: global config (~/.azd/config.json).
	if sources.CfgFound && sources.CfgState.Endpoint != "" {
		normalized, _, err := validateProjectEndpoint(sources.CfgState.Endpoint)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint:               normalized,
			Source:                 SourceGlobalConfig,
			SetAt:                  sources.CfgState.SetAt,
			FromLegacyAgentsConfig: sources.CfgFromLegacyAgents,
		}, nil
	}

	// Level 4: host environment variable FOUNDRY_PROJECT_ENDPOINT.
	if envVal := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); envVal != "" {
		normalized, _, err := validateProjectEndpoint(envVal)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint: normalized,
			Source:   SourceFoundryEnv,
		}, nil
	}

	// Level 5: structured error.
	return nil, noProjectEndpointError()
}
