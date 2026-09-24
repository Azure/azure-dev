// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import "encoding/json"

// UnmarshalJSON retains fields not yet modeled by the CLI, including nested
// simulation diagnostics. Human-readable views only use documented fields.
func (r *OpenAIEvalRun) UnmarshalJSON(data []byte) error {
	type wire OpenAIEvalRun
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields struct {
		Counts map[string]json.RawMessage `json:"result_counts"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = OpenAIEvalRun(decoded)
	r.raw = append(json.RawMessage(nil), data...)
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
	// Do not let typed zero defaults overwrite absent or null service members.
	if r.ResultCounts != nil && r.reportedCounts != nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(typed, &fields); err != nil {
			return nil, err
		}
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
		typed, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	return mergeServiceJSON(r.raw, typed)
}

// Merge at every object and array level without decoding numbers into float64.
func mergeServiceJSON(original, updated json.RawMessage) (json.RawMessage, error) {
	var oldObject, newObject map[string]json.RawMessage
	if json.Unmarshal(original, &oldObject) == nil && oldObject != nil &&
		json.Unmarshal(updated, &newObject) == nil && newObject != nil {
		for key, value := range newObject {
			if previous, ok := oldObject[key]; ok {
				merged, err := mergeServiceJSON(previous, value)
				if err != nil {
					return nil, err
				}
				value = merged
			}
			oldObject[key] = value
		}
		return json.Marshal(oldObject)
	}
	var oldArray, newArray []json.RawMessage
	if json.Unmarshal(original, &oldArray) == nil && oldArray != nil &&
		json.Unmarshal(updated, &newArray) == nil && newArray != nil {
		for i := range min(len(oldArray), len(newArray)) {
			merged, err := mergeServiceJSON(oldArray[i], newArray[i])
			if err != nil {
				return nil, err
			}
			newArray[i] = merged
		}
		return json.Marshal(newArray)
	}
	return updated, nil
}
