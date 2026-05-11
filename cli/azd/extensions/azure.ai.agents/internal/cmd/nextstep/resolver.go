// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"fmt"
	"slices"
	"strings"
)

// Default-payload literals used when the resolver cannot derive a sample
// payload from the agent's OpenAPI spec. Two protocols are recognized;
// anything else falls back to ProtocolDefaultPayload.
const (
	// ProtocolInvocations is the value of `agent.yaml#protocol` for
	// JSON-body /invocations agents.
	ProtocolInvocations = "invocations"
	// ProtocolResponses is the value of `agent.yaml#protocol` for plain
	// text /responses agents.
	ProtocolResponses = "responses"

	invokeInvocationsPayload = `'{"message": "Hello!"}'`
	invokeResponsesPayload   = `"Hello!"`

	// maxManualVarLines caps the number of `azd env set` hints emitted by
	// ResolveAfterInit so the block stays scannable even when an agent
	// declares many manual variables.
	maxManualVarLines = 3
)

// ResolveAfterInit produces the Next: block printed at the end of a
// successful `azd ai agent init`. Pure function over *State.
//
// Decision tree:
//   - MissingInfraVars   → `azd provision` (then "run `azd ai agent run` to
//     start locally" tail line)
//   - MissingManualVars  → one `azd env set <KEY> <value>` per missing var
//     (up to maxManualVarLines)
//   - Otherwise          → `azd ai agent run`
//
// All paths append the static "When ready to deploy to Azure…" tail.
func ResolveAfterInit(state *State) []Suggestion {
	if state == nil {
		return nil
	}

	out := make([]Suggestion, 0, 4)

	switch {
	case len(state.MissingInfraVars) > 0:
		out = append(out, Suggestion{
			Command:     "azd provision",
			Description: "set up your Foundry project, models, and connections",
			Priority:    10,
		})
	case len(state.MissingManualVars) > 0:
		manual := slices.Clone(state.MissingManualVars)
		slices.Sort(manual)
		limit := min(len(manual), maxManualVarLines)
		for i, key := range manual[:limit] {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("azd env set %s <value>", key),
				Description: "supply the agent.yaml variable",
				Priority:    20 + i,
			})
		}
	default:
		out = append(out, Suggestion{
			Command:     "azd ai agent run",
			Description: "start the agent locally",
			Priority:    10,
		})
	}

	out = append(out, Suggestion{
		Command:     "azd deploy",
		Description: "when ready to deploy to Azure",
		Priority:    90,
	})

	return out
}

// ResolveAfterRun produces the Next: block printed when the running
// agent first responds to its OpenAPI probe. Pure function over *State.
//
// Decision tree:
//   - HasOpenAPI + OpenAPIPayload non-empty → invoke with extracted payload
//   - ServiceState.Protocol == ProtocolInvocations → invoke with {"message"…}
//   - Otherwise (ProtocolResponses or unknown) → invoke with "Hello!"
//
// When the resolver wanted a richer payload but could not extract one
// (HasOpenAPI=false), the Tip suggestion is appended so the user knows
// where to look up the agent's exact contract.
func ResolveAfterRun(state *State, serviceName string) []Suggestion {
	if state == nil {
		return nil
	}

	svc := findService(state, serviceName)
	payload := defaultInvokePayload(svc)
	if state.HasOpenAPI && state.OpenAPIPayload != "" {
		payload = "'" + state.OpenAPIPayload + "'"
	}

	out := []Suggestion{{
		Command:     fmt.Sprintf("azd ai agent invoke --local %s", payload),
		Description: "send a sample request to the running agent",
		Priority:    10,
	}}

	if !state.HasOpenAPI {
		out = append(out, Suggestion{
			Command:     "curl http://localhost:<port>/invocations/docs/openapi.json",
			Description: "tip: inspect the spec to learn the agent's exact payload",
			Priority:    20,
		})
	}

	return out
}

// InvokeMode selects the invoke variant the user just ran.
type InvokeMode int

