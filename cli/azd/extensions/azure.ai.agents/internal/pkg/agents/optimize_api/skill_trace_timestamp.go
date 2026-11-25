// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// SkillTraceTimestamp preserves integer Unix seconds or legacy ISO timestamps without rounding.
type SkillTraceTimestamp json.RawMessage

// MarshalJSON retains the original numeric or string representation.
func (t SkillTraceTimestamp) MarshalJSON() ([]byte, error) {
	return json.RawMessage(t).MarshalJSON()
}

// UnmarshalJSON accepts the backend's migration formats, never guessing milliseconds.
func (t *SkillTraceTimestamp) UnmarshalJSON(data []byte) error {
	value := SkillTraceTimestamp(data)
	if _, err := value.Time(); err != nil {
		return err
	}
	*t = append((*t)[:0], data...)
	return nil
}

// Time returns the timestamp for window validation without changing its wire representation.
func (t SkillTraceTimestamp) Time() (time.Time, error) {
	var value time.Time
	data := bytes.TrimSpace(t)
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return time.Time{}, fmt.Errorf("invalid trace timestamp: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return time.Time{}, fmt.Errorf("trace timestamps require integer Unix seconds " +
				"or timezone-aware ISO 8601 strings")
		}
		value = parsed
	} else {
		var number json.Number
		if err := json.Unmarshal(data, &number); err != nil {
			return time.Time{}, fmt.Errorf("trace timestamps require integer Unix seconds " +
				"or timezone-aware ISO 8601 strings")
		}
		seconds, err := number.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("trace timestamps require integer Unix seconds " +
				"or timezone-aware ISO 8601 strings")
		}
		// Match the backend's DateTimeOffset range before constructing a Go time.
		if seconds < -62135596800 || seconds > 253402300799 {
			return time.Time{}, fmt.Errorf("trace timestamps must be valid Unix timestamps in seconds, not milliseconds")
		}
		value = time.Unix(seconds, 0).UTC()
	}
	if value.UTC().Year() < 1 || value.UTC().Year() > 9999 {
		return time.Time{}, fmt.Errorf("trace timestamps must be within years 1 through 9999")
	}
	return value, nil
}
