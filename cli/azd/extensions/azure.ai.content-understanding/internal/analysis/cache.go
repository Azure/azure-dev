// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"bytes"
	"encoding/json"
	"os"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/localio"
)

// ResultSuffix names complete JSON results, matching batch --output json results beside sources.
const ResultSuffix = ".result.json"

const maxSavedResultBytes = 256 * 1024 * 1024

// SavedResultPath returns the automatic complete-JSON result path for a local input.
func SavedResultPath(input Input) string {
	return input.Path + ResultSuffix
}

// SavedResult is a complete analysis response loaded from a local result file.
type SavedResult struct {
	Path     string
	Response json.RawMessage
	// Unmatched explains why a valid saved result does not apply to this run; Response is empty when set.
	Unmatched string
}

// LoadSavedResult returns the saved complete result for one local input. A missing file is not an error, and a
// valid result for another analyzer or an older source is reported through Unmatched. Unsafe, unreadable, or
// malformed files are errors so they never silently trigger an online analysis.
func LoadSavedResult(input Input, analyzer string) (SavedResult, bool, error) {
	path := SavedResultPath(input)
	exists, err := localio.Check(path)
	if err != nil {
		return SavedResult{}, false, savedResultError("unsafe_saved_result",
			"The saved .result.json file for this input is inaccessible, a symlink, or not a regular file.")
	}
	if !exists {
		return SavedResult{}, false, nil
	}
	body, err := localio.ReadBounded(path, maxSavedResultBytes)
	if err != nil {
		return SavedResult{}, false, savedResultError("unreadable_saved_result",
			"The saved .result.json file for this input could not be read as bounded UTF-8 JSON.")
	}
	var envelope map[string]json.RawMessage
	var result struct {
		AnalyzerID string `json:"analyzerId"`
	}
	nested := []byte(nil)
	if json.Unmarshal(body, &envelope) == nil && envelope != nil {
		nested = bytes.TrimSpace(envelope["result"])
	}
	if len(nested) == 0 || nested[0] != '{' || json.Unmarshal(nested, &result) != nil {
		return SavedResult{}, false, savedResultError("invalid_saved_result",
			"The saved .result.json file for this input is not a complete CU analysis response.")
	}
	if result.AnalyzerID != analyzer {
		return SavedResult{Path: path, Unmatched: "was produced by a different analyzer"}, true, nil
	}
	source, sourceErr := os.Stat(input.Path)
	saved, savedErr := os.Lstat(path)
	if sourceErr != nil || savedErr != nil {
		return SavedResult{}, false, savedResultError("unreadable_saved_result",
			"The saved .result.json file for this input could not be compared with the input file.")
	}
	if source.ModTime().After(saved.ModTime()) {
		return SavedResult{Path: path, Unmatched: "is older than the input file"}, true, nil
	}
	return SavedResult{Path: path, Response: json.RawMessage(body)}, true, nil
}

// PresentSavedResult renders a saved complete response exactly as Execute presents a single online result.
func PresentSavedResult(response json.RawMessage, llm, includeUsage bool) (any, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response, &envelope); err != nil || envelope == nil {
		return nil, invalidAnalysisResponse()
	}
	usage := envelope["usage"]
	if len(usage) == 0 {
		usage = json.RawMessage("null")
	}
	if llm {
		var requestedUsage json.RawMessage
		if includeUsage {
			requestedUsage = usage
		}
		return renderLLM(envelope["result"], requestedUsage)
	}
	return streamPayload(response, usage, false, includeUsage), nil
}

// SaveResult publishes a complete analysis response atomically with private file permissions.
func SaveResult(path string, response json.RawMessage, replace bool) error {
	return savePayload(path, response, replace)
}

func streamPayload(payload any, usage json.RawMessage, llm, includeUsage bool) any {
	if includeUsage && !llm {
		return struct {
			Result any             `json:"result"`
			Usage  json.RawMessage `json:"usage"`
		}{payload, usage}
	}
	return payload
}

func savedResultError(code, message string) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorValidation, Code: code, Message: message,
		Suggestion: "Pass --refresh to reanalyze and replace it, or --cache=false to analyze without saving.",
	}
}
