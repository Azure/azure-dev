// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExtractInvokeExample returns a compact JSON-encoded sample payload from
// the agent's OpenAPI spec, suitable for use as the literal argument to
// `azd ai agent invoke '<payload>'`. The walk follows the resolution
// order documented in the design:
//
//  1. paths./invocations.post.requestBody.content.application/json.example
//  2. ...schema.example
//  3. Generated from schema.required + schema.properties[*].example
//  4. "" — caller falls back to the protocol-generic payload
//
// All errors are silent. The function never returns an error: if the
// spec is malformed, missing the /invocations path, or uses $ref for
// any of the above nodes, the result is "" and the caller uses the
// protocol-generic fallback.
func ExtractInvokeExample(spec []byte) string {
	if len(spec) == 0 {
		return ""
	}

	var root map[string]any
	if err := json.Unmarshal(spec, &root); err != nil {
		return ""
	}

	jsonContent := walkInvokeJSONContent(root)
	if jsonContent == nil {
		return ""
	}

	if example, ok := jsonContent["example"]; ok {
		if encoded, ok := encodeCompactJSON(example); ok {
			return encoded
		}
	}

	schema, ok := jsonContent["schema"].(map[string]any)
	if !ok {
		return ""
	}

	// $ref short-circuits the walk per the design's out-of-scope note.
	if _, hasRef := schema["$ref"]; hasRef {
		return ""
	}

	if example, ok := schema["example"]; ok {
		if encoded, ok := encodeCompactJSON(example); ok {
			return encoded
		}
	}

	if payload, ok := payloadFromRequiredProperties(schema); ok {
		if encoded, ok := encodeCompactJSON(payload); ok {
			return encoded
		}
	}

	return ""
}

// walkInvokeJSONContent returns the application/json content node under
// paths./invocations.post.requestBody.content, or nil on any miss.
func walkInvokeJSONContent(root map[string]any) map[string]any {
	paths, ok := root["paths"].(map[string]any)
	if !ok {
		return nil
	}
	invocations, ok := paths["/invocations"].(map[string]any)
	if !ok {
		return nil
	}
	post, ok := invocations["post"].(map[string]any)
	if !ok {
		return nil
	}
	requestBody, ok := post["requestBody"].(map[string]any)
	if !ok {
		return nil
	}
	if _, hasRef := requestBody["$ref"]; hasRef {
		return nil
	}
	content, ok := requestBody["content"].(map[string]any)
	if !ok {
		return nil
	}
	jsonContent, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil
	}
	return jsonContent
}

// payloadFromRequiredProperties builds a minimal object from the schema's
// `required` array and each required property's `example`. Properties
// without an example or with non-object property entries are skipped;
// if the result is empty, the second return is false.
func payloadFromRequiredProperties(schema map[string]any) (map[string]any, bool) {
	required, ok := schema["required"].([]any)
	if !ok || len(required) == 0 {
		return nil, false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}

	out := make(map[string]any, len(required))
	for _, name := range required {
		key, ok := name.(string)
		if !ok || key == "" {
			continue
		}
		prop, ok := properties[key].(map[string]any)
		if !ok {
			continue
		}
		example, ok := prop["example"]
		if !ok {
			continue
		}
		out[key] = example
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// encodeCompactJSON returns the compact JSON encoding of v with no
// trailing newline; functions/channels and other non-encodable values
// produce ok=false.
func encodeCompactJSON(v any) (string, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ReadCachedOpenAPISpec returns the bytes of the on-disk OpenAPI cache
// produced by the extension's fetchOpenAPISpec helper for the given
// agent name and suffix ("local" or "remote"). Returns (nil, nil) when
// the file does not exist so callers can fall back without branching on
// the error type.
//
// configDir is the directory containing the active azd project's
// azure.yaml — the same directory fetchOpenAPISpec writes into.
//
// agentName is sanitized identically to fetchOpenAPISpec to keep the
// resolver and the writer in lockstep: any drift in the sanitization
// rule would let the resolver miss a freshly-cached spec.
func ReadCachedOpenAPISpec(configDir, agentName, suffix string) ([]byte, error) {
	if configDir == "" || agentName == "" || suffix == "" {
		return nil, fmt.Errorf("configDir, agentName, and suffix are required")
	}
	path := filepath.Join(configDir, fmt.Sprintf("openapi-%s-%s.json", sanitizeAgentName(agentName), suffix))
	bytes, err := os.ReadFile(path) //nolint:gosec // G304: configDir is the active project dir; agentName is sanitized
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return bytes, nil
}

// sanitizeAgentName mirrors the writer-side cleanup in fetchOpenAPISpec
// (cmd/helpers.go): strip path-traversal sequences and path separators
// so the resulting filename component stays inside configDir.
func sanitizeAgentName(name string) string {
	safe := strings.ReplaceAll(name, "..", "_")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return safe
}
