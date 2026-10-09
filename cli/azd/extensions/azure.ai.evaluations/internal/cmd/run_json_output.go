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
func runForJSON(run *eval_api.OpenAIEvalRun) (*eval_api.OpenAIEvalRun, error) {
	if run == nil || run.Error == nil {
		return run, nil
	}
	raw, err := json.Marshal(run)
	if err != nil {
		return nil, fmt.Errorf("writing run diagnostics: %w", err)
	}
	raw, err = redactExportRunError(raw)
	if err != nil {
		return nil, err
	}
	var display eval_api.OpenAIEvalRun
	if err := json.Unmarshal(raw, &display); err != nil {
		return nil, fmt.Errorf("reading sanitized run diagnostics: %w", err)
	}
	return &display, nil
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

// redactExportError redacts the recognized diagnostic members of one exported
// error object, including nested details and inner errors.
func redactExportError(errorJSON json.RawMessage) (redacted json.RawMessage, changed bool, err error) {
	var diagnostic map[string]json.RawMessage
	if err := json.Unmarshal(errorJSON, &diagnostic); err != nil {
		return nil, false, fmt.Errorf("reading exported run error: %w", err)
	}
	redacted, changed, err = redactDiagnosticObject(diagnostic)
	if err != nil || !changed {
		return errorJSON, changed, err
	}
	return redacted, true, nil
}

func redactDiagnosticObject(diagnostic map[string]json.RawMessage) (json.RawMessage, bool, error) {
	changed := false
	for key, value := range diagnostic {
		switch {
		case strings.EqualFold(key, "code"),
			strings.EqualFold(key, "message"),
			strings.EqualFold(key, "target"):
			redacted, valueChanged, err := redactDiagnosticText(key, value)
			if err != nil {
				return nil, false, err
			}
			if valueChanged {
				diagnostic[key] = redacted
				changed = true
			}
		case strings.EqualFold(key, "details"),
			strings.EqualFold(key, "innererror"),
			strings.EqualFold(key, "inner_error"),
			strings.EqualFold(key, "error"):
			redacted, valueChanged, err := redactDiagnosticValue(value)
			if err != nil {
				return nil, false, fmt.Errorf("reading exported run error %s: %w", key, err)
			}
			if valueChanged {
				diagnostic[key] = redacted
				changed = true
			}
		}
	}
	if !changed {
		return nil, false, nil
	}
	redacted, err := json.Marshal(diagnostic)
	return redacted, err == nil, err
}

func redactDiagnosticText(key string, value json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("null")) {
		return value, false, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '"' {
		if len(trimmed) > 0 && trimmed[0] != '{' && trimmed[0] != '[' {
			return value, false, nil
		}
		return nil, false, fmt.Errorf("reading exported run error %s: expected text or scalar", key)
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return nil, false, fmt.Errorf("reading exported run error %s: %w", key, err)
	}
	safe := urlsafe.Text(text)
	if safe == text {
		return value, false, nil
	}
	encoded, err := json.Marshal(safe)
	return encoded, err == nil, err
}

func redactDiagnosticValue(value json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return value, false, nil
	}
	switch trimmed[0] {
	case '"':
		return redactDiagnosticText("nested diagnostic", value)
	case '{':
		var diagnostic map[string]json.RawMessage
		if err := json.Unmarshal(value, &diagnostic); err != nil {
			return nil, false, err
		}
		redacted, changed, err := redactDiagnosticObject(diagnostic)
		if err != nil || !changed {
			return value, changed, err
		}
		return redacted, true, nil
	case '[':
		var entries []json.RawMessage
		if err := json.Unmarshal(value, &entries); err != nil {
			return nil, false, err
		}
		changed := false
		for i, entry := range entries {
			redacted, entryChanged, err := redactDiagnosticValue(entry)
			if err != nil {
				return nil, false, err
			}
			if entryChanged {
				entries[i] = redacted
				changed = true
			}
		}
		if !changed {
			return value, false, nil
		}
		redacted, err := json.Marshal(entries)
		return redacted, err == nil, err
	default:
		return value, false, nil
	}
}
