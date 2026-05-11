// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

// Suggestion is one "Next:" line shown to the user.
//
// A typical output block looks like:
//
//	Next:  azd ai agent run                   -- start the agent locally
//	       azd ai agent invoke --local "Hi!"   -- test it in another terminal
//
// Resolvers return ordered slices of Suggestion. The first entry is
// rendered with the "Next:" label; subsequent entries are aligned under it.
type Suggestion struct {
	// Command is the literal CLI command to display, e.g. "azd ai agent run".
	Command string

	// Description is a short trailing explanation rendered after "  -- ".
	// Keep it under ~50 chars; resolvers should write user-facing copy here.
	Description string
}

// State captures everything resolvers need to decide which suggestions
// to emit. It is assembled by AssembleState from the azd project +
// environment, plus optional runtime probes performed by the caller.
//
// Fields are intentionally permissive — resolvers must work with
// partial state (e.g., no azd environment, or no agent services
// declared yet).
type State struct {
	// HasAzureYaml reports whether an azure.yaml was successfully loaded.
	HasAzureYaml bool

	// EnvName is the current azd environment name. Empty when no
	// environment is selected.
	EnvName string

	// AgentServices lists every azure.ai.agent service declared in
	// azure.yaml, in declaration order.
	AgentServices []ServiceState

	// HasProjectEndpoint is true when AZURE_AI_PROJECT_ENDPOINT is set
	// and non-empty in the current azd environment.
	HasProjectEndpoint bool

	// ProjectEndpoint is the value of AZURE_AI_PROJECT_ENDPOINT, or
	// empty.
	ProjectEndpoint string

	// HasToolboxes is true when azure.yaml declares one or more
	// azure.ai.toolbox services.
	HasToolboxes bool

	// ToolboxNames lists every azure.ai.toolbox service in declaration
	// order.
	ToolboxNames []string

	// UnresolvedInfraVars lists ${VAR} references in azure.yaml that
	// look like Bicep/azd outputs and are not present in the azd env.
	UnresolvedInfraVars []string

	// UnresolvedManualVars lists ${VAR} references in azure.yaml that
	// look user-supplied (e.g., API keys) and are not present in the
	// azd env or .env.
	UnresolvedManualVars []string

	// UnresolvedConnections lists connection-shaped ${VAR} references
	// (e.g., ${GITHUB_MCP_CONN}) that are not present in the azd env.
	// Connections are also counted in UnresolvedInfraVars.
	UnresolvedConnections []string
}

// ServiceState captures per-agent state assembled from azure.yaml plus
// the AGENT_<KEY>_NAME / VERSION / ENDPOINT environment values.
type ServiceState struct {
	// ServiceName is the azure.yaml service key.
	ServiceName string

	// Protocol is the first protocol declared by the agent
	// (e.g., "responses", "invocations") or empty when not declared.
	Protocol string

	// DeployedName is the AGENT_<KEY>_NAME env value, set after deploy.
	DeployedName string

	// DeployedVersion is the AGENT_<KEY>_VERSION env value.
	DeployedVersion string

	// Endpoint is the AGENT_<KEY>_ENDPOINT env value.
	Endpoint string

	// IsDeployed reports whether DeployedName is set.
	IsDeployed bool

	// RelativePath is the azure.yaml service `project` directory
	// (relative to the azd project root). Empty when not declared.
	RelativePath string
}

// PrimaryAgent returns the single agent service when there is exactly
// one declared, otherwise nil. Resolvers use this to decide between
// single-agent and multi-agent rendering.
func (s *State) PrimaryAgent() *ServiceState {
	if s == nil || len(s.AgentServices) != 1 {
		return nil
	}
	return &s.AgentServices[0]
}

// HasMultipleAgents reports whether there are 2+ agent services.
func (s *State) HasMultipleAgents() bool {
	return s != nil && len(s.AgentServices) > 1
}

// JSON is the structured shape for the optional `nextStep` field that
// commands include in their `--output json` envelope. It mirrors the
// human-readable Next: block: a primary suggestion is always rendered,
// secondary is optional and used for the aligned second line, and note
// captures the dim hint (e.g., "Tip: enable shell completion ...").
//
// When suggestions is empty, BuildJSON returns nil so the field is
// omitted from JSON output entirely.
type JSON struct {
	Primary   *JSONSuggestion `json:"primary"`
	Secondary *JSONSuggestion `json:"secondary,omitempty"`
	Note      string          `json:"note,omitempty"`
}

// JSONSuggestion is the per-line shape inside JSON. It mirrors
// Suggestion but uses snake_case field names for the JSON envelope so
// it composes cleanly with the rest of the extension's command output.
type JSONSuggestion struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// BuildJSON converts a resolver's []Suggestion + optional hint into the
// `nextStep` envelope used by --output json paths. Returns nil when
// there is no primary suggestion so callers can omit the field via
// `omitempty`.
func BuildJSON(suggestions []Suggestion, note string) *JSON {
	if len(suggestions) == 0 {
		return nil
	}
	out := &JSON{
		Primary: &JSONSuggestion{
			Command:     suggestions[0].Command,
			Description: suggestions[0].Description,
		},
		Note: note,
	}
	if len(suggestions) > 1 {
		out.Secondary = &JSONSuggestion{
			Command:     suggestions[1].Command,
			Description: suggestions[1].Description,
		}
	}
	return out
}
