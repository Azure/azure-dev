// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// UnmarshalJSON retains service output fields not modeled by the CLI, including
// evaluator properties and sample details, for JSON listings and detail views.
func (o *OutputItem) UnmarshalJSON(data []byte) error {
	type wire OutputItem
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected JSON after output item")
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