const (
	// InvokeLocal is `azd ai agent invoke --local`.
	InvokeLocal InvokeMode = iota
	// InvokeRemote is the hosted-agent variant.
	InvokeRemote
)

// InvokeFailure describes a hosted-agent invoke failure for the resolver
// to branch on. SessionCode is the value of the `x-adc-response-details`
// header (or equivalent); empty means "not classified by the platform".
type InvokeFailure struct {
	SessionCode SessionErrorCode
}

// ResolveAfterInvoke produces the Next: block for a completed invoke.
//
// Success paths:
//   - InvokeLocal  → `azd deploy` (the natural next step is to ship)
//   - InvokeRemote → `azd ai agent show <agent>` + monitor secondary
//
// Failure paths:
//   - InvokeLocal  → single generic line ("see local server output")
//   - InvokeRemote → branched on InvokeFailure.SessionCode via the
//     error_codes vocabulary; unclassified failures get the monitor
//     fallback.
func ResolveAfterInvoke(state *State, mode InvokeMode, agentName string, failure *InvokeFailure) []Suggestion {
	if failure == nil {
		return resolveInvokeSuccess(mode, agentName)
	}
	return resolveInvokeFailure(state, mode, agentName, failure)
}

func resolveInvokeSuccess(mode InvokeMode, agentName string) []Suggestion {
	if mode == InvokeLocal {
		return []Suggestion{{
			Command:     "azd deploy",
			Description: "the local invoke worked — ship it to Azure",
			Priority:    10,
		}}
	}

	primary := "azd ai agent show"
	if agentName != "" {
		primary = fmt.Sprintf("azd ai agent show %s", agentName)
	}
	return []Suggestion{
		{
			Command:     primary,
			Description: "confirm the deployed agent is healthy",
			Priority:    10,
		},
		{
			Command:     "azd ai agent monitor --follow",
			Description: "stream live logs from the agent",
			Priority:    20,
		},
	}
}

func resolveInvokeFailure(_ *State, mode InvokeMode, _ string, failure *InvokeFailure) []Suggestion {
	if mode == InvokeLocal {
		return []Suggestion{{
			Command:     "see local server output",
			Description: "fix the error in your local agent and retry",
			Priority:    10,
		}}
	}

	if failure.SessionCode == "" {
		return []Suggestion{{
			Command:     "azd ai agent monitor --tail 100",
			Description: "inspect recent container logs for the failure",
			Priority:    10,
		}}
	}

	primary, secondary, ok := RemediationForSessionErrorCode(failure.SessionCode)
	if !ok {
		return []Suggestion{{
			Command:     "azd ai agent monitor --tail 100",
			Description: fmt.Sprintf("session error %q — inspect recent logs", failure.SessionCode),
			Priority:    10,
		}}
	}

	primary.Priority = 10
	out := []Suggestion{primary}
	if secondary != nil {
		s := *secondary
		s.Priority = 20
		out = append(out, s)
	}
	return out
}

// ResolveAfterShow produces the Next: block printed at the end of a
// successful `azd ai agent show`. Branches on State.AgentStatus per the
// platform's `AgentVersionStatus` vocabulary.
func ResolveAfterShow(state *State, agentName string) []Suggestion {
	if state == nil {
		return nil
	}

	switch AgentVersionStatus(state.AgentStatus) {
	case AgentVersionActive:
		return []Suggestion{{
			Command:     invokeCommandFor(agentName, ProtocolResponses),
			Description: "the agent is ready — send it a sample request",
			Priority:    10,
		}}
	case AgentVersionCreating:
		return []Suggestion{{
			Command:     "azd ai agent monitor --type system --follow",
			Description: "deploy is still in progress — watch readiness",
			Priority:    10,
		}}
	case AgentVersionFailed:
		return []Suggestion{{
			Command:     "azd ai agent monitor --tail 100",
			Description: "deploy failed — view the structured error and TSG link above",
			Priority:    10,
		}}
	case AgentVersionDeleting, AgentVersionDeleted:
		return []Suggestion{{
			Command:     "azd deploy",
			Description: "redeploy the agent",
			Priority:    10,
		}}
	}

	// Unknown / transitional / empty — re-check.
	primary := "azd ai agent show"
	if agentName != "" {
		primary = fmt.Sprintf("azd ai agent show %s", agentName)
	}
	return []Suggestion{{
		Command:     primary,
		Description: "status is transitioning — re-check shortly",
		Priority:    10,
	}}
}

