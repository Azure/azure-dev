// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

const (
	// ProtocolInvocations is the value of `agent.yaml#protocol` for
	// JSON-body /invocations agents.
	ProtocolInvocations = "invocations"
	// ProtocolResponses is the value of `agent.yaml#protocol` for plain
	// text /responses agents.
	ProtocolResponses = "responses"

	// placeholderPayload is the single-quoted literal the resolver
	// emits as the body argument when no concrete payload is known —
	// either because no OpenAPI sample has been extracted yet, or
	// because the agent's schema is genuinely opaque to azd. It
	// replaces the legacy `'{"message": "Hello!"}'` (invocations) and
	// `"Hello!"` (responses) fallbacks: those literals only matched
	// the basic sample's schema and silently 400'd on every other
	// agent shape. `'<payload>'` is honest — it signals "substitute
	// your own body here" instead of pretending we know the right
	// one. When the service has a sibling README on disk, the resolver
	// pairs the placeholder with a `see <relPath>/README.md` pointer
	// so the user has somewhere concrete to look for the real shape.
	placeholderPayload = `'<payload>'`

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
//
//   - UnresolvedPlaceholders (always shown first when present, regardless
//     of other branches) → one "edit agent.yaml: replace {{NAME}}" line
//     per unresolved Mustache placeholder (up to maxFixupLines). These
//     are deploy-time landmines: the literal `{{NAME}}` would otherwise
//     land in the container. They never reach `azd env set` because the
//     value lives in agent.yaml itself, not the azd environment.
//
//   - len(PendingProvisionReasons) > 0 OR !HasProjectEndpoint OR
//     MissingInfraVars → required Azure-context `azd env set` fixups,
//     then `azd provision`
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
//     FOUNDRY_PROJECT_ENDPOINT carried over from a prior init or
//     sibling environment cannot mislead the resolver into
//     suggesting `azd ai agent run`. See state.PendingProvisionReasons
//     for the env-var contract.
//
//   - MissingToolboxEndpoints OR MissingManualVars (or BOTH) → a
//     combined "fix things before you can run locally" branch. The two
//     sub-branches are additive (not mutually exclusive) so a manifest
//     that declares a toolbox AND references an unrelated manual var
//     (e.g. an API key) surfaces guidance for both — otherwise the
//     toolbox sub-branch would silently swallow the `azd env set` lines
//     and still emit `azd ai agent run`, leaving the user to discover
//     the unset manual var when the agent crashes.
//
//     Toolbox sub-branch: emits `azd provision` (with an
//     `azd ai agent doctor` follow-up so the user can verify whether
//     the toolbox already exists in their Foundry project before
//     publishing a fresh version). Toolbox-derived endpoint vars
//     (`TOOLBOX_<NAME>_MCP_ENDPOINT`) are azd-managed outputs of
//     provision (see listen.go::registerToolboxEnvVars), not operator-
//     supplied; suggesting `azd env set` for them would be misleading
//     because the value is the URL of a Foundry resource that doesn't
//     yet exist at this point in the lifecycle. The actual live
//     existence check requires a Foundry API call and lives in
//     `azd ai agent doctor`'s local.toolboxes check, not here —
//     ResolveAfterInit is offline by contract.
//
//     Manual-vars sub-branch: one `azd env set <KEY> <value>` per
//     missing operator-supplied var (up to maxFixupLines). Matches
//     issue #7975's "Then run 'azd ai agent run' to start locally"
//     manual-vars example.
//
//     A single `azd ai agent run` + invoke-local secondary follows
//     both sub-branches, with a description that names the prerequisite
//     ("once provision completes", "once the values above are set", or
//     "once the steps above are complete" when both apply). The
//     run/invoke pair is suppressed when UnresolvedPlaceholders are
//     also present, since literal `{{NAME}}` values would still break
//     the local agent.
//
//   - Otherwise          → `azd ai agent run` + `azd ai agent invoke
//     --local <payload>` secondary
//     Spec: issue #7975 lines 96-103. The invoke-local secondary
//     lets the user test the agent in another terminal once it's
//     running. Single-agent projects route through resolveInvokeArg,
//     which prefers a sibling README pointer + `'<payload>'`
//     placeholder over a hardcoded sample that could mismatch the
//     agent's schema. Multi-agent projects emit a single unqualified
//     invoke line with a bare placeholder — the user picks the
//     target at runtime via the interactive prompt or `--service`
//     flag, the same shape the spec example uses, and no per-service
//     README hint can be picked deterministically at this layer.
//     Both lines are skipped when only UnresolvedPlaceholders are
//     present, because running locally with literal `{{NAME}}`
//     values is broken.
//
// readmeExists is consulted by resolveInvokeArg to decide whether
// to emit a sibling `see <relPath>/README.md` hint immediately before
// the invoke line. A nil callback disables README detection (tests
// and dry-run callers can pass nil safely).
//
// All paths append the static "When ready to deploy to Azure…" tail.
func ResolveAfterInit(state *State, readmeExists func(relativePath string) bool) []Suggestion {
	if state == nil {
		return nil
	}

	out := make([]Suggestion, 0, 4)
	priority := 5

	// When init created a new project folder, the user's shell is still
	// in the original directory. A leading `cd` suggestion lets them
	// navigate before running any subsequent commands.
	if state.CreatedFolderDisplay != "" {
		out = append(out, Suggestion{
			Command:     fmt.Sprintf("cd %s", state.CreatedFolderDisplay),
			Description: "enter your new project folder",
			Priority:    0,
		})
	}

	// Placeholder fix-ups always come first when present: they are broken
	// state in azure.yaml itself and block both `run` and `deploy`. The
	// user has to edit azure.yaml (or define a matching parameter in
	// agent.manifest.yaml) — `azd env set` cannot reach them.
	hasPlaceholders := len(state.UnresolvedPlaceholders) > 0
	if hasPlaceholders {
		placeholders := slices.Clone(state.UnresolvedPlaceholders)
		slices.Sort(placeholders)
		limit := min(len(placeholders), maxFixupLines)
		for _, name := range placeholders[:limit] {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("edit azure.yaml: replace {{%s}} with the actual value", name),
				Description: "azure.yaml has unresolved manifest placeholders",
				Priority:    priority,
			})
			priority++
		}
	}

	hasToolboxEndpoints := len(state.MissingToolboxEndpoints) > 0
	hasManualVars := len(state.MissingManualVars) > 0

	needsProvision := len(state.PendingProvisionReasons) > 0 ||
		!state.HasProjectEndpoint ||
		len(state.MissingInfraVars) > 0

	switch {
	case needsProvision:
		for _, key := range state.MissingAzureContextVars {
			out = append(out, Suggestion{
				Command:     fmt.Sprintf("azd env set %s <value>", key),
				Description: "required before provisioning Azure resources",
				Priority:    priority,
			})
			priority++
		}
		out = append(out, Suggestion{
			Command:     "azd provision",
			Description: "set up your Foundry project, models, and connections",
			Priority:    priority,
		})
	case hasToolboxEndpoints || hasManualVars:
		// Combined branch for the two "things the user has to fix before
		// running locally" categories. They are intentionally additive
		// (not mutually exclusive) so a manifest that declares a
		// toolbox AND references an unrelated manual var (e.g. an API
		// key) surfaces guidance for BOTH — otherwise the toolbox
		// branch would silently swallow the `azd env set` lines and
		// still emit `azd ai agent run`, leaving the user to discover
		// the unset manual var when the agent crashes.
		//
		// Toolbox sub-branch: manifest declares one or more toolboxes
		// whose azd-injected TOOLBOX_<NAME>_MCP_ENDPOINT variable is
		// not yet present in the azd environment. The variable is
		// written by `azd provision` (listen.go::registerToolboxEnvVars)
		// after the FoundryToolboxClient publishes the toolbox version,
		// so the canonical fix is provision — NOT `azd env set`, which
		// the generic manual-vars sub-branch below would otherwise
		// suggest. We also surface `azd ai agent doctor` as a follow-up
		// so the user can check whether the toolbox already exists in
		// their Foundry project. The actual live existence check
		// belongs in doctor's local.toolboxes (one HTTP GET per
		// toolbox); ResolveAfterInit is offline by contract and must
		// not initiate Foundry API calls.
		if hasToolboxEndpoints {
			out = append(out, Suggestion{
				Command:     "azd provision",
				Description: "create your toolbox(es) in Foundry",
				Priority:    priority,
			})
			priority++
			out = append(out, Suggestion{
				Command:     "azd ai agent doctor",
				Description: "(optional) check whether your toolbox(es) already exist in Foundry",
				Priority:    priority,
			})
			priority++
		}
		// Manual-vars sub-branch: one `azd env set <KEY> <value>` per
		// missing operator-supplied var (up to maxFixupLines). Matches
		// issue #7975's "Then run 'azd ai agent run' to start locally"
		// manual-vars example.
		if hasManualVars {
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
		}
		// Follow-up: once the user finishes the steps above (provision
		// for toolboxes, env-set for manual vars), the next productive
		// command is `azd ai agent run` and the invoke-local secondary.
		// Suppressed when placeholders are also unresolved because
		// literal `{{NAME}}` values in agent.yaml still break the local
		// agent — the user must finish the placeholder fix-ups first;
		// the trailing `azd deploy` reminder still applies.
		if !hasPlaceholders {
			out = append(out, Suggestion{
				Command:     "azd ai agent run",
				Description: runFollowUpDescription(hasToolboxEndpoints, hasManualVars),
				Priority:    priority,
			})
			priority++
			out, _ = appendInvokeLocalSecondary(out, state, readmeExists, priority)
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
		//   azd ai agent invoke --local '<payload>'  -- test it in another terminal
		out, _ = appendInvokeLocalSecondary(out, state, readmeExists, priority)
	}

	out = append(out, Suggestion{
		Command:     "azd deploy",
		Description: "when ready to deploy to Azure",
		Priority:    90,
		Trailing:    true,
	})

	return out
}

