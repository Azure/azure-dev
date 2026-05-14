// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// foundryConnectionsProbeTimeout caps the per-project connections
// list round trip. Same 10s budget as model-deployments — the design
// doc allocates that envelope per remote probe so a stuck VPN or
// transient Foundry hiccup never drags the whole doctor run.
const foundryConnectionsProbeTimeout = 10 * time.Second

// foundryConnectionsProbeFn is the seam-friendly signature for the
// project connections list probe. The closure receives the account
// + project identifiers needed by `FoundryProjectsClient` and
// returns the connection names that exist on the project. Errors
// short-circuit the check to Skip — a 401/403/network failure has
// the same surface as "Foundry unreachable" which the upstream
// `remote.foundry-endpoint` already classifies, so silencing here
// keeps the report from emitting two near-identical Fail lines.
type foundryConnectionsProbeFn func(
	ctx context.Context,
	accountName, projectName string,
) ([]string, error)

// newCheckConnections produces Check `remote.connections` (P5.1
// C15). For each `ConnectionResource` declared in any service's
// `agent.manifest.yaml` (collected by the C2 manifest walker), the
// check queries the Foundry project's connection list and verifies a
// connection with the matching name exists. The check Passes when
// every manifest-declared connection has a corresponding entry;
// Fails when one or more are missing.
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
//   - state.HasConnections == false → no manifest connection
//     declarations; the check has nothing to verify. Surface as
//     Skip with a short explanation rather than a vacuous Pass.
//   - `AZURE_AI_PROJECT_ID` not set / cannot be parsed → can not
//     derive the account + project to probe. Skip cleanly; the
//     rbac check already emits the canonical `azd env set` fix.
//
// # Classification
//
//   - Every manifest connection matches a Foundry connection name →
//     Pass with the matched count.
//   - One or more missing → Fail with the missing names listed in
//     the Message and structured under `Details["missingConnections"]`
//     (each entry carries Name, ServiceName, Detail — the manifest's
//     "<Category> | <Target>" identifier surfaced by the C2 walker).
//   - Probe error → Skip with the underlying error verbatim.
func newCheckConnections(deps Dependencies) Check {
	return Check{
		ID:     "remote.connections",
		Name:   "Manifest connections exist on Foundry project",
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

			assembler := deps.assembleState
			if assembler == nil {
				assembler = func(c context.Context, client *azdext.AzdClient) (*nextstep.State, []error) {
					return nextstep.AssembleState(c, client)
				}
			}
			state, _ := assembler(ctx, deps.AzdClient)
			if state == nil || !state.HasConnections {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: no connection resources declared in any service's agent.manifest.yaml.",
				}
			}

			projectIDReader := deps.readProjectResourceIDFn
			if projectIDReader == nil {
				projectIDReader = readProjectResourceID
			}
			projectID, err := projectIDReader(ctx, deps.AzdClient)
			if err != nil || projectID == "" {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: %s is not set in the current azd environment "+
							"(see check `remote.rbac`).", projectIDVar),
				}
			}

			account, project, err := parseAccountProjectFromProjectID(projectID)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not parse account / project from %s (%s).",
						projectIDVar, err),
				}
			}

			probe := deps.probeFoundryConnections
			if probe == nil {
				probe = realProbeFoundryConnections
			}

			probeCtx, cancel := context.WithTimeout(ctx, foundryConnectionsProbeTimeout)
			defer cancel()

			connections, err := probe(probeCtx, account, project)
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

// parseAccountProjectFromProjectID extracts (accountName, projectName)
// from a Foundry project ARM resource ID of the form
//
//	/subscriptions/<sub>/resourceGroups/<rg>/providers/
//	  Microsoft.CognitiveServices/accounts/<account>/projects/<project>
//
// Sibling of `parseAccountFromProjectID` (C13) — left as a separate
// helper so the C13 signature does not churn for a single new
// caller. Both parsers are case-insensitive on segment markers
// because ARM occasionally normalizes casing on round-trip.
func parseAccountProjectFromProjectID(projectID string) (account, project string, err error) {
	parts := strings.Split(projectID, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "accounts":
			account = parts[i+1]
		case "projects":
			project = parts[i+1]
		}
	}
	if account == "" || project == "" {
		return "", "", fmt.Errorf("missing account / project in %q", projectID)
	}
	return account, project, nil
}

// classifyConnections produces the Pass/Fail Result by joining the
// manifest's `state.Connections` to the connection names returned by
// the Foundry project. Match is on connection name only — credential
// type / target compatibility surfaces at runtime.
//
// `account` / `project` are forwarded only for human-readable strings
// in the Message; redaction is not applied because both values are
// the same identifiers the user typed into
// `azd env set AZURE_AI_PROJECT_ID` and are not considered sensitive.
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
			"%d connection(s) referenced by agent.manifest.yaml are missing on project %s: %s",
			len(missing), project, sb.String()),
		Suggestion: "Run `azd provision` to create the missing connection(s), " +
			"or update the agent.manifest.yaml `resources[].name` entries to " +
			"match connections that already exist on the Foundry project.",
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
	accountName, projectName string,
) ([]string, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}
	return listFoundryConnectionNames(ctx, cred, accountName, projectName)
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
	accountName, projectName string,
) ([]string, error) {
	client, err := azure.NewFoundryProjectsClient(accountName, projectName, credential)
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
