// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// normalizeGeneratedSeedRows adapts generated turn targets to the simulation
// input contract. Raw fields preserve identifiers and unrelated numeric values.
func normalizeGeneratedSeedRows(content []byte) ([]byte, bool, error) {
	var output bytes.Buffer
	changed := false
	for i, line := range bytes.SplitAfter(content, []byte("\n")) {
		text := bytes.TrimSpace(line)
		if i == 0 {
			text = bytes.TrimPrefix(text, []byte("\xef\xbb\xbf"))
		}
		if len(text) == 0 {
			output.Write(line)
			continue
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal(text, &row); err != nil {
			return nil, false, fmt.Errorf("generated seed row %d: %w", i+1, err)
		}
		desired, present := row[seedTurnsField]
		if !present {
			output.Write(line)
			continue
		}
		var value any
		if err := json.Unmarshal(desired, &value); err != nil {
			return nil, false, fmt.Errorf("generated seed row %d: %w", i+1, err)
		}
		if turns, ok := wholeNumber(value); !ok || turns < 1 {
			return nil, false, fmt.Errorf("generated seed row %d: %s must be a positive whole number",
				i+1, seedTurnsField)
		}
		settings := map[string]json.RawMessage{}
		if raw, present := row[seedConfigField]; present {
			if err := json.Unmarshal(raw, &settings); err != nil || settings == nil {
				return nil, false, fmt.Errorf("generated seed row %d: %s must be an object", i+1, seedConfigField)
			}
		}
		if _, present := settings[seedTurnsField]; present {
			return nil, false, fmt.Errorf("generated seed row %d: %s is specified both inside and outside %s",
				i+1, seedTurnsField, seedConfigField)
		}
		settings[seedTurnsField] = desired
		raw, err := json.Marshal(settings)
		if err != nil {
			return nil, false, fmt.Errorf("encoding generated seed row %d settings: %w", i+1, err)
		}
		row[seedConfigField] = raw
		delete(row, seedTurnsField)
		raw, err = json.Marshal(row)
		if err != nil {
			return nil, false, fmt.Errorf("encoding generated seed row %d: %w", i+1, err)
		}
		output.Write(raw)
		output.WriteByte('\n')
		changed = true
	}
	if !changed {
		return content, false, nil
	}
	return output.Bytes(), true, nil
}