// runFollowUpDescription picks the description for the
// `azd ai agent run` follow-up emitted after the toolbox / manual-vars
// branch, so the suffix reflects which categories of work the user
// still has to complete first.
func runFollowUpDescription(hasToolboxEndpoints, hasManualVars bool) string {
	switch {
	case hasToolboxEndpoints && hasManualVars:
		return "start the agent locally once the steps above are complete"
	case hasToolboxEndpoints:
		return "start the agent locally once provision completes"
	case hasManualVars:
		return "start the agent locally once the values above are set"
	default:
		return "start the agent locally"
	}
}

// ResolveAfterRun produces the Next: block printed when the running
// agent first responds to its OpenAPI probe. Pure function over *State.
//
// Decision tree:
//   - HasOpenAPI + OpenAPIPayload non-empty → invoke with extracted payload
//   - No cached payload, sibling README on disk → README pointer +
//     invoke with '<payload>' placeholder. The README pointer is
//     preferred over the generic curl hint when both could fire,
//     because a project-local README is usually the more concrete
//     guide than the live spec URL.
//   - Otherwise → invoke with '<payload>' placeholder + the
//     curl-the-spec tip, so the user has somewhere to look when no
//     README is available.
//
// readmeExists is consulted by resolveInvokeArg to decide whether to
// surface the README pointer. A nil callback disables README
// detection (tests can pass nil safely).
func ResolveAfterRun(state *State, serviceName string, readmeExists func(relativePath string) bool) []Suggestion {
	if state == nil {
		return nil
	}

	svc := findService(state, serviceName)

	cachedPayload := ""
	if state.HasOpenAPI && state.OpenAPIPayload != "" {
		cachedPayload = state.OpenAPIPayload
	}

	invokeArg, readmeHint := resolveInvokeArg(svc, cachedPayload, readmeExists, 5)

	out := make([]Suggestion, 0, 3)
	if readmeHint != nil {
		out = append(out, *readmeHint)
	}
	out = append(out, Suggestion{
		Command:     fmt.Sprintf("azd ai agent invoke --local %s", invokeArg),
		Description: "send a sample request to the running agent",
		Priority:    10,
	})

	// Tip suppressed when a README pointer is already shown: the README
	// is the more concrete reference and the tip would just add noise.
	if !state.HasOpenAPI && readmeHint == nil {
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

// ResolveAfterShow produces the Next: block printed at the end of
// `azd ai agent show` for statuses that need action. Active agents
// intentionally return no guidance: `show` is an inspection command,
// and Doctor/deploy guidance already owns the "try invoke next" path.
// Branches on State.AgentStatus per the platform's `AgentVersionStatus`
// vocabulary.
//
// Status mapping (issue #7975 lines 208-214):
//   - active / idle  → no guidance (already healthy)
//   - creating       → `azd ai agent monitor --type system --follow`
//   - failed / ""    → `azd ai agent monitor --follow` (live log feed,
//     used to be `--tail 100` pre-C5; spec calls for `--follow` so
//     the user can watch the next reconcile attempt stream live)
//   - deleting / deleted → `azd deploy` (redeploy)
//   - anything else (transitional / genuinely unknown) → `azd ai agent
//     show <svc>` re-check
//
// serviceName is the azure.yaml service name used by the unknown-status
// `azd ai agent show <serviceName>` re-check fallback.
func ResolveAfterShow(state *State, serviceName string) []Suggestion {
	if state == nil {
		return nil
	}

	switch AgentVersionStatus(state.AgentStatus) {
	case AgentVersionActive, AgentVersionIdle:
		// `idle` is a defensive synonym for `active`. Both are healthy
		// states, so `show` should stay a pure inspection command and
		// not append invoke guidance.
		return nil
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
//     When no cached payload is available but a sibling README exists,
//     resolveInvokeArg inserts a README pointer immediately before the
//     invoke line so the user has somewhere concrete to look first.
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
// stays pure and unit-testable. Per-service routing is delegated to
// resolveInvokeArg, which decides between (a) the POSIX-escaped real
// payload, (b) a README pointer + `'<payload>'` placeholder, or
// (c) a bare `'<payload>'` placeholder.
//
// readmeExists, also injected, controls whether resolveInvokeArg
// surfaces a `see <relPath>/README.md` pointer for a given service.
// In the multi-agent layout each service's README hint is rendered
// immediately before that service's placeholder invoke line so users
// can scan rows top-to-bottom and find each agent's hint in context.
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
	// preceded by its README hint when applicable. Grouping invokes
	// after shows matches the spec example output (lines 238-241).
	for i := range state.Services {
		svc := &state.Services[i]

		cached := ""
		if cachedPayload != nil {
			cached = cachedPayload(svc.Name)
		}

		invokeArg, readmeHint := resolveInvokeArg(svc, cached, readmeExists, priority)

		desc := fmt.Sprintf("test %s", svc.Name)
		if singleAgent {
			desc = "test the deployment"
		}
		if cached == "" {
			if readmeHint != nil {
				desc = fmt.Sprintf("test %s with the sample-specific payload", svc.Name)
				if singleAgent {
					desc = "test with the sample-specific payload"
				}
				out = append(out, *readmeHint)
				priority++
			}
		}

		out = append(out, Suggestion{
			Command:     fmt.Sprintf("azd ai agent invoke %s %s", svc.Name, invokeArg),
			Description: desc,
			Priority:    priority,
		})
		priority++
	}

	return out
}

func readmeCommand(relativePath string) string {
	rel := path.Clean(strings.ReplaceAll(relativePath, "\\", "/"))
	if rel == "" || rel == "." {
		return "see README.md"
	}
	rel = strings.TrimPrefix(rel, "./")
	return fmt.Sprintf("see %s/README.md", rel)
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

// resolveInvokeArg returns the payload argument to use in an
// `azd ai agent invoke ...` command for svc, and an optional
// README-pointer Suggestion that should be emitted immediately before
// the invoke suggestion. Centralizing this logic keeps ResolveAfterInit,
// ResolveAfterRun, and ResolveAfterDeploy in sync — all three need the
// same "real payload > README hint + placeholder > placeholder" chain,
// and prior to extraction each had its own slightly different copy.
//
// Priority:
//  1. cachedPayload non-empty → POSIX-escaped real payload, no hint.
//     The caller is responsible for sourcing this (e.g. the run-time
//     OpenAPI extractor in ResolveAfterRun, or the doctor's spec
//     cache in ResolveAfterDeploy).
//  2. svc has a README on disk → placeholderPayload + README hint.
//     The hint points the user at the sibling README so they can
//     copy the real payload before running the command.
//  3. Otherwise → placeholderPayload, no hint.
//
// When a hint is returned, callers should append it to the output
// slice first (so it renders before the invoke line) and use
// hintPriority as the next priority counter value.
func resolveInvokeArg(
	svc *ServiceState,
	cachedPayload string,
	readmeExists func(string) bool,
	hintPriority int,
) (invokeArg string, readmeHint *Suggestion) {
	if cachedPayload != "" {
		return shellEscapeSingleQuoted(cachedPayload), nil
	}
	if svc != nil && readmeExists != nil && readmeExists(svc.RelativePath) {
		return placeholderPayload, &Suggestion{
			Command:     readmeCommand(svc.RelativePath),
			Description: "find the sample-specific payload",
			Priority:    hintPriority,
		}
	}
	return placeholderPayload, nil
}

// appendInvokeLocalSecondary appends the post-`azd ai agent run`
// invoke-local Suggestion (and its optional README hint) to out. It is
// shared by ResolveAfterInit's "everything ready" and "manual vars
// missing" branches so both paths give the user a concrete "what to try
// once it's running" command instead of bottoming out at `azd deploy`.
//
// Service selection: when state has exactly one service we route it
// through resolveInvokeArg so the user gets a per-service README pointer
// when applicable. With zero or multiple services we pass nil — the
// resolver cannot pick deterministically which service the user will
// target with an unqualified `azd ai agent invoke --local`, so it emits
// the bare placeholderPayload without a README hint.
//
// Returns the updated slice and the next priority counter value so
// callers can continue numbering subsequent suggestions.
func appendInvokeLocalSecondary(
	out []Suggestion,
	state *State,
	readmeExists func(string) bool,
	priority int,
) ([]Suggestion, int) {
	var svc *ServiceState
	if len(state.Services) == 1 {
		svc = &state.Services[0]
	}
	invokeArg, readmeHint := resolveInvokeArg(svc, "", readmeExists, priority)
	if readmeHint != nil {
		out = append(out, *readmeHint)
		priority++
	}
	out = append(out, Suggestion{
		Command:     fmt.Sprintf("azd ai agent invoke --local %s", invokeArg),
		Description: "test it in another terminal",
		Priority:    priority,
	})
	return out, priority + 1
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
