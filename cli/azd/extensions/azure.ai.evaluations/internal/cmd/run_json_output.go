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

// runForJSON removes URL credentials from strings in the error diagnostic tree
// on a copy. Non-credential values, top-level service fields, and the original
// model remain untouched.
func runForJSON(run *eval_api.OpenAIEvalRun) (*eval_api.OpenAIEvalRun, error) {
	if run == nil {
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
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw, nil
	}
	// The run decodes `error` without regard to case, so every spelling of it is
	// redacted, not only the exact one.
	redacted, _, err := transformJSONObject(raw, func(key string, value json.RawMessage) (json.RawMessage, bool, error) {
		if !strings.EqualFold(key, "error") || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return value, false, nil
		}
		return redactExportError(value)
	})
	if err != nil {
		return nil, fmt.Errorf("reading exported run diagnostics: %w", err)
	}
	return redacted, nil
}

// redactExportError removes URL credentials from every string below one
// exported error object, including nested diagnostic fields not yet modeled.
func redactExportError(errorJSON json.RawMessage) (redacted json.RawMessage, changed bool, err error) {
	redacted, changed, err = redactDiagnosticObject(errorJSON)
	if err != nil || !changed {
		return errorJSON, changed, err
	}
	return redacted, true, nil
}

func redactDiagnosticObject(diagnostic json.RawMessage) (json.RawMessage, bool, error) {
	return transformJSONObject(diagnostic, func(key string, value json.RawMessage) (json.RawMessage, bool, error) {
		switch {
		case strings.EqualFold(key, "code"),
			strings.EqualFold(key, "message"),
			strings.EqualFold(key, "target"):
			return redactDiagnosticText(key, value)
		case strings.EqualFold(key, "details"),
			strings.EqualFold(key, "innererror"),
			strings.EqualFold(key, "inner_error"),
			strings.EqualFold(key, "error"):
			redacted, valueChanged, err := redactDiagnosticValue(value)
			if err != nil {
				return nil, false, fmt.Errorf("reading exported run error %s: %w", key, err)
			}
			return redacted, valueChanged, nil
		default:
			redacted, valueChanged, err := redactDiagnosticValue(value)
			if err != nil {
				return nil, false, fmt.Errorf("reading exported run error %s: %w", key, err)
			}
			return redacted, valueChanged, nil
		}
	})
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
		redacted, changed, err := redactDiagnosticObject(value)
		if err != nil || !changed {
			return value, changed, err
		}
		return redacted, true, nil
	case '[':
		return transformJSONArray(value, redactDiagnosticValue)
	default:
		return value, false, nil
	}
}

type jsonValueTransformer func(json.RawMessage) (json.RawMessage, bool, error)

func transformJSONObject(
	raw json.RawMessage,
	transform func(string, json.RawMessage) (json.RawMessage, bool, error),
) (json.RawMessage, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, false, fmt.Errorf("expected object")
	}
	var output bytes.Buffer
	output.WriteByte('{')
	changed := false
	for first := true; decoder.More(); first = false {
		if !first {
			output.WriteByte(',')
		}
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false, fmt.Errorf("expected object member name")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false, err
		}
		transformed, valueChanged, err := transform(key, value)
		if err != nil {
			return nil, false, err
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, false, err
		}
		output.Write(encodedKey)
		output.WriteByte(':')
		output.Write(transformed)
		changed = changed || valueChanged
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false, err
	}
	output.WriteByte('}')
	if !changed {
		return raw, false, nil
	}
	return output.Bytes(), true, nil
}

func transformJSONArray(raw json.RawMessage, transform jsonValueTransformer) (json.RawMessage, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, false, fmt.Errorf("expected array")
	}
	var output bytes.Buffer
	output.WriteByte('[')
	changed := false
	for first := true; decoder.More(); first = false {
		if !first {
			output.WriteByte(',')
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false, err
		}
		transformed, valueChanged, err := transform(value)
		if err != nil {
			return nil, false, err
		}
		output.Write(transformed)
		changed = changed || valueChanged
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false, err
	}
	output.WriteByte(']')
	if !changed {
		return raw, false, nil
	}
	return output.Bytes(), true, nil
}
