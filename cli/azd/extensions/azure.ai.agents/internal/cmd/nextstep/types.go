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

// State is the snapshot resolvers operate on. AssembleState builds one per
// call; there is no shared singleton or cross-command cache. Fields
// marked optional below are populated only by the resolver paths that
// need them — see field docs.
type State struct {
	// HasProjectEndpoint reports whether FOUNDRY_PROJECT_ENDPOINT is set
	// (and non-empty) in the active azd environment.
	HasProjectEndpoint bool

	// PendingProvisionReasons lists the resource-class tags that
	// `azd ai agent init` configured but Azure has not yet
	// materialized. Init code paths append a tag — e.g.
	// "model_deployment" when a new model deployment is configured in
	// an existing project, "project" when a new Foundry project is
	// selected, "acr"/"app_insights" when the user leaves those
	// inputs blank — and the postprovisionHandler clears the list on
	// successful provision. The resolver fires `azd provision`
	// whenever the list is non-empty; doctor can surface the specific
	// reasons for richer diagnostics.
	//
	// The signal is stored in the AI_AGENT_PENDING_PROVISION env var
	// (extension-owned namespace, not AZURE_*) as a comma-separated,
	// sorted, deduplicated string. Unknown tags are tolerated by the
	// resolver for forward-compatibility, so new init sites can
	// introduce new tags without coordinating with this package. See
	// pending_provision.go for the read/write helpers and the
	// reason-tag taxonomy.
	PendingProvisionReasons []string

	// MissingInfraVars names ${...} references in agent.yaml that map to
	// Bicep outputs not yet present in the azd environment (i.e.,
	// provision is needed or has been skipped). Named so the resolver can
	// surface an actionable hint.
	MissingInfraVars []string

	// MissingAzureContextVars names Azure environment values that must be
	// set before provisioning can create the Foundry project and related
	// resources. Init can intentionally defer these under --no-prompt so
	// local files are still written in headless environments.
	MissingAzureContextVars []string

	// MissingManualVars names ${...} references that map to user-supplied
	// variables which are not set in the azd environment.
	//
	// Toolbox-derived endpoint variables (`TOOLBOX_<NAME>_MCP_ENDPOINT`
	// keys that correspond to a manifest-declared toolbox) are
	// partitioned out into MissingToolboxEndpoints — they are
	// azd-managed outputs of `azd provision`, not operator-supplied,
	// and routing them to `azd env set` is misleading.
	MissingManualVars []string

	// MissingToolboxEndpoints lists manifest-declared toolboxes whose
	// azd-injected TOOLBOX_<NAME>_MCP_ENDPOINT variable is unset in the
	// active azd environment. AssembleState partitions these out of
	// MissingManualVars because they are produced by
	// `azd provision` (listen.go::registerToolboxEnvVars), not by the
	// user — the right remediation is `azd provision` (which creates
	// the toolbox in the Foundry project on first run and sets the
	// derived env var), not `azd env set`.
	//
	// Each entry carries the manifest's resource Name and the owning
	// ServiceName so the resolver and doctor checks can render
	// per-service guidance. The Detail field is unused (toolbox
	// endpoints have no kind-specific identifier beyond Name) but the
	// shared ResourceRef shape keeps the renderer code uniform with
	// state.Toolboxes / state.ModelRefs / state.Connections.
	MissingToolboxEndpoints []ResourceRef

	// Services is the per-AGENT snapshot derived from azure.yaml plus the
	// azd environment (for IsDeployed). Under the unified design a single
	// `host: microsoft.foundry` service entry declares N agents; each
	// agent becomes one ServiceState entry here (the unit the resolvers
	// show and invoke). See ServiceState for the per-agent fields and the
	// owning-service back-reference.
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

	// CreatedFolderDisplay is a pre-computed, user-friendly relative path
	// to the project folder created during init (e.g., "my-agent"). Empty
	// when init did not create a new directory. The resolver uses it to
	// prepend a `cd <folder>` suggestion to the Next: block.
	CreatedFolderDisplay string

	// HasModels, HasToolboxes, HasConnections are aggregate flags derived
	// from each `host: microsoft.foundry` service's top-level
	// `deployments` / `toolboxes` / `connections` declarations in
	// azure.yaml. They are true when at least one resource of the
	// matching kind is declared across all Foundry services. Doctor
	// checks that only make sense in the presence of these resources
	// gate-skip themselves on the matching Has* flag; resolvers can use
	// them to tailor remediation suggestions.
	//
	// All three flags are false when no Foundry service declares the
	// matching resource kind.
	HasModels      bool
	HasToolboxes   bool
	HasConnections bool

	// ModelRefs, Toolboxes, Connections list every resource of the
	// matching kind found across all Foundry services' azure.yaml
	// declarations (`deployments` / `toolboxes` / `connections`).
	// Entries are sorted by Name (ties broken by ServiceName) and
	// deduplicated on (ServiceName, Name) so callers can render them
	// deterministically. The slices are nil when the matching Has* flag
	// is false.
	ModelRefs   []ResourceRef
	Toolboxes   []ResourceRef
	Connections []ResourceRef
}

