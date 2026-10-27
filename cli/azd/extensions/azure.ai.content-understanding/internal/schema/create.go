// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package schema implements the local structural rules shared by analyzer workflows.
package schema

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"azure.ai.content-understanding/internal/cuclient"
)

var customIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)
var fieldTypes = []string{"array", "boolean", "date", "integer", "json", "number", "object", "string", "time"}
var fieldMethods = []string{"classify", "extract", "generate"}

// ValidateCustomAnalyzerID applies the create contract, not the broader prebuilt ID syntax.
func ValidateCustomAnalyzerID(id string) error {
	if !customIDPattern.MatchString(id) {
		return invalid("analyzerId", "Custom analyzer IDs require 1-64 ASCII letters, numbers, or underscores.")
	}
	return nil
}

// PrepareDefinition validates blocking PR2 structural rules without network access.
// Non-blocking authoring warnings belong to the separate analyzer validate workflow.
// Raw JSON values preserve unknown properties and exact numbers when normalizing the root object.
func PrepareDefinition(data []byte) (json.RawMessage, string, error) {
	root, ok := object(data)
	if !utf8.Valid(data) || !ok {
		return nil, "", invalid("$", "The schema must be a UTF-8 JSON object.")
	}
	if raw := alias(root, "analyzerId", "analyzer_id"); present(raw) {
		id, ok := text(raw)
		if !ok {
			return nil, "", invalid("analyzerId", "The analyzer ID must be a string.")
		}
		if err := ValidateCustomAnalyzerID(id); err != nil {
			return nil, "", err
		}
	}
	if raw := alias(root, "baseAnalyzerId", "base_analyzer_id"); present(raw) {
		id, ok := text(raw)
		if !ok || cuclient.ValidateAnalyzerID(id) != nil {
			return nil, "", invalid("baseAnalyzerId", "Specify a valid base analyzer ID.")
		}
	}
	pinned := ""
	if raw := root["apiVersion"]; present(raw) {
		var ok bool
		if pinned, ok = text(raw); !ok || pinned == "" {
			return nil, "", invalid("apiVersion", "The pinned API version must be a nonempty string.")
		}
	}
	if raw := root["models"]; present(raw) {
		if _, ok := object(raw); !ok {
			return nil, "", invalid("models", "Model roles must be an object.")
		}
	}
	if raw := root["config"]; present(raw) {
		config, ok := object(raw)
		if !ok {
			return nil, "", invalid("config", "Analyzer config must be an object.")
		}
		for _, key := range []string{"enableSegment", "segmentPerPage", "estimateFieldSourceAndConfidence"} {
			if err := optionalBool(config[key], "config."+key); err != nil {
				return nil, "", err
			}
		}
		if raw := config["contentCategories"]; present(raw) {
			categories, ok := object(raw)
			if !ok || len(categories) == 0 {
				return nil, "", invalid("config.contentCategories", "Content categories must be a nonempty object.")
			}
			for _, name := range slices.Sorted(maps.Keys(categories)) {
				category, ok := object(categories[name])
				path := "config.contentCategories." + name
				if !ok {
					return nil, "", invalid(path, "Each category definition must be an object.")
				}
				if err := optionalText(category["description"], path+".description"); err != nil {
					return nil, "", err
				}
				if raw := category["analyzerId"]; present(raw) {
					id, ok := text(raw)
					if !ok || cuclient.ValidateAnalyzerID(id) != nil {
						return nil, "", invalid(path+".analyzerId", "Specify a valid category analyzer ID.")
					}
				}
			}
		}
	}
	if raw := alias(root, "fieldSchema", "field_schema"); present(raw) {
		fields, ok := object(raw)
		if !ok {
			return nil, "", invalid("fieldSchema", "The field schema must be an object.")
		}
		for _, key := range []string{"fields", "definitions"} {
			if raw := fields[key]; present(raw) {
				if err := fieldMap(raw, "fieldSchema."+key); err != nil {
					return nil, "", err
				}
			}
		}
	}
	body, err := json.Marshal(root)
	return body, pinned, err
}

func fieldMap(raw json.RawMessage, path string) error {
	fields, ok := object(raw)
	if !ok {
		return invalid(path, "Field definitions must be an object.")
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if err := field(fields[name], path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func field(raw json.RawMessage, path string) error {
	definition, ok := object(raw)
	if !ok {
		return invalid(path, "Each field definition must be an object.")
	}
	if _, present := definition["$ref"]; present {
		return invalid(path+".$ref", "References are unsupported; inline the field definition.")
	}
	kind, _ := text(definition["type"])
	if !slices.Contains(fieldTypes, kind) {
		return invalid(path+".type",
			"Specify a field type: array, boolean, date, integer, json, number, object, string, or time.")
	}
	if err := optionalText(definition["description"], path+".description"); err != nil {
		return err
	}
	method, _ := text(definition["method"])
	if present(definition["method"]) && !slices.Contains(fieldMethods, method) {
		return invalid(path+".method", "Specify classify, extract, or generate.")
	}
	if method == "classify" {
		var values []json.RawMessage
		if err := json.Unmarshal(definition["enum"], &values); err != nil {
			return invalid(path+".enum", "Classify fields require at least two distinct enum values.")
		}
		unique := make(map[string]bool)
		for _, raw := range values {
			value, ok := text(raw)
			if !ok {
				value = string(raw)
			}
			unique[value] = true
		}
		if len(unique) < 2 {
			return invalid(path+".enum", "Classify fields require at least two distinct enum values.")
		}
	}
	if kind == "array" {
		if err := field(definition["items"], path+".items"); err != nil {
			return err
		}
	}
	if kind == "object" {
		properties, ok := object(definition["properties"])
		if !ok || len(properties) == 0 {
			return invalid(path+".properties", "Object fields require nonempty properties.")
		}
		if err := fieldMap(definition["properties"], path+".properties"); err != nil {
			return err
		}
	}
	return optionalBool(definition["estimateSourceAndConfidence"], path+".estimateSourceAndConfidence")
}

func object(raw []byte) (map[string]json.RawMessage, bool) {
	var value map[string]json.RawMessage
	err := json.Unmarshal(raw, &value)
	return value, err == nil && value != nil
}

func text(raw json.RawMessage) (string, bool) {
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err == nil && string(raw) != "null"
}

func present(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

func alias(root map[string]json.RawMessage, primary, secondary string) json.RawMessage {
	raw := root[primary]
	if !present(raw) {
		return root[secondary]
	}
	return raw
}

func optionalText(raw json.RawMessage, path string) error {
	if present(raw) {
		if _, ok := text(raw); !ok {
			return invalid(path, "The value must be a string.")
		}
	}
	return nil
}

func optionalBool(raw json.RawMessage, path string) error {
	if present(raw) && string(raw) != "true" && string(raw) != "false" {
		return invalid(path, "The value must be a boolean.")
	}
	return nil
}

func invalid(path, message string) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorValidation, Code: "invalid_analyzer_schema",
		Message: "Invalid analyzer schema at " + strings.ToValidUTF8(path, "?") + ": " + message,
	}
}
