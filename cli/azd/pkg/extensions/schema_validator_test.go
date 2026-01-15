// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileSchema(t *testing.T) {
	props := jsonschema.NewProperties()
	props.Set("apiKey", &jsonschema.Schema{
		Type:        "string",
		Description: "API key for authentication",
	})
	props.Set("timeout", &jsonschema.Schema{
		Type:        "integer",
		Description: "Timeout in seconds",
		Minimum:     json.Number("1"),
	})

	schema := &jsonschema.Schema{
		Version:    jsonschema.Version,
		Type:       "object",
		Properties: props,
		Required:   []string{"apiKey"},
	}

	compiled, err := CompileSchema(schema)
	require.NoError(t, err)
	assert.NotNil(t, compiled)
}

func TestCompileSchema_NilSchema(t *testing.T) {
	_, err := CompileSchema(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")
}

func TestCompileSchema_InvalidSchema(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "invalid-type",
	}

	_, err := CompileSchema(schema)
	assert.Error(t, err)
}

func TestValidateAgainstSchema_Success(t *testing.T) {
	props := jsonschema.NewProperties()
	props.Set("name", &jsonschema.Schema{Type: "string"})
	props.Set("age", &jsonschema.Schema{
		Type:    "integer",
		Minimum: json.Number("0"),
	})

	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: props,
		Required:   []string{"name"},
	}

	validData := map[string]interface{}{
		"name": "John Doe",
		"age":  float64(30),
	}

	err := ValidateAgainstSchema(schema, validData)
	assert.NoError(t, err)
}

