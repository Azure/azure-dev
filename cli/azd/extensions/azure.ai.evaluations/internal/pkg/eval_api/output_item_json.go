// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import "encoding/json"

// UnmarshalJSON retains service output fields not modeled by the CLI, including
// evaluator properties and sample details, for JSON listings and detail views.
func (o *OutputItem) UnmarshalJSON(data []byte) error {
	type wire OutputItem
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*o = OutputItem(decoded)
	o.raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON preserves service fields the human view does not interpret while
// keeping the existing types and normalization of modeled fields.
func (o OutputItem) MarshalJSON() ([]byte, error) {
	type wire OutputItem
	typed, err := json.Marshal(wire(o))
	if err != nil || len(o.raw) == 0 {
		return typed, err
	}
	return mergeServiceJSON(o.raw, typed)
}
