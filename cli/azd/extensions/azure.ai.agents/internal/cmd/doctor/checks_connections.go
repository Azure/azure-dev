// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"azure.ai.projects/synthesis"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// foundryConnectionsProbeTimeout caps the per-project connections
// list round trip. 10s budget per remote probe so a stuck VPN or
// transient Foundry hiccup never drags the whole doctor run.
const foundryConnectionsProbeTimeout = 10 * time.Second

// foundryConnectionsProbeFn is the seam-friendly signature for the
// project connections list probe. The closure receives the validated
// project endpoint needed by `FoundryProjectsClient` and
// returns the connection names that exist on the project. Errors
// short-circuit the check to Skip — a 401/403/network failure has
// the same surface as "Foundry unreachable" which the upstream
// `remote.foundry-endpoint` already classifies, so silencing here
// keeps the report from emitting two near-identical Fail lines.
type foundryConnectionsProbeFn func(
	ctx context.Context,
	projectEndpoint string,
) ([]string, error)

// newCheckConnections produces Check `remote.connections`. For each
// enabled connection collected from unified azure.yaml services or
// compatible sources, the check queries the Foundry project's
// connection list and verifies a connection with the matching name
// exists. The check Passes when every configured connection has a
// corresponding entry; Fails when one or more are missing or when
// connection configuration cannot be loaded.
//
// # Skip cascade
//
//   - deps.AzdClient nil → upstream `local.grpc-extension` already
//     surfaced the actionable error.
//   - `local.environment-selected` failed/skipped → nothing to read
//     state from.
//   - `local.azure-yaml` / `local.agent-service-detected` failed →
//     no services to walk; would Pass falsely if we forged ahead.
//   - `remote.auth` failed → Foundry list would 401 identically;
//     let the auth check own the diagnosis.
//   - `remote.foundry-endpoint` failed → same root cause, same
//     remediation.
//   - state.ConnectionLoadErrors set → configuration could not be
//     read. Fail without probing so a bad $ref is not Skip.
//   - state.HasConnections == false → no enabled connection services;
//     Skip rather than a vacuous Pass.
//   - Prior checks did not surface FOUNDRY_PROJECT_ENDPOINT →
//     Skip without guessing a URL from AZURE_AI_PROJECT_ID.
//
// # Classification
//
//   - Every configured connection matches a Foundry connection
//     name → Pass with the matched count.
//   - One or more missing → Fail with the missing names listed in
//     the Message and structured under `Details["missingConnections"]`
//     (each entry carries Name, ServiceName, Detail — the
//     "<Category> | <Target>" identifier from collected state).
//   - Probe error → Skip with the underlying error verbatim.
func newCheckConnections(deps Dependencies) Check {
	return Check{
		ID:     "remote.connections",
		Name:   "Configured connections exist on Foundry project",
		Remote: true,
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable.",
				}
			}
			if priorBlocked(prior, "local.environment-selected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no azd environment is selected " +
						"(see check `local.environment-selected`).",
				}
			}
			if priorBlocked(prior, "local.azure-yaml") ||
				priorBlocked(prior, "local.agent-service-detected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: azure.yaml / agent service detection failed " +
						"(see checks `local.azure-yaml`, `local.agent-service-detected`).",
				}
			}
			if priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: auth probe did not succeed " +
						"(see check `remote.auth`).",
				}
			}
			if priorBlocked(prior, "remote.foundry-endpoint") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: Foundry project endpoint unreachable " +
						"(see check `remote.foundry-endpoint`).",
				}
			}

			state, errs := deps.AssembleAgentState(ctx)
			if state == nil {
				cause := "unknown error"
				if len(errs) > 0 {
					cause = errs[0].Error()
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to assemble agent state: %s", cause),
					Suggestion: "Re-run `azd ai agent doctor`; the state assembly returned nil unexpectedly.",
				}
			}
			if len(state.ConnectionLoadErrors) > 0 {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"failed to load configured connections: %s",
						strings.Join(state.ConnectionLoadErrors, "; "),
					),
					Suggestion: "Fix azure.yaml or its $ref files, then retry " +
						"`azd ai agent doctor`.",
					Details: map[string]any{
						"loadErrors": state.ConnectionLoadErrors,
					},
				}
			}
			if !state.HasConnections {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: no enabled connection services found.",
				}
			}

			endpoint := readProjectEndpoint(prior)
			if endpoint == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: upstream checks did not surface " +
						"FOUNDRY_PROJECT_ENDPOINT in their Details.",
				}
			}
			endpoint, err := synthesis.ValidateFoundryProjectEndpoint(endpoint)
			if err != nil {
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("invalid FOUNDRY_PROJECT_ENDPOINT: %s", err),
					Suggestion: "Set FOUNDRY_PROJECT_ENDPOINT to a valid Azure Foundry project endpoint.",
				}
			}
			parsed, _ := url.Parse(endpoint)
			account := strings.TrimSuffix(parsed.Hostname(), ".services.ai.azure.com")
			project := strings.TrimPrefix(strings.TrimPrefix(parsed.Path, "/api"), "/projects/")

			probe := deps.probeFoundryConnections
			if probe == nil {
				probe = realProbeFoundryConnections
			}

			probeCtx, cancel := context.WithTimeout(ctx, foundryConnectionsProbeTimeout)
			defer cancel()

			connections, err := probe(probeCtx, endpoint)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not list connections under project %s (%s).",
						project, err),
					Suggestion: "Retry `azd ai agent doctor`. If the error persists, " +
						"verify network reachability to Foundry and that your azd " +
						"login has read access to the project.",
				}
			}

			return classifyConnections(state.Connections, connections, account, project)
		},
	}
}

