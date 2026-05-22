// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package routines provides the data-plane client and models for Microsoft Foundry Routines.
package routines

// Routine represents a Foundry routine resource.
// Field shapes track the Routines TypeSpec (azure-rest-api-specs PR #42779):
//   - `triggers` is a map keyed by user-defined identifiers.
//   - `action` is a single discriminated object, not a map.
//
// JSON tags use camelCase to match the deployed Foundry service wire format,
// which applies a camelCase property-naming policy regardless of the
// snake_case casing in the OpenAPI document. YAML tags stay snake_case to
// match the user-facing manifest convention used in --file documentation.
type Routine struct {
	Name        string                    `json:"name,omitempty"        yaml:"name,omitempty"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Enabled     *bool                     `json:"enabled,omitempty"     yaml:"enabled,omitempty"`
	Triggers    map[string]RoutineTrigger `json:"triggers,omitempty"    yaml:"triggers,omitempty"`
	Action      *RoutineAction            `json:"action,omitempty"      yaml:"action,omitempty"`
	CreatedAt   string                    `json:"createdAt,omitempty"   yaml:"created_at,omitempty"`
	UpdatedAt   string                    `json:"updatedAt,omitempty"   yaml:"updated_at,omitempty"`
}

// RoutineTrigger is the discriminated union for routine triggers.
// The "type" field selects the variant:
//   - "schedule" (CLI alias: "recurring"): cron-based recurring trigger
//   - "timer": one-shot timer trigger
//   - "github_issue": GitHub issue event trigger (deferred)
type RoutineTrigger struct {
	Type string `json:"type"                          yaml:"type"`

	// schedule fields
	CronExpression string `json:"cronExpression,omitempty"      yaml:"cron_expression,omitempty"`

	// schedule / timer shared
	TimeZone string `json:"timeZone,omitempty"            yaml:"time_zone,omitempty"`

	// timer-only fields
	At string `json:"at,omitempty"                  yaml:"at,omitempty"`

	// github_issue fields
	ConnectionID string   `json:"connectionId,omitempty"        yaml:"connection_id,omitempty"`
	Owner        string   `json:"owner,omitempty"               yaml:"owner,omitempty"`
	Repository   string   `json:"repository,omitempty"          yaml:"repository,omitempty"`
	Actions      []string `json:"actions,omitempty"             yaml:"actions,omitempty"`
}

// RoutineAction is the discriminated union for routine actions.
// The "type" field selects the variant:
//   - "invoke_agent_responses_api" (CLI alias: "agent-response")
//   - "invoke_agent_invocations_api" (CLI alias: "agent-invoke")
type RoutineAction struct {
	Type            string `json:"type"                        yaml:"type"`
	AgentID         string `json:"agentId,omitempty"           yaml:"agent_id,omitempty"`
	AgentEndpointID string `json:"agentEndpointId,omitempty"   yaml:"agent_endpoint_id,omitempty"`
	ConversationID  string `json:"conversationId,omitempty"    yaml:"conversation_id,omitempty"`
	SessionID       string `json:"sessionId,omitempty"         yaml:"session_id,omitempty"`
}

// PagedRoutine represents a page of routine resources. The service returns an
// `nextLink` absolute URL when more pages exist (Azure.Core.Page<Routine>).
type PagedRoutine struct {
	Value    []Routine `json:"value"`
	NextLink string    `json:"nextLink,omitempty"`
}

// RoutineRun represents a single routine execution record.
type RoutineRun struct {
	ID                  string `json:"id,omitempty"`
	Status              string `json:"status,omitempty"`
	Phase               string `json:"phase,omitempty"`
	TriggerType         string `json:"triggerType,omitempty"`
	AttemptSource       string `json:"attemptSource,omitempty"`
	ActionType          string `json:"actionType,omitempty"`
	TriggeredAt         string `json:"triggeredAt,omitempty"`
	StartedAt           string `json:"startedAt,omitempty"`
	EndedAt             string `json:"endedAt,omitempty"`
	DispatchID          string `json:"dispatchId,omitempty"`
	ActionCorrelationID string `json:"actionCorrelationId,omitempty"`
	ResponseID          string `json:"responseId,omitempty"`
	ErrorType           string `json:"errorType,omitempty"`
	ErrorMessage        string `json:"errorMessage,omitempty"`
}

// PagedRoutineRun represents a page of routine run records.
type PagedRoutineRun struct {
	Value         []RoutineRun `json:"value"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

// RoutineDispatchPayload is the discriminated dispatch payload. The "type"
// field matches the routine action type (invoke_agent_responses_api or
// invoke_agent_invocations_api).
type RoutineDispatchPayload struct {
	Type  string `json:"type"`
	Input string `json:"input,omitempty"`
}

// DispatchRoutineRequest is the request body for the :dispatch / :dispatchAsync
// routes. The payload wrapper is required for :dispatchAsync.
type DispatchRoutineRequest struct {
	Payload *RoutineDispatchPayload `json:"payload,omitempty"`
}

// DispatchRoutineResponse is the response from the :dispatch / :dispatchAsync routes.
type DispatchRoutineResponse struct {
	DispatchID          string `json:"dispatchId,omitempty"`
	ActionCorrelationID string `json:"actionCorrelationId,omitempty"`
}

// TriggerCLIToWire maps CLI --trigger aliases to TypeSpec wire type values.
var TriggerCLIToWire = map[string]string{
	"recurring":    "schedule",
	"timer":        "timer",
	"github-issue": "github_issue",
}

// ActionCLIToWire maps CLI --action aliases to TypeSpec wire type values.
var ActionCLIToWire = map[string]string{
	"agent-response": "invoke_agent_responses_api",
	"agent-invoke":   "invoke_agent_invocations_api",
}

// DefaultTriggerKey is the map key used for the single trigger in create/update.
const DefaultTriggerKey = "default"
