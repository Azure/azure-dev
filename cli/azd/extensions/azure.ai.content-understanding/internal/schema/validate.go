// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"azure.ai.content-understanding/internal/cuclient"
)

type Finding struct {
	Path    string `json:"path"`
	Message string `json:"msg"`
}

// DiagnosticPath keeps contract-defined locations, never customer-authored property names.
func (f Finding) DiagnosticPath() string {
	switch f.Path {
	case "$", "analyzerId", "baseAnalyzerId", "apiVersion", "models", "models.completion",
		"config", "config.contentCategories", "fieldSchema", "fieldSchema.fields":
		return f.Path
	default:
		return "[redacted field path]"
	}
}

type ValidationResult struct {
	OK       bool      `json:"ok"`
	Errors   []Finding `json:"errors"`
	Warnings []Finding `json:"warnings"`
}

// Validate collects authoring findings without changing the create command's
// blocking-only validation or requiring a service/host connection.
func Validate(data []byte, version string, spec bool) (ValidationResult, error) {
	result := ValidationResult{Errors: []Finding{}, Warnings: []Finding{}}
	version, err := cuclient.NormalizeAPIVersion(version)
	if err != nil {
		return result, err
	}
	root, ok := object(data)
	if !utf8.Valid(data) || !ok {
		result.fail("$", "The schema must be a UTF-8 JSON object.")
		return result, nil
	}
	result.root(root, version)
	if spec {
		findings, err := validateSpec(data, version)
		if err != nil {
			return result, err
		}
		result.Errors = append(result.Errors, findings...)
	}
	result.OK = len(result.Errors) == 0
	return result, nil
}

func (r *ValidationResult) fail(path, message string) {
	r.Errors = append(r.Errors, Finding{path, message})
}

func (r *ValidationResult) warn(path, message string) {
	r.Warnings = append(r.Warnings, Finding{path, message})
}

func (r *ValidationResult) root(root map[string]json.RawMessage, version string) {
	if raw := authoringAlias(root, "analyzerId", "analyzer_id"); present(raw) {
		id, ok := text(raw)
		if !ok || ValidateCustomAnalyzerID(id) != nil {
			r.fail("analyzerId", "Custom analyzer IDs require 1-64 ASCII letters, numbers, or underscores.")
		}
	}
	if raw := authoringAlias(root, "baseAnalyzerId", "base_analyzer_id"); present(raw) {
		id, ok := text(raw)
		if !ok || cuclient.ValidateAnalyzerID(id) != nil {
			r.fail("baseAnalyzerId", "Specify a valid base analyzer ID.")
		}
	} else {
		r.warn("baseAnalyzerId", "No baseAnalyzerId; recommend prebuilt-document.")
	}
	if raw := root["apiVersion"]; present(raw) {
		pinned, ok := text(raw)
		if !ok || pinned != version {
			r.fail("apiVersion", "The schema's pinned API version does not match the selected API version.")
		}
	}
	models, modelsOK := object(root["models"])
	if present(root["models"]) && !modelsOK {
		r.fail("models", "Model roles must be an object.")
	}
	categories := false
	if raw := root["config"]; present(raw) {
		cfg, ok := object(raw)
		if !ok {
			r.fail("config", "Analyzer config must be an object.")
		} else {
			for _, key := range []string{"enableSegment", "segmentPerPage", "estimateFieldSourceAndConfidence"} {
				r.boolean(cfg[key], "config."+key)
			}
			if raw := cfg["contentCategories"]; present(raw) {
				items, ok := object(raw)
				if !ok || len(items) == 0 {
					r.fail("config.contentCategories", "Content categories must be a nonempty object.")
				} else {
					categories = true
					for _, name := range slices.Sorted(maps.Keys(items)) {
						path := "config.contentCategories." + name
						category, ok := object(items[name])
						if !ok {
							r.fail(path, "Each category definition must be an object.")
							continue
						}
						r.description(category["description"], path+".description", false)
						if raw := category["analyzerId"]; present(raw) {
							id, ok := text(raw)
							if !ok || cuclient.ValidateAnalyzerID(id) != nil {
								r.fail(path+".analyzerId", "Specify a valid category analyzer ID.")
							}
						}
					}
				}
			}
		}
	}
	raw := authoringAlias(root, "fieldSchema", "field_schema")
	if !present(raw) {
		if !categories {
			r.warn("fieldSchema", "No fieldSchema; layout/markdown only.")
		}
		return
	}
	fields, ok := object(raw)
	if !ok {
		r.fail("fieldSchema", "The field schema must be an object.")
		return
	}
	if !present(fields["fields"]) {
		r.warn("fieldSchema.fields", "No fields; layout only.")
	} else {
		if values, ok := object(fields["fields"]); ok && len(values) != 0 {
			completion, _ := text(models["completion"])
			if completion == "" {
				r.warn("models.completion", "Field extraction needs a completion model, for example gpt-5.2.")
			}
		}
		r.fields(fields["fields"], "fieldSchema.fields", 1)
	}
	if present(fields["definitions"]) {
		r.fields(fields["definitions"], "fieldSchema.definitions", 1)
	}
}

