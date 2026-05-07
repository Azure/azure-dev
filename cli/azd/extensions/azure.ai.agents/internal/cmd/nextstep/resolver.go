// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"fmt"
	"strings"
)

// ResolveAfterRun returns suggestions to print before/around `azd ai
// agent run` startup. Because the agent process is long-lived, the
// caller prints these suggestions before invoking the child process.
//
// When examplePayload is non-empty, it is used verbatim (already
// JSON-encoded). Otherwise we fall back to a generic "Hello!" payload.
//
// serviceName scopes the description for multi-agent projects.
func ResolveAfterRun(s *State, serviceName string, examplePayload string) []Suggestion {
	if s == nil {
		s = &State{}
	}
	payload := strings.TrimSpace(examplePayload)
	if payload == "" {
		payload = `"Hello!"`
	}
	desc := "send your first request once the agent is listening"
	if serviceName != "" {
		desc = fmt.Sprintf("send your first request to %s once it is listening", serviceName)
	}
	return []Suggestion{{
		Command:     fmt.Sprintf("azd ai agent invoke --local %s", payload),
		Description: desc,
	}}
}

// ResolveAfterInvokeLocal returns suggestions to show after a
// successful `azd ai agent invoke --local ...` call.
//
// The decision tree (from issue #7975):
//
//	deploy + monitor (single agent)
//	deploy + monitor per-agent (multi-agent)
//
// `monitor` is offered only when AZURE_AI_PROJECT_ENDPOINT is set —
// without a project endpoint there's nothing to monitor against.
func ResolveAfterInvokeLocal(s *State) []Suggestion {
	if s == nil {
		return nil
	}

	var out []Suggestion
	out = append(out, Suggestion{
		Command:     "azd deploy",
		Description: "package and publish the agent to Foundry",
	})

	if s.HasProjectEndpoint && s.PrimaryAgent() != nil {
		// Single-agent: emit one monitor command (simplest and most
		// common case). Multi-agent monitor commands are emitted
		// after deploy, not after a local invoke.
		out = append(out, Suggestion{
			Command:     "azd ai agent monitor --follow",
			Description: "stream live invocation logs once deployed",
		})
	}
	return out
}

// ResolveAfterInvokeRemote returns suggestions to show after a
// successful remote `azd ai agent invoke <agent>` call.
//
// agentName is the user-visible Foundry agent name (or service name
// when multi-agent disambiguation is needed). When empty, the
// suggestions fall back to the unqualified `show` form.
func ResolveAfterInvokeRemote(s *State, agentName string) []Suggestion {
	showCmd := "azd ai agent show"
	if name := strings.TrimSpace(agentName); name != "" {
		showCmd = fmt.Sprintf("azd ai agent show %s", name)
	}

	out := []Suggestion{{
		Command:     showCmd,
		Description: "inspect agent status, version, and metadata",
	}}

	if s != nil && s.HasProjectEndpoint {
		out = append(out, Suggestion{
			Command:     "azd ai agent monitor --follow",
			Description: "stream live invocation logs",
		})
	}
	return out
}

// ResolveAfterShow returns suggestions to show after `azd ai agent
// show`, based on the agent's status as reported by Foundry.
//
// Statuses follow the Foundry agent state vocabulary:
//   - "active", "idle", "ready", "succeeded", "" -> invoke is the
//     happy path
//   - "failed", "error" -> point at monitor for diagnostics
//   - "deploying", "creating", "updating", "transitional" -> retry
//     show
//
// Unknown statuses fall through to the happy path so we never
// dead-end the user.
func ResolveAfterShow(s *State, agentName, agentStatus string) []Suggestion {
	status := strings.ToLower(strings.TrimSpace(agentStatus))

	switch status {
	case "failed", "error":
		out := []Suggestion{{
			Command:     "azd ai agent monitor --follow",
			Description: "stream logs to diagnose the failed deployment",
		}}
		return out
	case "deploying", "creating", "updating", "transitional", "pending":
		showCmd := "azd ai agent show"
		if name := strings.TrimSpace(agentName); name != "" {
			showCmd = fmt.Sprintf("azd ai agent show %s", name)
		}
		return []Suggestion{{
			Command:     showCmd,
			Description: "re-run once the agent finishes transitioning",
		}}
	}

	// Happy path: invoke + monitor.
	invokeCmd := "azd ai agent invoke \"Hello!\""
	if name := strings.TrimSpace(agentName); name != "" {
		invokeCmd = fmt.Sprintf("azd ai agent invoke %s \"Hello!\"", name)
	}
	out := []Suggestion{{
		Command:     invokeCmd,
		Description: "test the agent end-to-end",
	}}
	if s != nil && s.HasProjectEndpoint {
		out = append(out, Suggestion{
			Command:     "azd ai agent monitor --follow",
			Description: "stream live invocation logs",
		})
	}
	return out
}

