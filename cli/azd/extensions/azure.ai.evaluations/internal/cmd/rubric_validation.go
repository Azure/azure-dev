// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"math"
)

// validateRubricDefinition checks authored numeric parameters without changing
// the bytes used for digest and drift decisions. Omitted parameters retain the
// service defaults; other definition kinds keep their own service contract.
func validateRubricDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var definition struct {
		Type          string          `json:"type"`
		Dimensions    json.RawMessage `json:"dimensions"`
		PassThreshold json.RawMessage `json:"pass_threshold"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, fmt.Errorf("reading evaluator definition: %w", err)
	}
	if definition.Type != rubricDefinitionType {
		return raw, nil
	}
	if len(definition.PassThreshold) > 0 {
		var threshold *float64
		if err := json.Unmarshal(definition.PassThreshold, &threshold); err != nil ||
			threshold == nil || *threshold < 0 || *threshold > 1 {
			return nil, fmt.Errorf("definition.pass_threshold must be a number between 0 and 1")
		}
	}
	if len(definition.Dimensions) > 0 {
		var dimensions []map[string]json.RawMessage
		if err := json.Unmarshal(definition.Dimensions, &dimensions); err != nil {
			return nil, fmt.Errorf("reading definition.dimensions: %w", err)
		}
		for i, dimension := range dimensions {
			if dimension == nil {
				return nil, fmt.Errorf("definition.dimensions[%d] must be an object", i)
			}
			if rawWeight, present := dimension["weight"]; present {
				var weight *float64
				if err := json.Unmarshal(rawWeight, &weight); err != nil ||
					weight == nil || *weight < 1 || *weight > 10 || math.Trunc(*weight) != *weight {
					return nil, fmt.Errorf("definition.dimensions[%d].weight must be a whole number between 1 and 10", i)
				}
			}
		}
	}
	return raw, nil
}
