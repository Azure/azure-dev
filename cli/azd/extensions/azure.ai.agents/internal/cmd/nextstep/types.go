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

	// MissingInfraVars lists unresolved agent configuration values
	// that map to infrastructure outputs.
	MissingInfraVars []string

	// MissingAzureContextVars names Azure environment values that must be
	// set before provisioning can create the Foundry project and related
	// resources. Init can intentionally defer these under --no-prompt so
	// local files are still written in headless environments.
	MissingAzureContextVars []string

	// MissingManualVars names ${...} references that map to user-supplied
	// variables which are not set in the azd environment.
	//
	// Toolbox endpoint variables are moved to
	// MissingToolboxEndpoints because azd manages them.
	MissingManualVars []string

	// MissingToolboxEndpoints lists declared toolboxes whose endpoint
	// variable is unset in the active azd environment.
	//
	// ResourceRef records whether deploy or provision owns setup.
	MissingToolboxEndpoints []ResourceRef

	// UnresolvedPlaceholders lists literal {{NAME}} values in agent
	// configuration. They require an azure.yaml edit.
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

	// HasModels, HasToolboxes, and HasConnections are aggregate
	// resource-presence flags across unified and legacy configuration.
	HasModels      bool
	HasToolboxes   bool
	HasConnections bool

	// Resource lists are sorted and deduplicated by service and name.
	ModelRefs   []ResourceRef
	Toolboxes   []ResourceRef
	Connections []ResourceRef

	// Resource load errors let doctor reject incomplete snapshots.
	ModelLoadErrors      []string
	ToolboxLoadErrors    []string
	ConnectionLoadErrors []string
}

// ResourceRef is the resource summary used by guidance and doctor.
type ResourceRef struct {
	// Name is the declared resource name.
	Name string

	// ServiceName is the azd service that owns the resource.
	ServiceName string

	// Detail carries a kind-specific identifier:
	//   - models:      ModelResource.Id (e.g., "azureml://...gpt-4o...")
	//   - connections: <Category> | <Target>
	//   - toolboxes:   empty (no identifier beyond Name today)
	// Doctor remediation messages render Detail verbatim, so changes
	// here must match the doctor-message contract.
	Detail string

	// ManagedByDeploy is true for split deploy-time resource services.
	ManagedByDeploy bool
}

// ServiceState mirrors one entry from the project's services map, plus a
// deployment marker derived from azd environment variables. IsDeployed is
// true when AGENT_<KEY>_VERSION is non-empty in the active environment,
// where <KEY> is the service name upper-cased with hyphens replaced by
// underscores — the convention used by the deploy-time env-var writer in
// project/service_target_agent.go.
type ServiceState struct {
	Name              string
	Host              string
	Protocol          string
	RelativePath      string
	EnvironmentValues []string
	IsDeployed        bool
}
