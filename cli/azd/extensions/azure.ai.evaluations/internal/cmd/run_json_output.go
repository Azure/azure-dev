// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/urlsafe"
)

// runForJSON sanitizes only known error diagnostics on a copy. Dataset values,
// unknown service fields, and the original model remain untouched.
func runForJSON(run *eval_api.OpenAIEvalRun) *eval_api.OpenAIEvalRun {
	if run == nil || run.Error == nil {
		return run
	}
	display := *run
	diagnostic := *run.Error
	diagnostic.Code = urlsafe.Text(diagnostic.Code)
	diagnostic.Message = urlsafe.Text(diagnostic.Message)
	display.Error = &diagnostic
	return &display
}

// redactExportRunError applies the same diagnostic-only projection to a raw
// export without decoding arbitrary user numbers into floating point.
func redactExportRunError(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var run map[string]json.RawMessage
	if err := json.Unmarshal(raw, &run); err != nil {
		return nil, fmt.Errorf("reading exported run diagnostics: %w", err)
	}
	errorJSON, exists := run["error"]
	if !exists || bytes.Equal(bytes.TrimSpace(errorJSON), []byte("null")) {
		return raw, nil
	}
	var diagnostic map[string]json.RawMessage
	if err := json.Unmarshal(errorJSON, &diagnostic); err != nil {
		return nil, fmt.Errorf("reading exported run error: %w", err)
	}
	changed := false
	for _, key := range []string{"code", "message"} {
		value, exists := diagnostic[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return nil, fmt.Errorf("reading exported run error %s: %w", key, err)
		}
		if safe := urlsafe.Text(text); safe != text {
			encoded, err := json.Marshal(safe)
			if err != nil {
				return nil, err
			}
			diagnostic[key] = encoded
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		return nil, err
	}
	run["error"] = encoded
	return json.Marshal(run)
}
