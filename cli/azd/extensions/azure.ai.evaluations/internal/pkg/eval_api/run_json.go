// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// UnmarshalJSON retains fields not yet modeled by the CLI, including nested
// simulation diagnostics. Human-readable views only use documented fields.
func (r *OpenAIEvalRun) UnmarshalJSON(data []byte) error {
	type wire OpenAIEvalRun
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var fields struct {
		Counts map[string]json.RawMessage `json:"result_counts"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	initial, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	*r = OpenAIEvalRun(decoded)
	r.raw = append(json.RawMessage(nil), data...)
	r.initial = initial
	r.reportedCounts = make(map[string]bool, len(fields.Counts))
	for key, value := range fields.Counts {
		r.reportedCounts[key] = string(value) != "null"
	}
	return nil
}

// ReportedResultCounts distinguishes an explicit zero from an absent or null
// count. Constructed runs without service JSON use their typed counts.
func (r *OpenAIEvalRun) ReportedResultCounts() map[string]int {
	if r.ResultCounts == nil {
		return nil
	}
	counts := map[string]int{
		"total": r.ResultCounts.Total, "passed": r.ResultCounts.Passed,
		"failed": r.ResultCounts.Failed, "errored": r.ResultCounts.Errored, "skipped": r.ResultCounts.Skipped,
	}
	for key, value := range counts {
		if value < 0 || (r.reportedCounts != nil && !r.reportedCounts[key]) {
			delete(counts, key)
		}
	}
	return counts
}

// MarshalJSON preserves unknown service fields while including CLI decorations
// such as portal_url and any updated typed values.
func (r OpenAIEvalRun) MarshalJSON() ([]byte, error) {
	type wire OpenAIEvalRun
	typed, err := json.Marshal(wire(r))
	if err != nil || len(r.raw) == 0 {
		return typed, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(typed, &fields); err != nil {
		return nil, err
	}
	// Do not let typed zero defaults overwrite absent or null service members.
	if r.ID == "" {
		delete(fields, "id")
	}
	if r.ResultCounts != nil && r.reportedCounts != nil {
		var counts map[string]json.RawMessage
		if err := json.Unmarshal(fields["result_counts"], &counts); err != nil {
			return nil, err
		}
		for key := range counts {
			if !r.reportedCounts[key] {
				delete(counts, key)
			}
		}
		fields["result_counts"], err = json.Marshal(counts)
		if err != nil {
			return nil, err
		}
	}
	typed, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return mergeServiceJSON(r.raw, typed, r.initial)
}

// Merge without float64 conversion or replacing absent/null service fields with
// unchanged decoded defaults. The initial typed snapshot distinguishes edits.
func mergeServiceJSON(original, updated, initial json.RawMessage) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(original), []byte("null")) && bytes.Equal(updated, initial) {
		return original, nil
	}
	var oldObject, newObject map[string]json.RawMessage
	if json.Unmarshal(original, &oldObject) == nil && oldObject != nil &&
		json.Unmarshal(updated, &newObject) == nil && newObject != nil {
		var initialObject map[string]json.RawMessage
		if len(initial) > 0 {
			if err := json.Unmarshal(initial, &initialObject); err != nil {
				return nil, err
			}
		}
		// The service's own spelling of a key wins: a typed value for `message`
		// replaces a `Message` the service sent, rather than sitting beside it and
		// leaving the original, unsanitized value in the output. A payload that
		// spells one modeled key several ways keeps one spelling (the exact one if
		// present, else the first in sorted order) and drops the rest, so no
		// variant carries a value the typed projection did not sanitize.
		spellings := make(map[string][]string, len(oldObject))
		for key := range oldObject {
			lower := strings.ToLower(key)
			spellings[lower] = append(spellings[lower], key)
		}
		for key, value := range newObject {
			target := key
			variants := spellings[strings.ToLower(key)]
			if _, exact := oldObject[key]; !exact && len(variants) > 0 {
				target = slices.Min(variants)
			}
			for _, variant := range variants {
				if variant != target {
					delete(oldObject, variant)
				}
			}
			if previous, ok := oldObject[target]; ok {
				merged, err := mergeServiceJSON(previous, value, initialObject[key])
				if err != nil {
					return nil, err
				}
				value = merged
			} else if bytes.Equal(value, initialObject[key]) {
				continue
			}
			oldObject[target] = value
		}
		return json.Marshal(oldObject)
	}
	var oldArray, newArray []json.RawMessage
	if json.Unmarshal(original, &oldArray) == nil && oldArray != nil &&
		json.Unmarshal(updated, &newArray) == nil && newArray != nil {
		var initialArray []json.RawMessage
		if len(initial) > 0 {
			if err := json.Unmarshal(initial, &initialArray); err != nil {
				return nil, err
			}
		}
		for i := range min(len(oldArray), len(newArray)) {
			var previous json.RawMessage
			if i < len(initialArray) {
				previous = initialArray[i]
			}
			merged, err := mergeServiceJSON(oldArray[i], newArray[i], previous)
			if err != nil {
				return nil, err
			}
			newArray[i] = merged
		}
		return json.Marshal(newArray)
	}
	return updated, nil
}
