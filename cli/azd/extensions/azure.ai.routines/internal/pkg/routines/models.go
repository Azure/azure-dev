// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package routines provides the data-plane client and models for Microsoft Foundry Routines.
//
// Wire shapes follow the Foundry Routines TypeSpec
// (azure-rest-api-specs PR #43186, src/routines/{models,routes}.tsp).
package routines

// Routine represents a Foundry routine resource.
type Routine struct {
	Name        string                    `json:"name,omitempty"        yaml:"name,omitempty"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Enabled     *bool                     `json:"enabled,omitempty"     yaml:"enabled,omitempty"`
	Triggers    map[string]RoutineTrigger `json:"triggers,omitempty"    yaml:"triggers,omitempty"`
	Action      *RoutineAction            `json:"action,omitempty"      yaml:"action,omitempty"`
	CreatedAt   string                    `json:"created_at,omitempty"  yaml:"created_at,omitempty"`
	UpdatedAt   string                    `json:"updated_at,omitempty"  yaml:"updated_at,omitempty"`
}

// RoutineTrigger is the discriminated union for routine triggers.
// The "type" field selects the variant: "schedule", "timer", or "github_issue".
type RoutineTrigger struct {
	Type string `json:"type"                          yaml:"type"`

	// schedule fields
	CronExpression string `json:"cron_expression,omitempty"     yaml:"cron_expression,omitempty"`

	// schedule / timer shared
	TimeZone string `json:"time_zone,omitempty"           yaml:"time_zone,omitempty"`

	// timer-only fields
	At string `json:"at,omitempty"                  yaml:"at,omitempty"`

	// github_issue fields
	ConnectionID string `json:"connection_id,omitempty"       yaml:"connection_id,omitempty"`
	Assignee     string `json:"assignee,omitempty"            yaml:"assignee,omitempty"`
	Repository   string `json:"repository,omitempty"          yaml:"repository,omitempty"`
}

// RoutineAction is the discriminated union for routine actions.
// The "type" field selects the variant:
//   - "invoke_agent_responses_api" (CLI alias: "agent-response")
//   - "invoke_agent_invocations_api" (CLI alias: "agent-invoke")
type RoutineAction struct {
	Type            string `json:"type"                          yaml:"type"`
	AgentName       string `json:"agent_name,omitempty"          yaml:"agent_name,omitempty"`
	AgentEndpointID string `json:"agent_endpoint_id,omitempty"   yaml:"agent_endpoint_id,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"     yaml:"conversation_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"          yaml:"session_id,omitempty"`
}

// PagedRoutine represents a page of routine resources.
//
// Wire shape is `AgentsPagedResult<Routine>` from the Foundry TypeSpec (see
// `azure-rest-api-specs` PR #43498): `{ data, first_id, last_id, has_more }`
// with `?after=<last_id>` as the continuation cursor.
//
// `Value` and `NextLink` are kept as fallback decode targets so the CLI still
// works against any region that has not yet rolled out the new envelope.
// Use [PagedRoutine.Items] and [PagedRoutine.NextCursor] / [PagedRoutine.NextLinkURL]
// instead of reading the fields directly.
type PagedRoutine struct {
	Data    []Routine `json:"data,omitempty"`
	FirstID string    `json:"first_id,omitempty"`
	LastID  string    `json:"last_id,omitempty"`
	HasMore bool      `json:"has_more,omitempty"`

	// Legacy fields kept for backward compatibility with the previous spec.
	Value    []Routine `json:"value,omitempty"`
	NextLink string    `json:"nextLink,omitempty"`
}

// Items returns the routines on the page, preferring the spec-shaped `data`
// field and falling back to the legacy `value` field.
func (p *PagedRoutine) Items() []Routine {
	if len(p.Data) > 0 {
		return p.Data
	}
	return p.Value
}

// NextCursor returns the opaque cursor to send as `?after=<cursor>` to fetch
// the next page, or the empty string if there is no next page.
func (p *PagedRoutine) NextCursor() string {
	if p.HasMore && p.LastID != "" {
		return p.LastID
	}
	return ""
}

// NextLinkURL returns the legacy absolute pagination URL, or the empty
// string if the response does not carry one.
func (p *PagedRoutine) NextLinkURL() string {
	return p.NextLink
}

// RoutineRun represents a single routine execution record.
type RoutineRun struct {
	ID                  string `json:"id,omitempty"`
	Status              string `json:"status,omitempty"`
	Phase               string `json:"phase,omitempty"`
	TriggerType         string `json:"trigger_type,omitempty"`
	AttemptSource       string `json:"attempt_source,omitempty"`
	ActionType          string `json:"action_type,omitempty"`
	TriggeredAt         string `json:"triggered_at,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	EndedAt             string `json:"ended_at,omitempty"`
	DispatchID          string `json:"dispatch_id,omitempty"`
	ActionCorrelationID string `json:"action_correlation_id,omitempty"`
	ResponseID          string `json:"response_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	ErrorType           string `json:"error_type,omitempty"`
	ErrorMessage        string `json:"error_message,omitempty"`
}

// PagedRoutineRun represents a page of routine run records.
//
// Wire shape is `AgentsPagedResult<RoutineRun>` from the Foundry TypeSpec
// (see `azure-rest-api-specs` PR #43498): `{ data, first_id, last_id, has_more }`
// with `?after=<last_id>` as the continuation cursor.
//
// `Value` and `NextPageToken` are kept as fallback decode targets so the CLI
// still works against any region that has not yet rolled out the new envelope.
// Use [PagedRoutineRun.Items] and [PagedRoutineRun.NextCursor] /
// [PagedRoutineRun.NextLegacyPageToken] instead of reading the fields directly.
type PagedRoutineRun struct {
	Data    []RoutineRun `json:"data,omitempty"`
	FirstID string       `json:"first_id,omitempty"`
	LastID  string       `json:"last_id,omitempty"`
	HasMore bool         `json:"has_more,omitempty"`

	// Legacy fields kept for backward compatibility with the previous spec.
	Value         []RoutineRun `json:"value,omitempty"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

// Items returns the runs on the page, preferring the spec-shaped `data` field
// and falling back to the legacy `value` field.
func (p *PagedRoutineRun) Items() []RoutineRun {
	if len(p.Data) > 0 {
		return p.Data
	}
	return p.Value
}

// NextCursor returns the opaque cursor to send as `?after=<cursor>` to fetch
// the next page. It prefers the spec-shaped `has_more`+`last_id` pair and
// falls back to the legacy `nextPageToken` field.
func (p *PagedRoutineRun) NextCursor() string {
	if p.HasMore && p.LastID != "" {
		return p.LastID
	}
	return p.NextPageToken
}

// RoutineDispatchPayload is the discriminated dispatch payload. The "type"
// field matches the routine action type (invoke_agent_responses_api or
// invoke_agent_invocations_api).
type RoutineDispatchPayload struct {
	Type  string `json:"type"`
	Input string `json:"input,omitempty"`
}

// DispatchRoutineRequest is the request body for the :dispatch_async route.
type DispatchRoutineRequest struct {
	Payload *RoutineDispatchPayload `json:"payload,omitempty"`
}

// DispatchRoutineResponse is the response from the :dispatch_async route.
type DispatchRoutineResponse struct {
	DispatchID          string `json:"dispatch_id,omitempty"`
	ActionCorrelationID string `json:"action_correlation_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
}

// TriggerCLIToWire maps CLI --trigger aliases to wire type values.
//
// The github_issue value here matches the deployed service. The TypeSpec uses
// github_issue_opened; the CLI does not expose the github trigger yet.
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
