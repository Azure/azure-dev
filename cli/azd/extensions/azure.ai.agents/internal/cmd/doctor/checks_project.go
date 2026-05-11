// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"gopkg.in/yaml.v3"
)

// agentHost is the value used in azure.yaml for an azure.ai.agent service.
// Must stay in sync with cmd.AiAgentHost ("azure.ai.agent") in
// `internal/cmd/init.go`; duplicated here so the doctor package does not
// have to import cmd (which would form an import cycle once the Cobra
// wiring lands in Phase 4.4).
const agentHost = "azure.ai.agent"

// projectEndpointVar is the azd environment variable that points at the
// Foundry project. Must stay in sync with the rest of the extension
// (`agent_context.go`, `listen.go`, `service_target_agent.go`).
const projectEndpointVar = "AZURE_AI_PROJECT_ENDPOINT"

// newCheckAgentServiceDetected produces Check `local.agent-service-detected`.
// It re-fetches the project config and counts services whose `host` is
// `azure.ai.agent`. Pass surfaces the count and service names so users
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
			if priorFailed(prior, "local.azure-yaml") {
				return Result{Status: StatusSkip, Message: "skipped: azure.yaml check failed"}
			}

			resp, err := deps.AzdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				suggestion := "Run `azd ai agent init` to add an azure.ai.agent service to azure.yaml."
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
					Message:    "no `azure.ai.agent` service found in azure.yaml",
					Suggestion: "Run `azd ai agent init` to add an azure.ai.agent service to azure.yaml.",
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
// It reads `AZURE_AI_PROJECT_ENDPOINT` from the currently-selected azd
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
		Name: "AZURE_AI_PROJECT_ENDPOINT set",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{Status: StatusSkip, Message: "skipped: azd extension not reachable"}
			}
			if priorFailed(prior, "local.environment-selected") {
				return Result{Status: StatusSkip, Message: "skipped: environment check failed"}
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

// newCheckAgentYAMLValid produces Check `local.agent-yaml-valid`. For
// each agent service in azure.yaml, it reads `<projectPath>/<svc.RelativePath>/agent.yaml`
// and parses it as `agent_yaml.ContainerAgent`. Fails when any service's
// file is missing, unreadable, or fails to parse — collecting all errors
// rather than short-circuiting so multi-service projects get a single
// actionable report.
//
// Skips when the gRPC client is unavailable or when
// `local.agent-service-detected` failed (no services to validate). The
// suggestion mirrors the spec's "fix YAML" guidance.
func newCheckAgentYAMLValid(deps Dependencies) Check {
	return Check{
		ID:   "local.agent-yaml-valid",
		Name: "agent.yaml valid (per service)",
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{Status: StatusSkip, Message: "skipped: azd extension not reachable"}
			}
			if priorFailed(prior, "local.agent-service-detected") {
				return Result{Status: StatusSkip, Message: "skipped: no agent services detected"}
			}

			resp, err := deps.AzdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				suggestion := "Run from a directory containing `azure.yaml`, or initialize one with `azd init`."
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

			projectPath := resp.Project.Path
			// Collect agent service entries in a stable order. protobuf
			// `Services` is a map, so iteration order is non-deterministic
			// — sorting by service name keeps the failure list (and the
			// validatedPaths Detail) reproducible.
			type agentSvc struct {
				name string
				rel  string
			}
			var agents []agentSvc
			for _, s := range resp.Project.Services {
				if s == nil || s.Host != agentHost {
					continue
				}
				agents = append(agents, agentSvc{name: s.Name, rel: s.RelativePath})
			}
			sort.Slice(agents, func(i, j int) bool { return agents[i].name < agents[j].name })

			var validatedPaths []string
			var failures []string
			for _, a := range agents {
				yamlPath := filepath.Join(projectPath, a.rel, "agent.yaml")
				if pathErr := validateAgentYAML(yamlPath); pathErr != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", a.name, pathErr))
					continue
				}
				validatedPaths = append(validatedPaths, yamlPath)
			}

			if len(failures) > 0 {
				return Result{
					Status: StatusFail,
					Message: fmt.Sprintf(
						"agent.yaml validation failed for %d service(s): %s",
						len(failures), strings.Join(failures, "; ")),
					Suggestion: "Fix the YAML syntax or ensure agent.yaml exists in each service directory.",
					Details: map[string]any{
						"failures":       failures,
						"validatedPaths": validatedPaths,
					},
				}
			}

			return Result{
				Status:  StatusPass,
				Message: fmt.Sprintf("agent.yaml valid for %d service(s)", len(validatedPaths)),
				Details: map[string]any{
					"validatedPaths": validatedPaths,
				},
			}
		},
	}
}

// validateAgentYAML reads the file at path and ensures it parses as a
// ContainerAgent. Returns the underlying read/parse error verbatim so
// the caller can attribute it to the offending service.
func validateAgentYAML(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from azd-resolved project root + service-relative path
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var parsed agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// priorFailed reports whether the prior results contain a Fail entry
// for the given check ID. Used for skip-cascade decisions across the
// local-checks chain.
func priorFailed(prior []Result, id string) bool {
	for _, p := range prior {
		if p.ID == id && p.Status == StatusFail {
			return true
		}
	}
	return false
}