// ResolveAfterDeploy produces the Next: block embedded in the post-deploy
// artifact note. The block is rendered per agent service: one
// `azd ai agent show <name>` plus one `azd ai agent invoke '<payload>'`
// line, where the payload is taken from the cached OpenAPI spec when the
// `cachedPayload` lookup yields a non-empty string for the agent.
//
// cachedPayload is injected by the caller (typically a closure over
// ReadCachedOpenAPISpec + ExtractInvokeExample) so the resolver itself
// stays pure and unit-testable.
//
// readmeExists, also injected, controls whether the "See <relPath>/README.md
// for a sample payload" line is appended. The resolver does not touch the
// filesystem directly.
func ResolveAfterDeploy(
	state *State,
	cachedPayload func(serviceName string) string,
	readmeExists func(relativePath string) bool,
) []Suggestion {
	if state == nil || len(state.Services) == 0 {
		return nil
	}

	out := make([]Suggestion, 0, len(state.Services)*3)
	singleAgent := len(state.Services) == 1
	priority := 10

	for _, svc := range state.Services {
		showCmd := "azd ai agent show"
		if !singleAgent {
			showCmd = fmt.Sprintf("azd ai agent show %s", svc.Name)
		}
		out = append(out, Suggestion{
			Command:     showCmd,
			Description: "verify the deployed agent is running",
			Priority:    priority,
		})
		priority++

		payload := ""
		if cachedPayload != nil {
			payload = cachedPayload(svc.Name)
		}
		invokeArg := defaultInvokePayload(&svc)
		if payload != "" {
			invokeArg = "'" + payload + "'"
		}

		invokeCmd := fmt.Sprintf("azd ai agent invoke %s", invokeArg)
		if !singleAgent {
			invokeCmd = fmt.Sprintf("azd ai agent invoke %s %s", svc.Name, invokeArg)
		}
		out = append(out, Suggestion{
			Command:     invokeCmd,
			Description: "send a sample request to the deployed agent",
			Priority:    priority,
		})
		priority++

		if payload == "" && svc.RelativePath != "" && readmeExists != nil && readmeExists(svc.RelativePath) {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("see %s/README.md", strings.TrimPrefix(svc.RelativePath, "./")),
				Description: "sample payload appropriate for this agent",
				Priority:    priority,
			})
			priority++
		}
	}

	return out
}

// findService returns a pointer to the named service in state, or nil.
// When serviceName is empty and there is exactly one service, that one is
// returned — handy for the single-agent default of `azd ai agent run`.
func findService(state *State, serviceName string) *ServiceState {
	if state == nil {
		return nil
	}
	if serviceName == "" {
		if len(state.Services) == 1 {
			return &state.Services[0]
		}
		return nil
	}
	for i := range state.Services {
		if state.Services[i].Name == serviceName {
			return &state.Services[i]
		}
	}
	return nil
}

// defaultInvokePayload returns the protocol-appropriate fallback payload
// string (already quoted) for a service. Unknown protocols and a nil
// service fall back to the /responses-style "Hello!" literal.
func defaultInvokePayload(svc *ServiceState) string {
	if svc != nil && svc.Protocol == ProtocolInvocations {
		return invokeInvocationsPayload
	}
	return invokeResponsesPayload
}

// invokeCommandFor returns `azd ai agent invoke [name] <payload>` for the
// protocol, omitting the name when empty.
func invokeCommandFor(agentName, protocol string) string {
	payload := invokeResponsesPayload
	if protocol == ProtocolInvocations {
		payload = invokeInvocationsPayload
	}
	if agentName == "" {
		return fmt.Sprintf("azd ai agent invoke %s", payload)
	}
	return fmt.Sprintf("azd ai agent invoke %s %s", agentName, payload)
}
