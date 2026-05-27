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

	// UnresolvedPlaceholders names {{NAME}} Mustache-style placeholders
	// still present (literally) inside agent.yaml's environment_variables
	// values. These are left over from init's manifest processing when
	// agent.manifest.yaml declares a placeholder without a matching
	// parameter (or the user skipped the prompt). Unlike Missing*Vars,
	// these cannot be supplied via `azd env set` — the literal `{{X}}`
	// would still be in agent.yaml at deploy time. The resolver surfaces
	// a distinct "edit agent.yaml" suggestion for each.
	UnresolvedPlaceholders []string

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

	// CreatedFolderDisplay is a pre-computed, user-friendly relative path
	// to the project folder created during init (e.g., "my-agent"). Empty
	// when init did not create a new directory. The resolver uses it to
	// prepend a `cd <folder>` suggestion to the Next: block.
	CreatedFolderDisplay string

	// HasModels, HasToolboxes, HasConnections are aggregate flags
	// derived from each azure.ai.agent service's agent.manifest.yaml
	// (when present). They are true when at least one resource of the
	// matching kind is declared across all services. Doctor checks that
	// only make sense in the presence of these resources gate-skip
	// themselves on the matching Has* flag; resolvers can use them to
	// tailor remediation suggestions.
	//
	// All three flags are false when the manifest file is missing,
	// malformed, or declares no resources — the walker is deliberately
	// silent on those failure modes so a missing/in-flight manifest
	// never blocks the rest of state assembly.
	HasModels      bool
	HasToolboxes   bool
	HasConnections bool

	// ModelRefs, Toolboxes, Connections list every resource of the
	// matching kind found across all services' agent.manifest.yaml
	// files. Entries are sorted by Name (ties broken by ServiceName)
	// and deduplicated on (ServiceName, Name) so callers can render
	// them deterministically. The slices are nil when the matching
	// Has* flag is false.
	ModelRefs   []ResourceRef
	Toolboxes   []ResourceRef
	Connections []ResourceRef
}

// ResourceRef is a slim summary of a manifest resource that the
// nextstep package surfaces to doctor checks and resolvers. The
// shape intentionally elides agent_yaml.ModelResource /
// ToolboxResource / ConnectionResource details that doctor checks
// don't consume today — keeping the surface small so future
// manifest schema changes don't ripple through the resolver / doctor
// boundary. Add fields here only when a doctor check or resolver
// branch needs them.
type ResourceRef struct {
	// Name is the resource's manifest-declared name (the `name:`
	// field on the manifest's `resources[]` entry). Doctor checks
	// match by this name when looking up Foundry deployments /
	// connections / toolboxes.
	Name string

	// ServiceName is the azd service that declared the resource (the
	// service entry under `services:` in azure.yaml whose
	// agent.manifest.yaml contains this entry). When the same logical
	// resource is declared by multiple services they appear as
	// separate entries — doctor checks key on (ServiceName, Name) so
	// per-service failures are surfaced individually.
	ServiceName string

	// Detail carries a kind-specific identifier:
	//   - models:      ModelResource.Id (e.g., "azureml://...gpt-4o...")
	//   - connections: <Category> | <Target>
	//   - toolboxes:   empty (no identifier beyond Name today)
	// Doctor remediation messages render Detail verbatim, so changes
	// here must match the doctor-message contract.
	Detail string
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
