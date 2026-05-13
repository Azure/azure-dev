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

	// maxFixupLines caps the number of `azd env set` / `edit agent.yaml`
	// hints emitted by ResolveAfterInit per missing-input category so the
	// block stays scannable even when an agent declares many manual
	// variables or unresolved placeholders.
	maxFixupLines = 3
)

// ResolveAfterInit produces the Next: block printed at the end of a
// successful `azd ai agent init`. Pure function over *State.
//
// Decision tree:
//   - UnresolvedPlaceholders (always shown first when present, regardless
//     of other branches) → one "edit agent.yaml: replace {{NAME}}" line
//     per unresolved Mustache placeholder (up to maxFixupLines). These
//     are deploy-time landmines: the literal `{{NAME}}` would otherwise
//     land in the container. They never reach `azd env set` because the
//     value lives in agent.yaml itself, not the azd environment.
//   - len(PendingProvisionReasons) > 0 OR !HasProjectEndpoint OR
//     MissingInfraVars → `azd provision`
//     The project endpoint is the canonical "provision finished"
//     marker — it is set by `azd provision` as a Bicep output, or by
//     `azd ai agent init` when the user selects an existing Foundry
//     project. When the endpoint is empty, provision has not yet
//     populated the infra outputs (typical path: user selected
//     "Deploy new models from the catalog" in init), so `azd
//     provision` is the next step regardless of whether agent.yaml
//     directly references any Bicep-output variables.
//     MissingInfraVars is still consulted to cover the
//     post-provision re-provision case (a new `${VAR}` reference
//     mapping to a Bicep output was added to agent.yaml after the
//     last provision run). PendingProvisionReasons is the explicit
//     "init configured something provision still has to materialize"
//     signal — every reason tag (project, model_deployment, acr,
//     app_insights) fires this branch so a stale
//     AZURE_AI_PROJECT_ENDPOINT carried over from a prior init or
//     sibling environment cannot mislead the resolver into
//     suggesting `azd ai agent run`. See state.PendingProvisionReasons
//     for the env-var contract.
//   - MissingManualVars  → one `azd env set <KEY> <value>` per missing var
//     (up to maxFixupLines) plus an `azd ai agent run` follow-up so
//     the user knows what to do after supplying the values. Matches
//     issue #7975's "Then run 'azd ai agent run' to start locally"
//     manual-vars example. The run follow-up is suppressed when
//     UnresolvedPlaceholders are also present, since literal
//     `{{NAME}}` values would still break the local agent.
//   - Otherwise          → `azd ai agent run` + `azd ai agent invoke
//     --local <payload>` secondary
//     Spec: issue #7975 lines 96-103. The invoke-local secondary
//     lets the user test the agent in another terminal once it's
//     running. Payload is protocol-aware when the project has
//     exactly one service in state (the unqualified `invoke --local`
//     resolves to that service). For multi-agent projects the
//     payload defaults to the responses-style `"Hello!"` and the
//     command is left unqualified — the user picks the target at
//     runtime via the interactive prompt or `--service` flag, the
//     same shape the spec example uses.
//     Both lines are skipped when only UnresolvedPlaceholders are
//     present, because running locally with literal `{{NAME}}`
//     values is broken.
//
// All paths append the static "When ready to deploy to Azure…" tail.
func ResolveAfterInit(state *State) []Suggestion {
	if state == nil {
		return nil
	}

	out := make([]Suggestion, 0, 4)
	priority := 5

	// Placeholder fix-ups always come first when present: they are broken
	// state in agent.yaml itself and block both `run` and `deploy`. The
	// user has to edit agent.yaml (or define a matching parameter in
	// agent.manifest.yaml) — `azd env set` cannot reach them.
	hasPlaceholders := len(state.UnresolvedPlaceholders) > 0
	if hasPlaceholders {
		placeholders := slices.Clone(state.UnresolvedPlaceholders)
		slices.Sort(placeholders)
		limit := min(len(placeholders), maxFixupLines)
		for _, name := range placeholders[:limit] {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("edit agent.yaml: replace {{%s}} with the actual value", name),
				Description: "agent.yaml has unresolved manifest placeholders",
				Priority:    priority,
			})
			priority++
		}
	}

	switch {
	case len(state.PendingProvisionReasons) > 0 ||
		!state.HasProjectEndpoint ||
		len(state.MissingInfraVars) > 0:
		out = append(out, Suggestion{
			Command:     "azd provision",
			Description: "set up your Foundry project, models, and connections",
			Priority:    priority,
		})
	case len(state.MissingManualVars) > 0:
		manual := slices.Clone(state.MissingManualVars)
		slices.Sort(manual)
		limit := min(len(manual), maxFixupLines)
		for _, key := range manual[:limit] {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("azd env set %s <value>", key),
				Description: "referenced by agent.yaml but not set in azd env",
				Priority:    priority,
			})
			priority++
		}
		// Follow-up: once the user supplies the values above, the next
		// productive command is `azd ai agent run`. Without this hint
		// the post-init Next: block stops at the env-set lines and the
		// user has to remember the run step themselves — that's the
		// "Then run 'azd ai agent run' to start locally" line in
		// issue #7975's manual-vars example output. Suppressed when
		// placeholders are also unresolved — running locally with
		// literal `{{NAME}}` values produces a broken agent, so the
		// user must finish the placeholder fix-ups first; the
		// trailing `azd deploy` reminder still applies.
		if !hasPlaceholders {
			out = append(out, Suggestion{
				Command:     "azd ai agent run",
				Description: "start the agent locally once the values above are set",
				Priority:    priority,
			})
		}
	case hasPlaceholders:
		// Only unresolved placeholders remain — do not emit
		// `azd ai agent run` because running locally with literal
		// `{{NAME}}` values produces a broken agent. The placeholder
		// fix-ups above already tell the user what to do.
	default:
		out = append(out, Suggestion{
			Command:     "azd ai agent run",
			Description: "start the agent locally",
			Priority:    priority,
		})
		priority++
		// Invoke-local secondary (issue #7975 lines 99-100). The
		// spec's "everything ready" example shows the user a second
		// command to try once the agent is running:
		//   azd ai agent invoke --local "Hello!"  -- test it in another terminal
		// Single-agent projects get a protocol-aware payload (matches
		// the protocol the agent's `/invocations` or `/responses`
		// endpoint expects). Multi-agent projects fall back to the
		// responses-style "Hello!" literal because the unqualified
		// command shape doesn't know which service the user will
		// pick at runtime — mirroring the spec example which also
		// uses the unqualified form.
		invokePayload := invokeResponsesPayload
		if len(state.Services) == 1 {
			invokePayload = defaultInvokePayload(&state.Services[0])
		}
		out = append(out, Suggestion{
			Command:     fmt.Sprintf("azd ai agent invoke --local %s", invokePayload),
			Description: "test it in another terminal",
			Priority:    priority,
		})
	}

	out = append(out, Suggestion{
		Command:     "azd deploy",
		Description: "when ready to deploy to Azure",
		Priority:    90,
		Trailing:    true,
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
		payload = shellEscapeSingleQuoted(state.OpenAPIPayload)
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
//   - InvokeLocal  → `azd deploy` + `azd ai agent monitor --follow`
//     (the local invoke worked, so the next loop is "ship to Azure
//     then watch the live logs". Spec: issue #7975 lines 168-181.)
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
		// Issue #7975 lines 168-181: local-invoke success has run to
		// completion against a local `azd ai agent run` process, so
		// the user has already provisioned (dependencies exist) and
		// the agent code itself works. The natural next step is to
		// ship to Azure with `azd deploy`, and once it's running
		// there, `monitor --follow` is the live-log feed.
		return []Suggestion{
			{
				Command:     "azd deploy",
				Description: "deploy the agent to Azure",
				Priority:    10,
			},
			{
				Command:     "azd ai agent monitor --follow",
				Description: "view logs after deploying",
				Priority:    20,
			},
		}
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
//
// Status mapping (issue #7975 lines 208-214):
//   - active / idle  → `azd ai agent invoke <svc> "Hello!"` (ready to test)
//   - creating       → `azd ai agent monitor --type system --follow`
//   - failed / ""    → `azd ai agent monitor --follow` (live log feed,
//     used to be `--tail 100` pre-C5; spec calls for `--follow` so
//     the user can watch the next reconcile attempt stream live)
//   - deleting / deleted → `azd deploy` (redeploy)
//   - anything else (transitional / genuinely unknown) → `azd ai agent
//     show <svc>` re-check
//
// serviceName is the azure.yaml service name. It is used end-to-end:
// (1) to look up State.Services[].Protocol for the protocol-aware
// payload, (2) as the positional in the suggested
// `azd ai agent invoke <serviceName> ...` command, and (3) as the
// positional in the unknown-status `azd ai agent show <serviceName>`
// re-check fallback.
//
// Critically, the invoke suggestion intentionally uses the azure.yaml
// service name rather than the deployed Foundry agent name. invoke's
// protocol/service resolution keys on azure.yaml service names; the
// invocations/responses remote paths then translate to the deployed
// agent name internally before constructing the Foundry URL (see
// invoke.go gates inside invocationsRemote/responsesRemote). Emitting
// the deployed Foundry name here would fail upstream in
// resolveAgentProtocol with "no azure.ai.agent service named …
// found".
func ResolveAfterShow(state *State, serviceName string) []Suggestion {
	if state == nil {
		return nil
	}

	switch AgentVersionStatus(state.AgentStatus) {
	case AgentVersionActive, AgentVersionIdle:
		// Issue #7975 line 208: `idle` is a defensive synonym for
		// `active`. The platform's verified enum only emits `active`
		// today, but if the API ever surfaces `idle` we treat it the
		// same — both mean "ready to invoke".
		protocol := ProtocolResponses
		if svc := findService(state, serviceName); svc != nil && svc.Protocol != "" {
			protocol = svc.Protocol
		}
		return []Suggestion{{
			Command:     invokeCommandFor(serviceName, protocol, state),
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
		// Issue #7975 line 211: failed status maps to `monitor
		// --follow`. The historical `--tail 100` was useful for
		// one-shot CI inspection but the interactive default is the
		// live tail — by the time `show` surfaces the failure, the
		// user wants to watch the next reconcile attempt stream
		// rather than capture a fixed-size window.
		return []Suggestion{{
			Command:     "azd ai agent monitor --follow",
			Description: "stream agent logs to investigate the failure",
			Priority:    10,
		}}
	case "":
		// Issue #7975 line 210: empty status also routes to `monitor
		// --follow`, but the framing differs from the Failed arm —
		// here the platform simply hasn't reported a Status yet (the
		// `show` table even suppresses the Status row in this case;
		// see show.go printShowResultTable). The most useful next
		// view is the live log feed, but we don't presume a failure
		// occurred.
		return []Suggestion{{
			Command:     "azd ai agent monitor --follow",
			Description: "stream agent logs — status has not been reported yet",
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
	if serviceName != "" {
		primary = fmt.Sprintf("azd ai agent show %s", serviceName)
	}
	return []Suggestion{{
		Command:     primary,
		Description: "status is transitioning — re-check shortly",
		Priority:    10,
	}}
}

// AfterDeployOpts configures ResolveAfterDeploy. Optional — the
// zero-value matches the post-deploy call site behavior.
type AfterDeployOpts struct {
	// ForceQualified is retained for backward compatibility but is
	// effectively a no-op as of issue #7975 fix B9: ResolveAfterDeploy
	// now always emits service-qualified
	// `azd ai agent show <name>` / `invoke <name> ...` commands
	// regardless of how many services are in state.
	//
	// Pre-B9 callers passed ForceQualified=true to override a
	// "single-agent → unqualified command" heuristic that no longer
	// exists. The flag is preserved so existing callers compile and
	// run identically; new callers may simply omit it.
	ForceQualified bool
}

// ResolveAfterDeploy produces the Next: block embedded in the post-deploy
// artifact note. Issue #7975 fix B9 spec (lines 228-242):
//
//   - Single-agent project: emit one `azd ai agent show <name>` line
//     followed by one `azd ai agent invoke <name> '<payload>'` line.
//     Descriptions are "verify it's running" / "test the deployment".
//   - Multi-agent project: emit all `show <name>` lines first (one
//     per service, in declaration order), then all `invoke <name>`
//     lines. Descriptions include the agent name —
//     "verify <name> is running" / "test <name>" — so the user can
//     identify which row maps to which agent at a glance.
//
// In both cases commands are always service-qualified (B9). Pre-B9
// behavior would strip the name when len(state.Services) == 1, which
// produced ambiguous `azd ai agent show` lines in artifact notes that
// users couldn't run directly when copy-pasted into a multi-agent
// project later. The qualified form is unambiguous and copy-paste
// safe in either project shape.
//
// cachedPayload is injected by the caller (typically a closure over
// ReadCachedOpenAPISpec + ExtractInvokeExample) so the resolver itself
// stays pure and unit-testable. The cached sample is used verbatim
// (POSIX-escaped) when present; otherwise the protocol-appropriate
// fallback from defaultInvokePayload is used.
//
// readmeExists, also injected, controls whether the
// "See <relPath>/README.md for a sample payload" line is appended
// for a given service. The hint is emitted only when:
// (1) no cached payload was available for that service,
// (2) the service has a RelativePath, and
// (3) readmeExists reports a README on disk at that path.
// In the multi-agent layout each service's README hint is rendered
// immediately after that service's invoke line so users can scan
// rows top-to-bottom and find each agent's hint in context.
//
// opts is variadic for backward compatibility but is no longer
// consulted — every field of AfterDeployOpts is now a no-op post-B9.
// See AfterDeployOpts.ForceQualified for the historical context.
func ResolveAfterDeploy(
	state *State,
	cachedPayload func(serviceName string) string,
	readmeExists func(relativePath string) bool,
	opts ...AfterDeployOpts,
) []Suggestion {
	if state == nil || len(state.Services) == 0 {
		return nil
	}

	singleAgent := len(state.Services) == 1
	out := make([]Suggestion, 0, len(state.Services)*3)
	priority := 10

	// Pass 1: all `azd ai agent show <name>` lines, in service order.
	for _, svc := range state.Services {
		desc := fmt.Sprintf("verify %s is running", svc.Name)
		if singleAgent {
			desc = "verify it's running"
		}
		out = append(out, Suggestion{
			Command:     fmt.Sprintf("azd ai agent show %s", svc.Name),
			Description: desc,
			Priority:    priority,
		})
		priority++
	}

	// Pass 2: all `azd ai agent invoke <name> <payload>` lines, each
	// followed by its README hint when applicable. Grouping invokes
	// after shows matches the spec example output (lines 238-241).
	for _, svc := range state.Services {
		payload := ""
		if cachedPayload != nil {
			payload = cachedPayload(svc.Name)
		}
		invokeArg := defaultInvokePayload(&svc)
		if payload != "" {
			invokeArg = shellEscapeSingleQuoted(payload)
		}

		desc := fmt.Sprintf("test %s", svc.Name)
		if singleAgent {
			desc = "test the deployment"
		}
		out = append(out, Suggestion{
			Command:     fmt.Sprintf("azd ai agent invoke %s %s", svc.Name, invokeArg),
			Description: desc,
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
// protocol, omitting the name when empty. When state carries an OpenAPI
// payload (HasOpenAPI == true), the cached sample is preferred over the
// protocol-generic literal so the suggestion matches the agent's actual
// schema. state may be nil — the lookup is a no-op in that case.
//
// `name` is the value placed verbatim into the emitted command. For the
// ResolveAfterShow flow this is the azure.yaml service name (see that
// function's contract for the rationale).
func invokeCommandFor(name, protocol string, state *State) string {
	payload := invokeResponsesPayload
	if protocol == ProtocolInvocations {
		payload = invokeInvocationsPayload
	}
	if state != nil && state.HasOpenAPI && state.OpenAPIPayload != "" {
		payload = shellEscapeSingleQuoted(state.OpenAPIPayload)
	}
	if name == "" {
		return fmt.Sprintf("azd ai agent invoke %s", payload)
	}
	return fmt.Sprintf("azd ai agent invoke %s %s", name, payload)
}

// shellEscapeSingleQuoted wraps s in single quotes for POSIX shells.
// Each embedded apostrophe is replaced with the four-character POSIX
// escape sequence formed by: close the single-quoted string, emit a
// backslash-escaped literal apostrophe, then reopen. The implementation
// below uses a Go raw string for that sequence so its byte pattern is
// stable across edits.
//
// The extracted OpenAPI payload originates from json.Marshal, which
// does not escape apostrophes, so a sample like {"q":"don't"} would
// otherwise terminate the surrounding single-quoted argument and break
// the suggested command.
//
// PowerShell users running these suggestions must adapt the escape
// sequence manually — in PowerShell a literal apostrophe inside a
// single-quoted string is represented by two consecutive apostrophes
// instead. The suggestions are otherwise portable.
func shellEscapeSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
