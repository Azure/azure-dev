// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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
	changed := false
	// The run decodes `error` without regard to case, so every spelling of it is
	// redacted, not only the exact one.
	for runKey, errorJSON := range run {
		if !strings.EqualFold(runKey, "error") || bytes.Equal(bytes.TrimSpace(errorJSON), []byte("null")) {
			continue
		}
		redacted, errorChanged, err := redactExportError(errorJSON)
		if err != nil {
			return nil, err
		}
		if errorChanged {
			run[runKey] = redacted
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(run)
}

// redactExportError redacts the code and message of one exported error object.
func redactExportError(errorJSON json.RawMessage) (redacted json.RawMessage, changed bool, err error) {
	var diagnostic map[string]json.RawMessage
	if err := json.Unmarshal(errorJSON, &diagnostic); err != nil {
		return nil, false, fmt.Errorf("reading exported run error: %w", err)
	}
	// The service's key spelling is kept, and matched without regard to case the
	// way the run decodes it, so `Message` is redacted as `message` is.
	for key := range diagnostic {
		if !strings.EqualFold(key, "code") && !strings.EqualFold(key, "message") {
			continue
		}
		value := diagnostic[key]
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return nil, false, fmt.Errorf("reading exported run error %s: %w", key, err)
		}
		if safe := urlsafe.Text(text); safe != text {
			encoded, err := json.Marshal(safe)
			if err != nil {
				return nil, false, err
			}
			diagnostic[key] = encoded
			changed = true
		}
	}
	if !changed {
		return errorJSON, false, nil
	}
	redacted, err = json.Marshal(diagnostic)
	return redacted, err == nil, err
}