// classifyConnections produces the Pass/Fail Result by joining the supported
// azure.yaml `state.Connections` to the connection names returned by
// the Foundry project. Match is on connection name only — credential
// type / target compatibility surfaces at runtime.
//
// `account` / `project` are endpoint identities used for diagnostics.
func classifyConnections(
	refs []nextstep.ResourceRef,
	foundryConnections []string,
	account, project string,
) Result {
	existing := make(map[string]struct{}, len(foundryConnections))
	for _, name := range foundryConnections {
		existing[name] = struct{}{}
	}

	type missingEntry struct {
		Name        string `json:"name"`
		ServiceName string `json:"service"`
		Detail      string `json:"detail,omitempty"`
	}

	var missing []missingEntry
	matched := 0
	for _, ref := range refs {
		if _, ok := existing[ref.Name]; ok {
			matched++
			continue
		}
		missing = append(missing, missingEntry{
			Name:        ref.Name,
			ServiceName: ref.ServiceName,
			Detail:      ref.Detail,
		})
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Name != missing[j].Name {
			return missing[i].Name < missing[j].Name
		}
		return missing[i].ServiceName < missing[j].ServiceName
	})

	if len(missing) == 0 {
		return Result{
			Status: StatusPass,
			Message: fmt.Sprintf(
				"all %d referenced connection(s) present on project %s.",
				matched, project),
			Details: map[string]any{
				"matchedCount": matched,
				"account":      account,
				"project":      project,
			},
		}
	}

	var sb strings.Builder
	for i, m := range missing {
		if i > 0 {
			sb.WriteString(", ")
		}
		if m.Detail != "" {
			sb.WriteString(fmt.Sprintf("%s [%s] (service %s)", m.Name, m.Detail, m.ServiceName))
		} else {
			sb.WriteString(fmt.Sprintf("%s (service %s)", m.Name, m.ServiceName))
		}
	}

	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d configured connection(s) are missing on project %s: %s",
			len(missing), project, sb.String()),
		Suggestion: "Run `azd deploy --all` to create or reconcile the " +
			"missing azure.ai.connection services and add them to agent uses before deploying.",
		Details: map[string]any{
			"missingConnections": missing,
			"matchedCount":       matched,
			"account":            account,
			"project":            project,
		},
	}
}

// realProbeFoundryConnections lists every connection on a Foundry
// project using the same `FoundryProjectsClient.GetAllConnections`
// path that production callers (init / listen) use. The function is
// the production wiring of `foundryConnectionsProbeFn`; tests inject
// a fake via `deps.probeFoundryConnections` so they don't need a
// live azd auth session.
//
// The returned slice contains connection names only; nothing else is
// surfaced because the doctor only needs name-based matching. A
// non-nil error from the client short-circuits with the wrapped
// error; the check classifies any non-nil error as Skip.
func realProbeFoundryConnections(
	ctx context.Context,
	projectEndpoint string,
) ([]string, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}
	return listFoundryConnectionNames(ctx, cred, projectEndpoint)
}

// listFoundryConnectionNames is the credential-injecting variant of
// realProbeFoundryConnections, factored out so tests that supply a
// fake `azcore.TokenCredential` can exercise the client wiring
// without going through `azd auth`. The function pages through every
// connection on the project (via `GetAllConnections`) and returns
// the `Name` field of each.
func listFoundryConnectionNames(
	ctx context.Context,
	credential azcore.TokenCredential,
	projectEndpoint string,
) ([]string, error) {
	client, err := azure.NewFoundryProjectsClient(projectEndpoint, credential)
	if err != nil {
		return nil, fmt.Errorf("create Foundry projects client: %w", err)
	}
	conns, err := client.GetAllConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Foundry connections: %w", err)
	}
	names := make([]string, 0, len(conns))
	for _, c := range conns {
		names = append(names, c.Name)
	}
	return names, nil
}
