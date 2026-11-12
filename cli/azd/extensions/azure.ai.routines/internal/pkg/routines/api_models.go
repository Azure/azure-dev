// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

// routineAPI is the Foundry Routines request and response wire shape.
type routineAPI struct {
	Name          string                       `json:"name,omitempty"`
	Description   string                       `json:"description,omitempty"`
	Enabled       *bool                        `json:"enabled,omitempty"`
	Authorization *RoutineAuthorization        `json:"authorization,omitempty"`
	Triggers      map[string]routineTriggerAPI `json:"triggers,omitempty"`
	Action        *routineActionAPI            `json:"action,omitempty"`
	CreatedAt     FlexibleTimestamp            `json:"created_at,omitempty"`
	UpdatedAt     FlexibleTimestamp            `json:"updated_at,omitempty"`
}

type routineTriggerAPI struct {
	Type           string            `json:"type"`
	CronExpression string            `json:"cron_expression,omitempty"`
	TimeZone       string            `json:"time_zone,omitempty"`
	At             FlexibleTimestamp `json:"at,omitempty"`
	ConnectionID   string            `json:"connection_id,omitempty"`
	Owner          string            `json:"owner,omitempty"`
	Repository     string            `json:"repository,omitempty"`
	IssueEvent     string            `json:"issue_event,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	EventName      string            `json:"event_name,omitempty"`
	Parameters     *map[string]any   `json:"parameters,omitempty"`
}

type routineActionAPI struct {
	Type            string `json:"type"`
	AgentName       string `json:"agent_name,omitempty"`
	AgentEndpointID string `json:"agent_endpoint_id,omitempty"`
	Input           any    `json:"input,omitempty"`
	Conversation    string `json:"conversation,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
}

type pagedRoutineAPI struct {
	Value             []routineAPI `json:"value"`
	ContinuationToken string       `json:"continuationToken,omitempty"`
}

func routineToAPI(routine *Routine) *routineAPI {
	if routine == nil {
		return nil
	}
	wire := &routineAPI{
		Name:          routine.Name,
		Description:   routine.Description,
		Enabled:       routine.Enabled,
		Authorization: routine.Authorization,
		CreatedAt:     routine.CreatedAt,
		UpdatedAt:     routine.UpdatedAt,
	}
	if len(routine.Triggers) > 0 {
		wire.Triggers = make(map[string]routineTriggerAPI, len(routine.Triggers))
		for name, trigger := range routine.Triggers {
			wire.Triggers[name] = routineTriggerToAPI(trigger)
		}
	}
	if routine.Action != nil {
		wire.Action = routineActionToAPI(routine.Action)
	}
	return wire
}

func routineFromAPI(wire *routineAPI) *Routine {
	if wire == nil {
		return nil
	}
	routine := &Routine{
		Name:          wire.Name,
		Description:   wire.Description,
		Enabled:       wire.Enabled,
		Authorization: wire.Authorization,
		CreatedAt:     wire.CreatedAt,
		UpdatedAt:     wire.UpdatedAt,
	}
	if len(wire.Triggers) > 0 {
		routine.Triggers = make(map[string]RoutineTrigger, len(wire.Triggers))
		for name, trigger := range wire.Triggers {
			routine.Triggers[name] = routineTriggerFromAPI(trigger)
		}
	}
	if wire.Action != nil {
		routine.Action = routineActionFromAPI(wire.Action)
	}
	return routine
}

func routineTriggerToAPI(trigger RoutineTrigger) routineTriggerAPI {
	return routineTriggerAPI{
		Type:           trigger.Type,
		CronExpression: trigger.CronExpression,
		TimeZone:       trigger.TimeZone,
		At:             trigger.At,
		ConnectionID:   trigger.ConnectionID,
		Owner:          trigger.Owner,
		Repository:     trigger.Repository,
		IssueEvent:     trigger.IssueEvent,
		Provider:       trigger.Provider,
		EventName:      trigger.EventName,
		Parameters:     trigger.Parameters,
	}
}

func routineTriggerFromAPI(wire routineTriggerAPI) RoutineTrigger {
	return RoutineTrigger{
		Type:           wire.Type,
		CronExpression: wire.CronExpression,
		TimeZone:       wire.TimeZone,
		At:             wire.At,
		ConnectionID:   wire.ConnectionID,
		Owner:          wire.Owner,
		Repository:     wire.Repository,
		IssueEvent:     wire.IssueEvent,
		Provider:       wire.Provider,
		EventName:      wire.EventName,
		Parameters:     wire.Parameters,
	}
}

func routineActionToAPI(action *RoutineAction) *routineActionAPI {
	return &routineActionAPI{
		Type:            action.Type,
		AgentName:       action.AgentName,
		AgentEndpointID: action.AgentEndpointID,
		Input:           action.Input,
		Conversation:    action.Conversation,
		SessionID:       action.SessionID,
	}
}

func routineActionFromAPI(wire *routineActionAPI) *RoutineAction {
	return &RoutineAction{
		Type:            wire.Type,
		AgentName:       wire.AgentName,
		AgentEndpointID: wire.AgentEndpointID,
		Input:           wire.Input,
		Conversation:    wire.Conversation,
		SessionID:       wire.SessionID,
	}
}