// ResolveAfterDeployOne returns suggestions to print after a single
// agent deploy completes. Unlike ResolveAfterDeploy (which fans out
// over all agents in the project state), this resolver is scoped to
// the agent that was just deployed.
//
// agentName should be the deployed Foundry name when known; otherwise
// pass the azure.yaml service name. An empty string falls back to the
// unscoped invoke form ("azd ai agent invoke").
//
// Convention: in the single-agent case “invoke“ is emitted without
// the agent name (matches the README sample) since there is only one
// agent to target. “show“ keeps the name so users can copy-paste the
// command unambiguously.
func ResolveAfterDeployOne(agentName string) []Suggestion {
	name := strings.TrimSpace(agentName)
	if name == "" {
		return []Suggestion{
			{Command: "azd ai agent show", Description: "inspect agent status, version, and metadata"},
			{Command: "azd ai agent invoke <payload>", Description: "test the deployed agent end-to-end"},
			{Command: "azd ai agent monitor --follow", Description: "stream live invocation logs"},
		}
	}
	return []Suggestion{
		{
			Command:     fmt.Sprintf("azd ai agent show %s", name),
			Description: "inspect agent status, version, and metadata",
		},
		{
			Command:     "azd ai agent invoke <payload>",
			Description: "test the deployed agent end-to-end",
		},
		{
			Command:     "azd ai agent monitor --follow",
			Description: "stream live invocation logs",
		},
	}
}

// ResolveAfterDeploy returns suggestions to show after a successful
// post-deploy hook for the agent service target.
//
// For a single agent, it suggests show + invoke. For multi-agent
// projects, it lists one show command per deployed agent so the user
// can pick.
func ResolveAfterDeploy(s *State) []Suggestion {
	if s == nil || len(s.AgentServices) == 0 {
		return nil
	}

	if primary := s.PrimaryAgent(); primary != nil {
		name := agentDisplayName(primary)
		showCmd := "azd ai agent show"
		invokeCmd := "azd ai agent invoke \"Hello!\""
		if name != "" {
			showCmd = fmt.Sprintf("azd ai agent show %s", name)
			invokeCmd = fmt.Sprintf("azd ai agent invoke %s \"Hello!\"", name)
		}
		return []Suggestion{
			{Command: showCmd, Description: "inspect the deployed agent"},
			{Command: invokeCmd, Description: "test it end-to-end"},
		}
	}

	// Multi-agent: emit one show command per service so the user can
	// pick which to drill into.
	out := make([]Suggestion, 0, len(s.AgentServices))
	for i := range s.AgentServices {
		svc := &s.AgentServices[i]
		name := agentDisplayName(svc)
		if name == "" {
			continue
		}
		out = append(out, Suggestion{
			Command:     fmt.Sprintf("azd ai agent show %s", name),
			Description: fmt.Sprintf("inspect the %q agent", svc.ServiceName),
		})
	}
	return out
}

// agentDisplayName returns the most user-friendly identifier for an
// agent — the deployed Foundry name when known, otherwise the
// azure.yaml service name.
func agentDisplayName(svc *ServiceState) string {
	if svc == nil {
		return ""
	}
	if svc.DeployedName != "" {
		return svc.DeployedName
	}
	return svc.ServiceName
}

// ResolveAfterInit returns suggestions to show after a successful
// `azd ai agent init` (or `init from-code`).
//
// The decision tree (from issue #7975):
//
//	IF HasUnresolvedInfraVars      -> azd provision
//	ELSE IF HasUnresolvedManualVars -> azd env set <KEY> <value> (one per var)
//	ELSE                           -> azd ai agent run + azd ai agent invoke --local "Hello!"
//
// We approximate "HasUnresolvedInfraVars" with `!HasProjectEndpoint` —
// when AZURE_AI_PROJECT_ENDPOINT is missing, infra has not been
// provisioned yet, so `azd provision` is the right next step.
//
// serviceName is the azure.yaml service key just added by `init`. It
// scopes the run/invoke suggestions where helpful. Pass an empty
// string to emit the unscoped form.
func ResolveAfterInit(s *State, serviceName string) []Suggestion {
	if s == nil {
		s = &State{}
	}

	// Stage 1: infra not provisioned yet.
	if !s.HasProjectEndpoint {
		return []Suggestion{{
			Command:     "azd provision",
			Description: "provision the Azure resources required to deploy and run your agent",
		}}
	}

	// Stage 2: infra is up but the user still has manual env vars to fill.
	if len(s.UnresolvedManualVars) > 0 {
		out := make([]Suggestion, 0, len(s.UnresolvedManualVars))
		for _, v := range s.UnresolvedManualVars {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("azd env set %s <value>", v),
				Description: fmt.Sprintf("provide a value for the %s setting", v),
			})
		}
		return out
	}

	// Stage 3: ready to run + invoke locally, then deploy to Foundry.
	runCmd := "azd ai agent run"
	if serviceName != "" {
		runCmd = fmt.Sprintf("azd ai agent run %s", serviceName)
	}
	return []Suggestion{
		{Command: runCmd, Description: "start the agent locally"},
		{
			Command:     "azd ai agent invoke --local <payload>",
			Description: "test it in another terminal (use a payload appropriate for your agent)",
		},
		{
			Command:     "azd deploy",
			Description: "ship the agent to Microsoft Foundry once local testing succeeds",
		},
	}
}
