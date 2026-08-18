// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
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

	envValue, envName, envErr := readEnvHostedSource(ctx, azdClient.Environment())
	if envErr != nil {
		return out, envErr
	}
	out.EnvValue, out.EnvName = envValue, envName

	state, found, cfgErr := getProjectContext(ctx, azdClient)
	if cfgErr != nil {
		// The same rule the environment reads use. Today the config service can
		// only fail here by being unreachable, but stating it differently in
		// one of three places is how the three come to disagree.
		if !hostedSourceAbsent(cfgErr) {
			return out, cfgErr
		}
	} else {
		out.CfgState = state
		out.CfgFound = found
	}

	return out, nil
}

// envSource is the slice of azd's environment service this file reads.
//
// Narrowed to an interface so the classification below can be tested. The rule
// it applies -- carry on when the daemon answered "nothing", stop when it
// failed to answer -- has regressed twice while every test passed, because the
// only seam was the whole function.
type envSource interface {
	GetCurrent(context.Context, *azdext.EmptyRequest, ...grpc.CallOption) (*azdext.EnvironmentResponse, error)
	GetValue(context.Context, *azdext.GetEnvRequest, ...grpc.CallOption) (*azdext.KeyValueResponse, error)
}

// readEnvHostedSource reads the active environment's project endpoint.
//
// Returns an empty value and no error when there is nothing to read: no
// environment selected, no project at all, or neither key set. An error means
// the daemon failed to answer, which the caller must not read as absence --
// falling through would resolve to a lower-priority endpoint that can belong to
// a different project.
func readEnvHostedSource(ctx context.Context, env envSource) (value, name string, err error) {
	envResp, envErr := env.GetCurrent(ctx, &azdext.EmptyRequest{})
	if envErr != nil {
		if !hostedSourceAbsent(envErr) {
			return "", "", envErr
		}
		return "", "", nil
	}
	if envResp.GetEnvironment() == nil {
		return "", "", nil
	}

	for _, key := range []string{foundryEnvKey, azureAiEnvKey} {
		envVal, valErr := env.GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envResp.Environment.Name,
			Key:     key,
		})
		if valErr != nil {
			if !hostedSourceAbsent(valErr) {
				return "", "", valErr
			}
			continue
		}
		if envVal.GetValue() != "" {
			return envVal.Value, envResp.Environment.Name, nil
		}
	}
	return "", "", nil
}

// azd's absence sentinels, as they reach us.
//
// `pkg/environment` and `pkg/environment/azdcontext` declare these with
// errors.New, and the daemon's error-wrapping interceptor passes an error
// carrying no suggestion and no auth failure through untouched, so all three
// arrive as Unknown -- the same code a failure to load project state arrives
// under. The message is the only thing left to tell them apart.
//
// Matched whole rather than by substring. The message is the only evidence
// there is, so a failure whose prose happens to mention an environment must not
// read as one of these. The default-environment and no-project sentinels arrive
// on their own; the named-environment one arrives from the data store as
// `'<name>': environment not found`.
//
// Matched rather than imported: taking a dependency on the environment manager
// for three strings costs more than it settles, and a rename fails closed. The
// command would report the daemon error instead of resolving quietly to a
// lower-priority endpoint, which is the direction to fail in.
const (
	azdNoDefaultEnvironment = "default environment not found"
	azdNoSuchEnvironment    = "environment not found"
	azdNoProject            = "no project exists; to create a new project, run `azd init`"
)

// HostedSourceAbsent reports whether an error from the azd daemon is an answer
// of "nothing here" rather than a failure to answer.
//
// Exported because more than the cascade has to ask it. Deriving the set of
// azd's absences a second time elsewhere is how a sentinel comes to be handled
// in one place and missed in another, which has happened three times.
func HostedSourceAbsent(err error) bool {
	return hostedSourceAbsent(err)
}

// DaemonUnreachable reports the one absence that is not an answer about
// anything: there was nobody to ask.
//
// The cascade carries on regardless -- an unreachable daemon has no endpoint to
// offer, so the next level should be consulted. A caller reporting *why* a
// value is missing has to tell it apart, or a gRPC hiccup ends up phrased as a
// fact about the project.
func DaemonUnreachable(err error) bool {
	return containsGRPCCode(err, codes.Unavailable)
}

// hostedSourceAbsent reports whether an error from the azd daemon leaves the
// cascade free to carry on to the next level.
//
// Unavailable is no daemon at all. NotFound is a daemon with nothing under that
// name -- kept as a defence, though azd's environment service does not use it
// today. Unknown is the one that is not obvious: azd answers the ordinary
// absences with plain Go errors that reach us with no status, and without
// letting those through, a project with no environment selected -- or a command
// run outside a project at all, which the atomic commands are meant to support
// -- could never reach the global config or the host variable. It is admitted
// only for the three messages above, because Unknown is equally what a failure
// to load project state arrives as.
//
// Everything else is a failure to answer rather than an answer of "nothing":
// an expired login, a denial, a cancellation, or a server fault. Falling
// through on any of those would resolve quietly to a lower-priority endpoint
// that can belong to a different project.
func hostedSourceAbsent(err error) bool {
	if containsGRPCCode(err, codes.Unavailable) || containsGRPCCode(err, codes.NotFound) {
		return true
	}
	// The status the daemon sent, not the flattened text: status.FromError
	// replaces a wrapped error's message with the whole of err.Error(), so the
	// wrapper's own prose would take part in the comparison below.
	st, ok := azdext.GRPCStatusFromError(err)
	if !ok || st.Code() != codes.Unknown {
		return false
	}
	msg := st.Message()
	return msg == azdNoDefaultEnvironment ||
		msg == azdNoProject ||
		strings.HasSuffix(msg, "': "+azdNoSuchEnvironment)
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
