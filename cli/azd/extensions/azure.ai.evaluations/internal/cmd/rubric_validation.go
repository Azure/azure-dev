// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

type rubricDimension struct {
	ID               *string         `json:"id,omitempty"`
	Description      *string         `json:"description,omitempty"`
	Weight           json.RawMessage `json:"weight,omitempty"`
	AlwaysApplicable *bool           `json:"always_applicable,omitempty"`
}

// validateRubricDefinition checks authored shape and numeric parameters without changing
// the bytes used for digest and drift decisions. Rubrics require a dimensions
// array; optional parameters and other kinds retain their own service contract.
func validateRubricDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var definition struct {
		Type          json.RawMessage `json:"type"`
		Dimensions    json.RawMessage `json:"dimensions"`
		PassThreshold json.RawMessage `json:"pass_threshold"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, fmt.Errorf("reading evaluator definition: %w", err)
	}
	kind, err := evaluatorDefinitionKind(definition.Type)
	if err != nil {
		return nil, err
	}
	if kind != "" && kind != rubricDefinitionType {
		return raw, nil
	}
	if len(definition.Dimensions) == 0 {
		return nil, fmt.Errorf("definition.dimensions must be an array")
	}
	if len(definition.PassThreshold) > 0 {
		if !rubricNumberInRange(definition.PassThreshold, 0, 1, false) {
			return nil, fmt.Errorf("definition.pass_threshold must be a number between 0 and 1")
		}
	}
	var dimensions []*rubricDimension
	if err := json.Unmarshal(definition.Dimensions, &dimensions); err != nil {
		return nil, fmt.Errorf("reading definition.dimensions: %w", err)
	}
	if dimensions == nil {
		return nil, fmt.Errorf("definition.dimensions must be an array")
	}
	for i, dimension := range dimensions {
		if dimension == nil {
			return nil, fmt.Errorf("definition.dimensions[%d] must be an object", i)
		}
		if len(dimension.Weight) > 0 {
			if !rubricNumberInRange(dimension.Weight, 1, 10, true) {
				return nil, fmt.Errorf("definition.dimensions[%d].weight must be a whole number between 1 and 10", i)
			}
		}
	}
	return raw, nil
}

// evaluatorDefinitionKind distinguishes an omitted compatibility discriminator
// from an explicitly invalid one before either normalization or projection.
func evaluatorDefinitionKind(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var kind string
	if err := json.Unmarshal(raw, &kind); err != nil || kind == "" {
		return "", fmt.Errorf("definition.type must be a non-empty string when supplied")
	}
	return kind, nil
}

// rubricNumberInRange compares the authored JSON number without rounding it.
// Equivalent decimal and exponent forms remain valid; strings and null do not.
func rubricNumberInRange(raw json.RawMessage, minimum, maximum int64, whole bool) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	exact, ok := new(big.Rat).SetString(string(number))
	return ok && (!whole || exact.IsInt()) &&
		exact.Cmp(new(big.Rat).SetInt64(minimum)) >= 0 &&
		exact.Cmp(new(big.Rat).SetInt64(maximum)) <= 0
}