func TestValidateAgainstSchema_MissingRequired(t *testing.T) {
	props := jsonschema.NewProperties()
	props.Set("apiKey", &jsonschema.Schema{Type: "string"})

	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: props,
		Required:   []string{"apiKey"},
	}

	invalidData := map[string]interface{}{
		"otherField": "value",
	}

	err := ValidateAgainstSchema(schema, invalidData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestValidateAgainstSchema_WrongType(t *testing.T) {
	props := jsonschema.NewProperties()
	props.Set("count", &jsonschema.Schema{Type: "integer"})

	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: props,
	}

	invalidData := map[string]interface{}{
		"count": "not-a-number",
	}

	err := ValidateAgainstSchema(schema, invalidData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestValidateAgainstSchema_MinimumConstraint(t *testing.T) {
	props := jsonschema.NewProperties()
	props.Set("timeout", &jsonschema.Schema{
		Type:    "integer",
		Minimum: json.Number("1"),
	})

	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: props,
	}

	invalidData := map[string]interface{}{
		"timeout": float64(0),
	}

	err := ValidateAgainstSchema(schema, invalidData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestValidateAgainstSchema_ComplexSchema(t *testing.T) {
	serverProps := jsonschema.NewProperties()
	serverProps.Set("host", &jsonschema.Schema{Type: "string"})
	serverProps.Set("port", &jsonschema.Schema{
		Type:    "integer",
		Minimum: json.Number("1"),
		Maximum: json.Number("65535"),
	})

	props := jsonschema.NewProperties()
	props.Set("server", &jsonschema.Schema{
		Type:       "object",
		Properties: serverProps,
		Required:   []string{"host", "port"},
	})
	props.Set("features", &jsonschema.Schema{
		Type:  "array",
		Items: &jsonschema.Schema{Type: "string"},
	})

	schema := &jsonschema.Schema{
		Version:    jsonschema.Version,
		Type:       "object",
		Properties: props,
		Required:   []string{"server"},
	}

	validData := map[string]interface{}{
		"server": map[string]interface{}{
			"host": "localhost",
			"port": float64(8080),
		},
		"features": []interface{}{"auth", "logging"},
	}

	err := ValidateAgainstSchema(schema, validData)
	assert.NoError(t, err)
}

func TestConfigurationMetadata_JSONMarshaling(t *testing.T) {
	globalProps := jsonschema.NewProperties()
	globalProps.Set("apiKey", &jsonschema.Schema{Type: "string"})

	projectProps := jsonschema.NewProperties()
	projectProps.Set("projectName", &jsonschema.Schema{Type: "string"})

	config := ConfigurationMetadata{
		Global: &jsonschema.Schema{
			Type:       "object",
			Properties: globalProps,
		},
		Project: &jsonschema.Schema{
			Type:       "object",
			Properties: projectProps,
		},
	}

	// Test marshaling
	jsonBytes, err := json.Marshal(config)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), "apiKey")
	assert.Contains(t, string(jsonBytes), "projectName")

	// Test unmarshaling
	var unmarshaled ConfigurationMetadata
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	require.NoError(t, err)
	assert.NotNil(t, unmarshaled.Global)
	assert.NotNil(t, unmarshaled.Project)
	assert.Nil(t, unmarshaled.Service)
}

// TestConfigurationMetadata_FromGoTypes demonstrates the RECOMMENDED way for
// extension developers to define configuration schemas - using Go types with
// json tags and generating schemas via reflection.
func TestConfigurationMetadata_FromGoTypes(t *testing.T) {
	// Define configuration structures using Go types
	type CustomGlobalConfig struct {
		APIKey string `json:"apiKey"            jsonschema:"required,description=API key for authentication,minLength=10"`
		// Timeout in seconds (1-300, default 60)
		Timeout int  `json:"timeout,omitempty" jsonschema:"minimum=1,maximum=300,default=60"`
		Debug   bool `json:"debug,omitempty"   jsonschema:"description=Enable debug logging"`
	}

	type CustomProjectConfig struct {
		ProjectName string `json:"projectName"           jsonschema:"required,description=Name of the project"`
		// Deployment environment: dev, staging, or prod
		Environment string   `json:"environment,omitempty" jsonschema:"enum=dev,enum=staging,enum=prod"`
		Features    []string `json:"features,omitempty"    jsonschema:"description=Enabled features"`
	}

	type CustomServiceConfig struct {
		// Service port (1-65535)
		Port     int               `json:"port"               jsonschema:"required,minimum=1,maximum=65535"`
		HostName string            `json:"hostName,omitempty" jsonschema:"description=Service hostname"`
		Labels   map[string]string `json:"labels,omitempty"   jsonschema:"description=Service labels"`
	}

	// Generate schemas from Go types - THIS IS THE EASIEST WAY!
	config := ConfigurationMetadata{
		Global:  jsonschema.Reflect(&CustomGlobalConfig{}),
		Project: jsonschema.Reflect(&CustomProjectConfig{}),
		Service: jsonschema.Reflect(&CustomServiceConfig{}),
	}

	// Verify schemas were generated correctly
	require.NotNil(t, config.Global)
	require.NotNil(t, config.Project)
	require.NotNil(t, config.Service)

	// Test that generated schema validates correctly
	validGlobalData := map[string]interface{}{
		"apiKey":  "my-secret-key-123",
		"timeout": float64(30),
		"debug":   true,
	}

	err := ValidateAgainstSchema(config.Global, validGlobalData)
	assert.NoError(t, err)

	// Test validation failure for required field
	invalidGlobalData := map[string]interface{}{
		"timeout": float64(30),
		// missing required apiKey
	}

	err = ValidateAgainstSchema(config.Global, invalidGlobalData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")

	// Test validation failure for minimum constraint
	invalidGlobalData2 := map[string]interface{}{
		"apiKey":  "short", // violates minLength=10
		"timeout": float64(30),
	}

	err = ValidateAgainstSchema(config.Global, invalidGlobalData2)
	assert.Error(t, err)

	// Test enum validation
	validProjectData := map[string]interface{}{
		"projectName": "my-project",
		"environment": "staging",
		"features":    []interface{}{"auth", "logging"},
	}

	err = ValidateAgainstSchema(config.Project, validProjectData)
	assert.NoError(t, err)

	invalidProjectData := map[string]interface{}{
		"projectName": "my-project",
		"environment": "invalid-env", // not in enum
	}

	err = ValidateAgainstSchema(config.Project, invalidProjectData)
	assert.Error(t, err)

	// Test marshaling - the schema can be serialized to JSON
	jsonBytes, err := json.Marshal(config)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), "apiKey")
	assert.Contains(t, string(jsonBytes), "projectName")
	assert.Contains(t, string(jsonBytes), "port")
}
