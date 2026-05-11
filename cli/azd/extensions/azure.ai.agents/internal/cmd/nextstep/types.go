// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package nextstep computes and renders the context-aware "Next:" guidance
// block that azure.ai.agents commands surface at the end of successful (and
// some failing) runs.
//
// The package is split into three concerns:
//
//   - State assembly (state.go) — collects everything resolvers may need
//     into a single immutable snapshot; partial state never silences
//     guidance.
//   - Resolvers (resolver.go) — pure functions over *State that return a
//     ranked []Suggestion for each command's exit path.
//   - Formatters (format.go) — render []Suggestion either to a writer
//     (PrintNext) or as a string suitable for embedding in an artifact's
//     Metadata["note"] (FormatNextForNote).
//
// Output discipline lives at the call sites: the package never writes to
// os.Stdout directly and never inspects --output flags. Callers gate on
// the isTerminal helper / output mode and choose the writer or JSON
// envelope field accordingly.
package nextstep

// Suggestion is a single line of next-step guidance: a command to run plus
// a one-line description. Suggestions are sorted ascending by Priority
// before rendering (lower = earlier; ties preserve input order).
//
// Trailing flags a "footer" suggestion that the renderer reserves a slot
// for even when higher-priority primary suggestions would otherwise fill
// the visible block. Used for follow-up nudges (e.g., the `azd deploy`
// line that ResolveAfterInit appends after the primary action) so the
// follow-up survives truncation. At most one trailing entry is rendered
// per block; when multiple Trailing-flagged entries are passed, the
// entry with the highest Priority wins — by convention footers are the
// most-deferred entries, so the most-deferred survives.
type Suggestion struct {
	Command     string
	Description string
	Priority    int
	Trailing    bool
}

// AuthState captures whether a doctor-style auth probe has been run and
// what it found. AuthUnknown (the zero value) means the probe was not run;
// resolvers treat that as "skip auth-conditional advice" rather than
// emitting login-prompt noise on every successful command.
type AuthState int

const (
	// AuthUnknown indicates the auth probe was not run for this state.
	AuthUnknown AuthState = iota
	// AuthAuthed indicates the probe confirmed a usable token.
	AuthAuthed
	// AuthUnauthed indicates the probe confirmed login is needed.
	AuthUnauthed
)

// State is the snapshot resolvers operate on. AssembleState builds one per
// call; there is no shared singleton or cross-command cache. Fields
// marked optional below are populated only by the resolver paths that
// need them — see field docs.
type State struct {
	// HasProjectEndpoint reports whether AZURE_AI_PROJECT_ENDPOINT is set
	// (and non-empty) in the active azd environment.
	HasProjectEndpoint bool

	// MissingInfraVars names ${...} references in agent.yaml that map to
	// Bicep outputs not yet present in the azd environment (i.e.,
	// provision is needed or has been skipped). Named so the resolver can
	// surface an actionable hint.
	MissingInfraVars []string

	// MissingManualVars names ${...} references that map to user-supplied
	// variables which are not set in the azd environment.
	MissingManualVars []string

	// Services is the per-service snapshot derived from azure.yaml plus
	// the azd environment (for IsDeployed).
	Services []ServiceState

	// AgentStatus is the remote agent version status as reported by the
	// Foundry API (e.g., "Active", "Creating", "Failed"). Empty when the
	// caller did not probe the remote API.
	AgentStatus string

	// HasOpenAPI reports whether OpenAPIPayload has been populated. The
	// payload is populated only when AssembleState is called from a path
	// that contacts the agent (e.g., `run`, `doctor`).
	HasOpenAPI bool

	// OpenAPIPayload is a sample request payload extracted from the
	// agent's OpenAPI spec, suitable for an `azd ai agent invoke '...'`
	// example. Empty when HasOpenAPI is false.
	OpenAPIPayload string

	// IsAuthenticated is populated only by the full-sweep `doctor` path.
	// Every other resolver receives AuthUnknown and treats
	// auth-conditional suggestions as "skip" rather than "tell user to
	// log in".
	IsAuthenticated AuthState
}

// ServiceState mirrors one entry from the project's services map, plus a
// deployment marker derived from azd environment variables. IsDeployed is
// true when AGENT_<KEY>_VERSION is non-empty in the active environment,
// where <KEY> is the service name upper-cased with hyphens replaced by
// underscores — the convention used by the deploy-time env-var writer in
// project/service_target_agent.go.
type ServiceState struct {
	Name         string
	Host         string
	Protocol     string
	RelativePath string
	IsDeployed   bool
}
