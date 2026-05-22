// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// modelDeploymentsProbeTimeout caps the per-account deployments list
// round trip. The doctor remote-checks design budgets 10s per probe
// for one-shot diagnostics (.tmp/pr-8057/azd-ai-agent-doctor-remote
// -checks.md). Deployments lists complete in well under a second in
// practice; the ceiling exists so a stuck VPN or transient ARM hiccup
// surfaces as a clean Skip rather than dragging the whole doctor run.
const modelDeploymentsProbeTimeout = 10 * time.Second

// modelDeploymentProbeFn is the seam-friendly signature for the
// deployments list probe. The closure receives a per-account ARM
// scope (subscription + resourceGroup + accountName) and returns
// the deployment names that exist under it. Errors short-circuit
// the check to Skip — we can not distinguish "deployment missing"
// from "ARM unreachable" without a successful round trip, and
// surfacing a noisy classification on every transient failure is
// worse than a single Skip with the underlying error verbatim.
type modelDeploymentProbeFn func(
	ctx context.Context,
	subscriptionID, resourceGroup, accountName string,
) ([]string, error)

// newCheckModelDeployments produces Check `remote.model-deployments`
// (P5.1 C13). For each `ModelResource` declared in any service's
// `agent.manifest.yaml` (collected by the C2 manifest walker), the
// check queries the Foundry project's underlying Cognitive Services
// account for the matching deployment name. The check Passes when
// every manifest-declared model has a corresponding deployment;
// Fails when one or more deployments are missing.
//
// # Skip cascade
//
//   - deps.AzdClient nil → upstream `local.grpc-extension` already
//     surfaced the actionable error.
//   - `local.environment-selected` failed/skipped → nothing to read
//     state from.
//   - `local.azure-yaml` or `local.agent-service-detected` failed →
//     no services to walk; would Pass falsely if we forged ahead.
//   - `remote.auth` failed → ARM probe would 401 identically; let
//     the auth check own the diagnosis.
//   - `remote.foundry-endpoint` failed → same root cause, same
//     remediation.
//   - state.HasModels == false → no manifest model declarations;
//     the check has nothing to verify. Surface as Skip with a
//     short explanation rather than a vacuous Pass.
//   - `AZURE_AI_PROJECT_ID` not set / cannot be parsed → can not derive
//     the ARM scope to probe. Skip cleanly; the rbac check already
//     emits the canonical `azd env set AZURE_AI_PROJECT_ID ...`
//     suggestion for the same root cause.
//
// # Aggregation
//
// Deployments live at the Cognitive Services *account* level, not
// the project. The walker may surface ModelRefs from multiple
// services, but every service in an azd project belongs to the same
// Foundry project (and therefore the same account), so the check
// issues exactly one deployments list per run.
//
// # Classification
//
//   - All ModelRefs match a deployment name → Pass with the matched
//     count.
//   - One or more missing → Fail with the missing names listed in
//     the Message and structured under `Details["missingModels"]`.
//   - Probe error → Skip with the underlying error verbatim.
func newCheckModelDeployments(deps Dependencies) Check {
	return Check{
		ID:     "remote.model-deployments",
		Name:   "Manifest model deployments exist in Foundry",
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
			state, errs := assembler(ctx, deps.AzdClient)
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
			if !state.HasModels {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: no model resources declared in any service's agent.manifest.yaml.",
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

			sub, rg, account, err := parseAccountFromProjectID(projectID)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not parse account from %s (%s).",
						projectIDVar, err),
				}
			}

			probe := deps.probeModelDeployments
			if probe == nil {
				probe = realProbeModelDeployments
			}

			probeCtx, cancel := context.WithTimeout(ctx, modelDeploymentsProbeTimeout)
			defer cancel()

			deployments, err := probe(probeCtx, sub, rg, account)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not list deployments under account %s (%s).",
						account, err),
					Suggestion: "Retry `azd ai agent doctor`. If the error persists, " +
						"verify network reachability to ARM and that your azd login " +
						"has read access to the Cognitive Services account.",
				}
			}

			return classifyModelDeployments(state.ModelRefs, deployments, account)
		},
	}
}

