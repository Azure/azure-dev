// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"fmt"
)

// RubricDimensionScore is a returned rubric dimension, not an inferred verdict.
// Missing numeric and applicability fields remain distinct from zero and false.
type RubricDimensionScore struct {
	ID         string        `json:"id"`
	Score      *LenientFloat `json:"score"`
	Applicable *bool         `json:"applicable"`
	Weight     *LenientFloat `json:"weight"`
	Reason     string        `json:"reason"`
}

// RubricDimensions reads dimension_scores from the evaluator's service
// properties. It does not derive scores or pass/fail from a rubric definition.
func (r OutputResult) RubricDimensions() ([]RubricDimensionScore, error) {
	if len(r.Properties) == 0 {
		return nil, nil
	}
	var properties struct {
		Dimensions []RubricDimensionScore `json:"dimension_scores"`
	}
	if err := json.Unmarshal(r.Properties, &properties); err != nil {
		return nil, fmt.Errorf("reading rubric dimension scores for evaluator %q: %w", r.Name, err)
	}
	return properties.Dimensions, nil
}