// ResourceRef is a slim summary of a Foundry resource (model deployment,
// toolbox, or connection) that the nextstep package surfaces to doctor
// checks and resolvers. The shape intentionally elides the full
// deployment/toolbox/connection details that doctor checks don't consume
// today — keeping the surface small so future schema changes don't ripple
// through the resolver / doctor boundary. Add fields here only when a
// doctor check or resolver branch needs them.
type ResourceRef struct {
	// Name is the resource's declared name (the `name:` field on the
	// `deployments[]` / `toolboxes[]` / `connections[]` entry in the
	// Foundry service). Doctor checks match by this name when looking up
	// Foundry deployments / connections / toolboxes.
	Name string

	// ServiceName is the `host: microsoft.foundry` service entry (under
	// `services:` in azure.yaml) that declared the resource. When the
	// same logical resource is declared by multiple Foundry services
	// they appear as separate entries — doctor checks key on
	// (ServiceName, Name) so per-service failures are surfaced
	// individually.
	ServiceName string

	// Detail carries a kind-specific identifier:
	//   - models:      <Format>/<Name> (e.g., "OpenAI/gpt-4.1-mini")
	//   - connections: <Category> | <Target>
	//   - toolboxes:   empty (no identifier beyond Name today)
	// Doctor remediation messages render Detail verbatim, so changes
	// here must match the doctor-message contract.
	Detail string
}

// ServiceState is the per-AGENT snapshot the resolvers operate on. Under
// the unified design each `host: microsoft.foundry` service declares N
// agents; one ServiceState is produced per agent (the unit the resolvers
// show and invoke), with ServiceName back-referencing the owning Foundry
// service entry.
//
// IsDeployed is true when AGENT_<KEY>_VERSION is non-empty in the active
// environment, where <KEY> is the agent name upper-cased with spaces and
// hyphens replaced by underscores — the convention used by the
// deploy-time env-var writer in project/service_target_agent.go.
type ServiceState struct {
	// Name is the agent name (used in `azd ai agent show/invoke <Name>`).
	Name string
	// Kind is the agent kind: "hosted" or "prompt".
	Kind string
	// Host is the owning Foundry service host ("microsoft.foundry").
	Host string
	// ServiceName is the owning `host: microsoft.foundry` service entry
	// name in azure.yaml.
	ServiceName string
	// Protocol is the agent's preferred protocol ("responses" or
	// "invocations"); best-effort, empty when undeclared.
	Protocol string
	// RelativePath is the agent's source/project directory relative to
	// the project root (the agent's `project:` field), used for README
	// lookups. Empty for prompt agents (no code).
	RelativePath string
	// IsDeployed reports whether AGENT_<KEY>_VERSION is set for this agent.
	IsDeployed bool
	// Env is the agent's declared environment map (`agents[].env` in
	// azure.yaml). Values may contain ${VAR} / ${{...}} references
	// verbatim; missing-var detection scans these.
	Env map[string]string
}