func (r *ValidationResult) fields(raw json.RawMessage, path string, depth int) {
	fields, ok := object(raw)
	if !ok {
		r.fail(path, "Field definitions must be an object.")
		return
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		r.field(fields[name], path+"."+name, depth)
	}
}

func (r *ValidationResult) field(raw json.RawMessage, path string, depth int) {
	definition, ok := object(raw)
	if !ok {
		r.fail(path, "Each field definition must be an object.")
		return
	}
	if _, present := definition["$ref"]; present {
		r.fail(path+".$ref", "References are unsupported; inline the field definition.")
		return
	}
	kind, _ := text(definition["type"])
	if !slices.Contains(fieldTypes, kind) {
		r.fail(path+".type", "Specify a field type: "+strings.Join(fieldTypes, ", ")+".")
	}
	r.description(definition["description"], path+".description", true)
	method, _ := text(definition["method"])
	if present(definition["method"]) && !slices.Contains(fieldMethods, method) {
		r.fail(path+".method", "Specify classify, extract, or generate.")
	}
	if method == "classify" {
		var values []json.RawMessage
		unique := map[string]bool{}
		if json.Unmarshal(definition["enum"], &values) == nil {
			for _, raw := range values {
				value, ok := text(raw)
				if !ok {
					value = string(raw)
				}
				unique[value] = true
			}
		}
		if len(unique) < 2 {
			r.fail(path+".enum", "Classify fields require at least two distinct enum values.")
		}
	}
	if kind == "array" {
		if _, ok := object(definition["items"]); !ok {
			r.fail(path+".items", "Array fields require an items object.")
		} else {
			r.depth(path+".items", depth+1)
			r.field(definition["items"], path+".items", depth+1)
		}
	}
	if kind == "object" {
		if properties, ok := object(definition["properties"]); !ok || len(properties) == 0 {
			r.fail(path+".properties", "Object fields require nonempty properties.")
		} else {
			r.depth(path+".properties", depth+1)
			r.fields(definition["properties"], path+".properties", depth+1)
		}
	}
	r.boolean(definition["estimateSourceAndConfidence"], path+".estimateSourceAndConfidence")
}

func (r *ValidationResult) boolean(raw json.RawMessage, path string) {
	if optionalBool(raw, path) != nil {
		r.fail(path, "The value must be a boolean.")
	}
}

func (r *ValidationResult) description(raw json.RawMessage, path string, shortWarning bool) {
	value, ok := text(raw)
	switch {
	case !present(raw) || (ok && strings.TrimSpace(value) == ""):
		r.warn(path, "No description; use a specific authoring prompt.")
	case !ok:
		r.fail(path, "The description must be a string.")
	case shortWarning && utf8.RuneCountInString(strings.TrimSpace(value)) < 15:
		r.warn(path, "The description is very short; be specific.")
	}
}

func (r *ValidationResult) depth(path string, depth int) {
	if depth > 4 {
		r.warn(path, "Nesting depth "+strconv.Itoa(depth)+" exceeds 4; consider flattening for extraction accuracy.")
	}
}

// Shared Python authoring validation uses `primary or secondary`, so an empty
// primary container/value falls back to the alias. Spec validation still checks
// the original, unmodified body and rejects values outside the service contract.
func authoringAlias(root map[string]json.RawMessage, primary, secondary string) json.RawMessage {
	raw := root[primary]
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	empty := value == nil
	switch value := value.(type) {
	case bool:
		empty = !value
	case float64:
		empty = value == 0
	case string:
		empty = value == ""
	case map[string]any:
		empty = len(value) == 0
	case []any:
		empty = len(value) == 0
	}
	if empty {
		return root[secondary]
	}
	return raw
}
