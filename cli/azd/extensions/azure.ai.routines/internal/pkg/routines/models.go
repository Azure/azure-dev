// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package routines provides the data-plane client and models for Microsoft Foundry Routines.
package routines

// Routine represents a Foundry routine resource.
//
// Field shapes follow the Routines TypeSpec
// (azure-rest-api-specs PR #43186, src/routines/models.tsp), with a deliberate
// wire-naming divergence noted below.
//
// JSON tags use camelCase to match the deployed Foundry service, which applies
// a camelCase property-naming policy on the wire regardless of the snake_case
// casing in the TypeSpec / OpenAPI document. YAML tags stay snake_case to
// match the user-facing manifest convention used in --file documentation.
//
// Spec divergences kept for service compatibility:
//   - Wire field naming uses camelCase, not snake_case as in the spec.
//   - `AgentID` keeps the wire name `agentId`; the spec renames this to
//     `agent_name`, but the live service still expects `agentId`.
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
//   - "github_issue_opened": GitHub issue-opened trigger (deferred in CLI)
//
// The spec previously used `github_issue` with `owner`/`actions[]` fields;
// PR #43186 renamed it to `github_issue_opened` with an `assignee` field.
// The CLI surface for this trigger is deferred, so the struct tracks the new
// spec shape (assignee), while `TriggerCLIToWire` still maps the CLI alias
// `github-issue` to `github_issue` for live-service compatibility.
type RoutineTrigger struct {
	Type string `json:"type"                          yaml:"type"`

	// schedule fields
	CronExpression string `json:"cronExpression,omitempty"      yaml:"cron_expression,omitempty"`

	// schedule / timer shared
	TimeZone string `json:"timeZone,omitempty"            yaml:"time_zone,omitempty"`

	// timer-only fields
	At string `json:"at,omitempty"                  yaml:"at,omitempty"`

	// github_issue_opened fields (per spec PR #43186)
	ConnectionID string `json:"connectionId,omitempty"        yaml:"connection_id,omitempty"`
	Assignee     string `json:"assignee,omitempty"            yaml:"assignee,omitempty"`
	Repository   string `json:"repository,omitempty"          yaml:"repository,omitempty"`
}

// RoutineAction is the discriminated union for routine actions.
// The "type" field selects the variant:
//   - "invoke_agent_responses_api" (CLI alias: "agent-response")
//   - "invoke_agent_invocations_api" (CLI alias: "agent-invoke")
//
// Spec PR #43186 renamed `agent_id` to `agent_name` in
// `InvokeAgentResponsesApiRoutineAction`. The live service still expects
// `agentId`, so we keep `AgentID` with the `agentId` JSON tag and revisit
// when the service catches up.
type RoutineAction struct {
	Type            string `json:"type"                        yaml:"type"`
	AgentID         string `json:"agentId,omitempty"           yaml:"agent_id,omitempty"`
	AgentEndpointID string `json:"agentEndpointId,omitempty"   yaml:"agent_endpoint_id,omitempty"`
	ConversationID  string `json:"conversationId,omitempty"    yaml:"conversation_id,omitempty"`
	SessionID       string `json:"sessionId,omitempty"         yaml:"session_id,omitempty"`
}

// PagedRoutine represents a page of routine resources.
//
// Spec PR #43186 defines the paginated envelope as `AgentsPagedResult<T>`
// with fields `data`, `first_id`, `last_id`, `has_more` (where `last_id`
// is the continuation cursor passed back as `after=`). The deployed service
// still returns the legacy `value` + `nextLink` shape, so the client tracks
// that shape for now and revisits when the service catches up.
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
	// TaskID is the workspace task identifier linked to the routine attempt
	// (added in spec PR #43186; the service already emits it).
	TaskID       string `json:"taskId,omitempty"`
	ErrorType    string `json:"errorType,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// PagedRoutineRun represents a page of routine run records.
//
// Spec PR #43186 also models this with `AgentsPagedResult<RoutineRun>`. The
// deployed service still uses `value` + `nextPageToken`, so the client tracks
// that shape for now.
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

// DispatchRoutineRequest is the request body for the :dispatchAsync route.
//
// The spec route is `:dispatch_async` (snake_case); the live service exposes
// the camelCase form `:dispatchAsync` only. The client URL is camelCase to
// match the service.
type DispatchRoutineRequest struct {
	Payload *RoutineDispatchPayload `json:"payload,omitempty"`
}

// DispatchRoutineResponse is the response from the :dispatchAsync route.
//
// `TaskID` was added in spec PR #43186 and is already emitted by the service.
type DispatchRoutineResponse struct {
	DispatchID          string `json:"dispatchId,omitempty"`
	ActionCorrelationID string `json:"actionCorrelationId,omitempty"`
	TaskID              string `json:"taskId,omitempty"`
}

// TriggerCLIToWire maps CLI --trigger aliases to wire type values.
//
// Note: spec PR #43186 renamed the github trigger wire value from
// `github_issue` to `github_issue_opened`. The live service still expects
// `github_issue`, so the CLI alias `github-issue` keeps that value until the
// service catches up. The CLI does not expose the github trigger yet — see
// `buildTrigger` in `routine_create.go` for the deferred-feature gate.
var TriggerCLIToWire = map[string]string{
	"recurring":    "schedule",
	"timer":        "timer",
	"github-issue": "github_issue",
}

// ActionCLIToWire maps CLI --action aliases to wire type values.
var ActionCLIToWire = map[string]string{
	"agent-response": "invoke_agent_responses_api",
	"agent-invoke":   "invoke_agent_invocations_api",
}

// DefaultTriggerKey is the map key used for the single trigger in create/update.
const DefaultTriggerKey = "default"
