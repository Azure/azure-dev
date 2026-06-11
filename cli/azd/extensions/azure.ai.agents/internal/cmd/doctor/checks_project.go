// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// agentHost is the value used in azure.yaml for a Foundry service under the
// unified design. Duplicated here so the doctor package does not have to
// import cmd (which would form an import cycle).
const agentHost = "microsoft.foundry"

// projectEndpointVar is the azd environment variable that points at the
// Foundry project. Must stay in sync with the rest of the extension
// (`agent_context.go`, `listen.go`, `service_target_agent.go`).
const projectEndpointVar = "FOUNDRY_PROJECT_ENDPOINT"

// newCheckAgentServiceDetected produces Check `local.agent-service-detected`.
// It re-fetches the project config and counts services whose `host` is
// `microsoft.foundry`. Pass surfaces the count and service names so users
// can verify the doctor saw what they expected; Fail tells them to run
// `azd ai agent init` to scaffold one. Skips when the gRPC client is
// unavailable or when `local.azure-yaml` failed.
func newCheckAgentServiceDetected(deps Dependencies) Check {
	return Check{
		ID:   "local.agent-service-detected",
		Name: "agent service in azure.yaml",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{Status: StatusSkip, Message: "skipped: azd extension not reachable"}
			}
			if priorBlocked(prior, "local.azure-yaml") {
				return Result{Status: StatusSkip, Message: "skipped: azure.yaml check failed"}
			}

			resp, err := deps.AzdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				suggestion := "Run `azd ai agent init` to add a microsoft.foundry service to azure.yaml."
				if isTransportFailure(err) {
					suggestion = "Re-run via `azd ai agent doctor`; the extension cannot reach azd's gRPC channel."
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to get project config: %v", err),
					Suggestion: suggestion,
				}
			}
			if resp == nil || resp.Project == nil {
				return Result{
					Status:     StatusFail,
					Message:    "failed to get project config (is there an azure.yaml?)",
					Suggestion: "Run from a directory containing `azure.yaml`, or initialize one with `azd init`.",
				}
			}

			var agentServices []string
			for _, s := range resp.Project.Services {
				if s != nil && s.Host == agentHost {
					agentServices = append(agentServices, s.Name)
				}
			}
			// Sort for deterministic display: protobuf Services is a map,
			// so iteration order is unstable across runs.
			sort.Strings(agentServices)
			if len(agentServices) == 0 {
				return Result{
					Status:     StatusFail,
					Message:    "no `microsoft.foundry` service found in azure.yaml",
					Suggestion: "Run `azd ai agent init` to add a microsoft.foundry service to azure.yaml.",
				}
			}
			return Result{
				Status: StatusPass,
				Message: fmt.Sprintf(
					"%d agent service(s) in azure.yaml: %s",
					len(agentServices), strings.Join(agentServices, ", ")),
				Details: map[string]any{
					"agentServices":     agentServices,
					"agentServiceCount": len(agentServices),
				},
			}
		},
	}
}

// newCheckProjectEndpointSet produces Check `local.project-endpoint-set`.
// It reads `FOUNDRY_PROJECT_ENDPOINT` from the currently-selected azd
// environment via the EnvironmentService gRPC. An empty EnvName in
// GetEnvRequest defaults to the current environment, so this check does
// not need to re-resolve the environment name itself.
//
// Skips when the gRPC client is unavailable or when
// `local.environment-selected` failed. Fails when the value is missing
// or empty, telling users to run `azd provision` (the production path)
// or `azd env set` (for pointing at an existing project).
func newCheckProjectEndpointSet(deps Dependencies) Check {
	return Check{
		ID:   "local.project-endpoint-set",
		Name: "FOUNDRY_PROJECT_ENDPOINT set",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{Status: StatusSkip, Message: "skipped: azd extension not reachable"}
			}
			if priorBlocked(prior, "local.environment-selected") {
				return Result{Status: StatusSkip, Message: "skipped: environment check failed or skipped"}
			}

			resp, err := deps.AzdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				Key: projectEndpointVar,
			})
			if err != nil {
				suggestion := fmt.Sprintf(
					"Run `azd provision` to create the Foundry project, or `azd env set %s <https://...>` to point at an existing one.",
					projectEndpointVar)
				if isTransportFailure(err) {
					suggestion = "Re-run via `azd ai agent doctor`; the extension cannot reach azd's gRPC channel."
				}
				return Result{
					Status:     StatusFail,
					Message:    fmt.Sprintf("failed to read %s: %v", projectEndpointVar, err),
					Suggestion: suggestion,
				}
			}
			if resp == nil || strings.TrimSpace(resp.Value) == "" {
				return Result{
					Status:  StatusFail,
					Message: fmt.Sprintf("%s is not set in the current azd environment", projectEndpointVar),
					Suggestion: fmt.Sprintf(
						"Run `azd provision` to create the Foundry project, or `azd env set %s <https://...>` to point at an existing one.",
						projectEndpointVar),
				}
			}
			return Result{
				Status:  StatusPass,
				Message: fmt.Sprintf("%s = %s", projectEndpointVar, resp.Value),
				Details: map[string]any{
					"projectEndpoint": resp.Value,
				},
			}
		},
	}
}

// priorBlocked reports whether the prior results contain a Fail or Skip
// entry for the given check ID. Used for skip-cascade decisions across
// the local-checks chain: when an upstream check is skipped (e.g.
// because *its* upstream failed), downstream checks must also skip
// rather than running on a broken-state assumption — otherwise users
// see misleading remediation for the wrong root cause.
func priorBlocked(prior []Result, id string) bool {
	for _, p := range prior {
		if p.ID == id && (p.Status == StatusFail || p.Status == StatusSkip) {
			return true
		}
	}
	return false
}
