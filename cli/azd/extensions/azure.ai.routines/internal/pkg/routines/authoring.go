// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

var legacyTriggerAuthoringKeys = map[string]string{
	"cron_expression": "cronExpression",
	"time_zone":       "timeZone",
	"connection_id":   "connectionId",
	"issue_event":     "issueEvent",
	"event_name":      "eventName",
}

var legacyActionAuthoringKeys = map[string]string{
	"agent_name":        "agentName",
	"agent_endpoint_id": "agentEndpointId",
	"session_id":        "sessionId",
}

// AuthoringKeyError reports a retired routine authoring property and its
// canonical replacement.
type AuthoringKeyError struct {
	Path        string
	Replacement string
}

// Error returns the authoring-key validation message.
func (e *AuthoringKeyError) Error() string {
	return fmt.Sprintf("routine property %q is not supported; use %q instead", e.Path, e.Replacement)
}

// ParseAuthoringJSON parses a JSON routine manifest using the public camelCase
// authoring contract.
func ParseAuthoringJSON(data []byte) (*Routine, error) {
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if err := ValidateAuthoringKeys(values); err != nil {
		return nil, err
	}

	var routine Routine
	if err := json.Unmarshal(data, &routine); err != nil {
		return nil, err
	}
	return &routine, nil
}

// ParseAuthoringYAML parses a YAML routine manifest using the public camelCase
// authoring contract.
func ParseAuthoringYAML(data []byte) (*Routine, error) {
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if err := ValidateAuthoringKeys(values); err != nil {
		return nil, err
	}

	var routine Routine
	if err := yaml.Unmarshal(data, &routine); err != nil {
		return nil, err
	}
	return &routine, nil
}

// ParseAuthoringMap parses routine properties already decoded from azure.yaml.
func ParseAuthoringMap(values map[string]any) (*Routine, error) {
	if err := ValidateAuthoringKeys(values); err != nil {
		return nil, err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var routine Routine
	if err := json.Unmarshal(data, &routine); err != nil {
		return nil, err
	}
	return &routine, nil
}

// AuthoringMap converts a routine to the canonical camelCase authoring shape.
func AuthoringMap(routine *Routine) (map[string]any, error) {
	data, err := json.Marshal(routine)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

// ValidateAuthoringKeys rejects retired snake_case spellings at extension-owned
// routine property locations. Pass-through action input and trigger parameters
// are intentionally not inspected.
func ValidateAuthoringKeys(values map[string]any) error {
	if triggers, ok := values["triggers"].(map[string]any); ok {
		for _, triggerName := range slices.Sorted(maps.Keys(triggers)) {
			trigger, ok := triggers[triggerName].(map[string]any)
			if !ok {
				continue
			}
			for _, key := range slices.Sorted(maps.Keys(legacyTriggerAuthoringKeys)) {
				if _, found := trigger[key]; found {
					return legacyAuthoringKeyError(
						fmt.Sprintf("triggers[%q].%s", triggerName, key),
						legacyTriggerAuthoringKeys[key],
					)
				}
			}
		}
	}

	if action, ok := values["action"].(map[string]any); ok {
		for _, key := range slices.Sorted(maps.Keys(legacyActionAuthoringKeys)) {
			if _, found := action[key]; found {
				return legacyAuthoringKeyError("action."+key, legacyActionAuthoringKeys[key])
			}
		}
	}
	return nil
}

func legacyAuthoringKeyError(path, replacement string) error {
	return &AuthoringKeyError{Path: path, Replacement: replacement}
}
