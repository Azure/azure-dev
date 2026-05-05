// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CachedOpenAPISpecPath returns the on-disk path where helpers.go writes
// the OpenAPI spec for an agent. It mirrors the layout used by
// fetchOpenAPISpec in cmd/helpers.go (kept in lock-step).
//
// configDir is typically filepath.Dir(<configPath>) — the per-extension
// config directory under the active azd environment. suffix is "local"
// or "remote".
func CachedOpenAPISpecPath(configDir, agentName, suffix string) string {
	safe := strings.ReplaceAll(agentName, "..", "_")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return filepath.Join(configDir, fmt.Sprintf("openapi-%s-%s.json", safe, suffix))
}

// ExtractInvokeExample reads the cached OpenAPI spec at specPath and
// returns a compact JSON example payload suitable for `azd ai agent
// invoke <payload>`. The returned string is the JSON-encoded value of
// the most relevant example for `POST /invocations`. When no example
// can be found the returned string is empty (the caller should fall
// back to a generic placeholder like "Hello!").
//
// Lookup order, all under paths./invocations.post.requestBody.content
// (preferring "application/json"):
//
//  1. content[ct].example
//  2. content[ct].examples[<first>].value
//  3. content[ct].schema.example (top-level)
//  4. a synthesized object from content[ct].schema.properties using
//     each property's `example` (or `default`) value, when the schema
//     is a plain `type: object` with at least one such property.
//
// The function is best-effort: any unexpected shape returns an empty
// string with no error. Returning an empty string is the canonical
// "no example" signal and is not treated as failure by callers.
func ExtractInvokeExample(specPath string) (string, error) {
	if specPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read openapi spec: %w", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return "", fmt.Errorf("parse openapi spec: %w", err)
	}

	post := getMap(getMap(getMap(spec, "paths"), "/invocations"), "post")
	body := getMap(getMap(post, "requestBody"), "content")
	if len(body) == 0 {
		return "", nil
	}

	ct := pickContentType(body)
	media := getMap(body, ct)
	if media == nil {
		return "", nil
	}

	if v, ok := media["example"]; ok {
		return marshalCompact(v)
	}
	if examples := getMap(media, "examples"); len(examples) > 0 {
		if first := firstExampleValue(examples); first != nil {
			return marshalCompact(first)
		}
	}
	schema := getMap(media, "schema")
	if v, ok := schema["example"]; ok {
		return marshalCompact(v)
	}
	if synth := synthesizeFromSchema(schema); synth != nil {
		return marshalCompact(synth)
	}
	return "", nil
}

// pickContentType prefers "application/json", falls back to the first
// content type encountered.
func pickContentType(content map[string]any) string {
	if _, ok := content["application/json"]; ok {
		return "application/json"
	}
	for k := range content {
		return k
	}
	return ""
}

// firstExampleValue extracts the .value field from the first entry in
// a swagger `examples` map. Order is not guaranteed but is acceptable
// here — most specs include a single example anyway.
func firstExampleValue(examples map[string]any) any {
	for _, entry := range examples {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := m["value"]; ok {
			return v
		}
	}
	return nil
}

// synthesizeFromSchema builds an object from a JSON Schema's
// `properties`, using each property's `example` or `default`. Only
// emits a result when at least one property contributes a value.
func synthesizeFromSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	props := getMap(schema, "properties")
	if len(props) == 0 {
		return nil
	}
	out := make(map[string]any, len(props))
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := p["example"]; ok {
			out[name] = v
			continue
		}
		if v, ok := p["default"]; ok {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func getMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	v, ok := parent[key]
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func marshalCompact(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	// Strings in OpenAPI examples are already valid payloads — return them as-is
	// (quoted) so callers can splice into shell commands directly.
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal example: %w", err)
	}
	return string(b), nil
}
