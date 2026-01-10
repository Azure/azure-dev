// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// CompileSchema compiles a JSON Schema for validation.
// Accepts either *jsonschema.Schema (from invopop/jsonschema) or json.RawMessage.
// Returns a compiled schema from santhosh-tekuri/jsonschema/v6 for validation.
func CompileSchema(schema *jsonschema.Schema) (*jsonschemav6.Schema, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	// Marshal the invopop schema to JSON
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}

	var schemaData interface{}
	if err := json.Unmarshal(schemaBytes, &schemaData); err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}

	// Compile the schema using santhosh-tekuri
	const resourceURI = "mem://extension-config.json"
	compiler := jsonschemav6.NewCompiler()
	if err := compiler.AddResource(resourceURI, schemaData); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	compiled, err := compiler.Compile(resourceURI)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return compiled, nil
}

// ValidateAgainstSchema validates data against a JSON Schema.
// Returns nil if validation succeeds, or a detailed error if it fails.
func ValidateAgainstSchema(schema *jsonschema.Schema, data interface{}) error {
	compiled, err := CompileSchema(schema)
	if err != nil {
		return fmt.Errorf("schema compilation failed: %w", err)
	}

	if err := compiled.Validate(data); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return nil
}
