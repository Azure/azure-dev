// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package routines provides the data-plane client and models for Microsoft Foundry Routines.
package routines

// Routine represents a Foundry routine resource.
type Routine struct {
	Name        string                    `json:"name,omitempty"`
	Description string                    `json:"description,omitempty"`
	Enabled     *bool                     `json:"enabled,omitempty"`
	Triggers    map[string]RoutineTrigger `json:"triggers,omitempty"`
	Actions     map[string]RoutineAction  `json:"actions,omitempty"`
}

// RoutineTrigger is the discriminated union for routine triggers.
// The "type" field selects the variant:
//   - "schedule" (CLI alias: "recurring"): cron-based recurring trigger
//   - "timer": one-shot timer trigger
//   - "github_issue": GitHub issue event trigger (deferred)
type RoutineTrigger struct {
	Type string `json:"type"`

	// schedule / timer fields
	Cron     string `json:"cron,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`

	// timer-only fields
	At string `json:"at,omitempty"`

	// github_issue fields (deferred in v1)
	Connection string `json:"connection,omitempty"`
	Assignee   string `json:"assignee,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// RoutineAction is the discriminated union for routine actions.
// The "type" field selects the variant:
//   - "invoke_agent_responses_api" (CLI alias: "agent-response")
//   - "invoke_agent_invocations_api" (CLI alias: "agent-invoke")
type RoutineAction struct {
	Type            string `json:"type"`
	AgentName       string `json:"agent_name,omitempty"`
	AgentEndpointID string `json:"agent_endpoint_id,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
}

// PagedRoutine represents a page of routine resources.
type PagedRoutine struct {
	Value             []Routine `json:"value"`
	ContinuationToken string    `json:"continuation_token,omitempty"`
}

// RoutineRun represents a single routine execution record.
type RoutineRun struct {
	ID        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PagedRoutineRun represents a page of routine run records.
type PagedRoutineRun struct {
	Value         []RoutineRun `json:"value"`
	NextPageToken string       `json:"next_page_token,omitempty"`
}

// DispatchRoutineRequest is the request body for the dispatch_async route.
type DispatchRoutineRequest struct {
	Input          string `json:"input,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// DispatchRoutineResponse is the response from the dispatch_async route.
type DispatchRoutineResponse struct {
	DispatchID           string `json:"dispatch_id,omitempty"`
	ActionCorrelationID  string `json:"action_correlation_id,omitempty"`
	Status               string `json:"status,omitempty"`
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

// DefaultActionKey is the map key used for the single action in create/update.
const DefaultActionKey = "default"
