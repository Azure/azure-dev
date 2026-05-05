// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"fmt"
	"strings"
)

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
