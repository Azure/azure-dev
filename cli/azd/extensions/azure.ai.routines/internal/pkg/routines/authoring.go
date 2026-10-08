// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"cmp"
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
	for _, trigger := range authoringMappingEntries(values["triggers"]) {
		for _, key := range slices.Sorted(maps.Keys(legacyTriggerAuthoringKeys)) {
			if _, found := authoringMappingValue(trigger.Value, key); found {
				return legacyAuthoringKeyError(
					fmt.Sprintf("triggers[%q].%s", trigger.Key, key),
					legacyTriggerAuthoringKeys[key],
				)
			}
		}
	}

	action := values["action"]
	for _, key := range slices.Sorted(maps.Keys(legacyActionAuthoringKeys)) {
		if _, found := authoringMappingValue(action, key); found {
			return legacyAuthoringKeyError("action."+key, legacyActionAuthoringKeys[key])
		}
	}
	return nil
}

type authoringMappingEntry struct {
	Key   string
	Value any
}

func authoringMappingEntries(value any) []authoringMappingEntry {
	var entries []authoringMappingEntry
	switch mapping := value.(type) {
	case map[string]any:
		entries = make([]authoringMappingEntry, 0, len(mapping))
		for key, value := range mapping {
			entries = append(entries, authoringMappingEntry{Key: key, Value: value})
		}
	case map[any]any:
		entries = make([]authoringMappingEntry, 0, len(mapping))
		for key, value := range mapping {
			entries = append(entries, authoringMappingEntry{Key: fmt.Sprint(key), Value: value})
		}
	}
	slices.SortFunc(entries, func(a, b authoringMappingEntry) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return entries
}

func authoringMappingValue(value any, key string) (any, bool) {
	switch mapping := value.(type) {
	case map[string]any:
		value, found := mapping[key]
		return value, found
	case map[any]any:
		value, found := mapping[key]
		return value, found
	default:
		return nil, false
	}
}

func legacyAuthoringKeyError(path, replacement string) error {
	return &AuthoringKeyError{Path: path, Replacement: replacement}
}