// parseAccountFromProjectID extracts (subscription, resourceGroup,
// accountName) from a Foundry project ARM resource ID of the form
//
//	/subscriptions/<sub>/resourceGroups/<rg>/providers/
//	  Microsoft.CognitiveServices/accounts/<account>/projects/<project>
//
// The parser is intentionally case-insensitive on segment markers
// because ARM occasionally normalizes casing on round-trip. Missing
// any of the three segments returns an error; the check surfaces
// that as Skip with an actionable message pointing at the rbac
// check (which owns the canonical AZURE_AI_PROJECT_ID guidance).
func parseAccountFromProjectID(projectID string) (sub, rg, account string, err error) {
	parts := strings.Split(projectID, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "subscriptions":
			sub = parts[i+1]
		case "resourcegroups":
			rg = parts[i+1]
		case "accounts":
			account = parts[i+1]
		}
	}
	if sub == "" || rg == "" || account == "" {
		return "", "", "", fmt.Errorf("missing subscription / resourceGroup / account in %q", projectID)
	}
	return sub, rg, account, nil
}

// classifyModelDeployments produces the Pass/Fail Result by joining
// the manifest's `state.ModelRefs` to the deployments listed under
// the Foundry account. The match is on deployment name only —
// version compatibility surfaces at runtime, not in the doctor.
//
// `account` is forwarded only for human-readable strings; redaction
// is not applied because the account name is the same value the
// user typed into `azd env set AZURE_AI_PROJECT_ID` and is not
// considered sensitive (the project ARN it is parsed from already
// shows up in other doctor checks).
func classifyModelDeployments(refs []nextstep.ResourceRef, deployments []string, account string) Result {
	deployed := make(map[string]struct{}, len(deployments))
	for _, name := range deployments {
		deployed[name] = struct{}{}
	}

	type missingEntry struct {
		Name        string `json:"name"`
		ServiceName string `json:"service"`
	}

	var missing []missingEntry
	matched := 0
	for _, ref := range refs {
		if _, ok := deployed[ref.Name]; ok {
			matched++
			continue
		}
		missing = append(missing, missingEntry{Name: ref.Name, ServiceName: ref.ServiceName})
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
			Message: fmt.Sprintf("all %d referenced model deployment(s) present on account %s.",
				matched, account),
			Details: map[string]any{
				"matchedCount": matched,
				"account":      account,
			},
		}
	}

	var sb strings.Builder
	for i, m := range missing {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s (service %s)", m.Name, m.ServiceName))
	}

	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf(
			"%d model deployment(s) referenced by agent.manifest.yaml are missing on account %s: %s",
			len(missing), account, sb.String()),
		Suggestion: "Run `azd provision` to create the missing deployment(s), " +
			"or update the agent.manifest.yaml `resources[].name` entries to " +
			"match deployments that already exist in Foundry.",
		Details: map[string]any{
			"missingModels": missing,
			"matchedCount":  matched,
			"account":       account,
		},
	}
}

// realProbeModelDeployments lists every Cognitive Services
// deployment under (subscription, resourceGroup, accountName) using
// the same `armcognitiveservices.DeploymentsClient.NewListPager`
// path that `internal/cmd/init_foundry_resources_helpers.go`
// (`listProjectDeployments`) uses for the init flow. The function
// is the production wiring of `modelDeploymentProbeFn`; tests inject
// a fake via `deps.probeModelDeployments`.
//
// The returned slice contains deployment names only; nothing else
// is currently surfaced because the doctor only needs name-based
// matching. Pager errors short-circuit the call with the wrapped
// error; the check classifies a non-nil error as Skip.
func realProbeModelDeployments(
	ctx context.Context,
	subscriptionID, resourceGroup, accountName string,
) ([]string, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}
	return listDeploymentNames(ctx, cred, subscriptionID, resourceGroup, accountName)
}

// listDeploymentNames is the credential-injecting variant of
// realProbeModelDeployments, factored out so tests that supply a
// fake `azcore.TokenCredential` can exercise the pager / client
// wiring without going through `azd auth`. The function pages
// through every deployment under the account and returns the
// `Name` field of each.
func listDeploymentNames(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionID, resourceGroup, accountName string,
) ([]string, error) {
	clientOptions := azure.NewArmClientOptions()
	client, err := armcognitiveservices.NewDeploymentsClient(subscriptionID, credential, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("create deployments client: %w", err)
	}
	pager := client.NewListPager(resourceGroup, accountName, nil)
	var names []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
		for _, d := range page.Value {
			if d == nil || d.Name == nil {
				continue
			}
			names = append(names, *d.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}
