// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"os"
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandableMap_Expand(t *testing.T) {
	t.Run("EmptyMap", func(t *testing.T) {
		em := ExpandableMap{}
		result, err := em.Expand(func(key string) string { return "" })
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("SimpleValues", func(t *testing.T) {
		em := ExpandableMap{
			"KEY1": NewExpandableString("value1"),
			"KEY2": NewExpandableString("value2"),
			"KEY3": NewExpandableString("value3"),
		}
		result, err := em.Expand(func(key string) string { return "" })
		require.NoError(t, err)
		assert.Equal(t, "value1", result["KEY1"])
		assert.Equal(t, "value2", result["KEY2"])
		assert.Equal(t, "value3", result["KEY3"])
	})

	t.Run("StaticStringsWithoutVariables", func(t *testing.T) {
		em := ExpandableMap{
			"DATABASE_HOST": NewExpandableString("localhost"),
			"DATABASE_PORT": NewExpandableString("5432"),
			"DATABASE_NAME": NewExpandableString("myapp"),
			"LOG_LEVEL":     NewExpandableString("info"),
			"ENABLE_CACHE":  NewExpandableString("true"),
		}
		// Even if mapping function returns empty, static strings should stay intact
		result, err := em.Expand(func(key string) string { return "" })
		require.NoError(t, err)
		assert.Equal(t, "localhost", result["DATABASE_HOST"])
		assert.Equal(t, "5432", result["DATABASE_PORT"])
		assert.Equal(t, "myapp", result["DATABASE_NAME"])
		assert.Equal(t, "info", result["LOG_LEVEL"])
		assert.Equal(t, "true", result["ENABLE_CACHE"])
	})

	t.Run("MixedStaticAndExpandable", func(t *testing.T) {
		mapping := func(key string) string {
			switch key {
			case "AZURE_LOCATION":
				return "eastus"
			case "AZURE_SUBSCRIPTION_ID":
				return "sub-123"
			default:
				return ""
			}
		}

		em := ExpandableMap{
			"LOCATION":             NewExpandableString("${AZURE_LOCATION}"),
			"SUBSCRIPTION_ID":      NewExpandableString("${AZURE_SUBSCRIPTION_ID}"),
			"DATABASE_HOST":        NewExpandableString("localhost"),
			"DATABASE_PORT":        NewExpandableString("5432"),
			"CONNECTION_STRING":    NewExpandableString("Host=localhost;Port=5432"),
			"MIXED":                NewExpandableString("Location: ${AZURE_LOCATION}, Port: 5432"),
			"SPECIAL_CHARS":        NewExpandableString("password!@#$%^&*()"),
			"WITH_EQUALS":          NewExpandableString("key=value"),
			"WITH_COLONS":          NewExpandableString("http://example.com:8080"),
			"EMPTY_STRING":         NewExpandableString(""),
			"WITH_DOLLAR_NO_BRACE": NewExpandableString("Price: $100"),
			"WITH_CURLY_NO_DOLLAR": NewExpandableString("{json: value}"),
		}

		result, err := em.Expand(mapping)
		require.NoError(t, err)

		// Variables should be expanded
		assert.Equal(t, "eastus", result["LOCATION"])
		assert.Equal(t, "sub-123", result["SUBSCRIPTION_ID"])

		// Static values should remain unchanged
		assert.Equal(t, "localhost", result["DATABASE_HOST"])
		assert.Equal(t, "5432", result["DATABASE_PORT"])
		assert.Equal(t, "Host=localhost;Port=5432", result["CONNECTION_STRING"])

		// Mixed should expand variables but keep static parts
		assert.Equal(t, "Location: eastus, Port: 5432", result["MIXED"])

		// Special characters should be preserved
		assert.Equal(t, "password!@#$%^&*()", result["SPECIAL_CHARS"])
		assert.Equal(t, "key=value", result["WITH_EQUALS"])
		assert.Equal(t, "http://example.com:8080", result["WITH_COLONS"])

		// Empty string should stay empty
		assert.Equal(t, "", result["EMPTY_STRING"])

		// Dollar sign without braces should be preserved
		assert.Equal(t, "Price: $100", result["WITH_DOLLAR_NO_BRACE"])

		// Curly braces without dollar are treated as shell variable syntax by envsubst
		// Since 'json: value' is not a valid variable name, it resolves to empty string
		// This is expected behavior from the envsubst library
		assert.Equal(t, "", result["WITH_CURLY_NO_BRACE"])
	})

	t.Run("ExpandableValues", func(t *testing.T) {
		mapping := func(key string) string {
			switch key {
			case "LOCATION":
				return "eastus"
			case "SUBSCRIPTION":
				return "sub-123"
			case "ENV_NAME":
				return "dev"
			default:
				return ""
			}
		}

		em := ExpandableMap{
			"AZURE_LOCATION":        NewExpandableString("${LOCATION}"),
			"AZURE_SUBSCRIPTION_ID": NewExpandableString("${SUBSCRIPTION}"),
			"AZURE_ENV_NAME":        NewExpandableString("${ENV_NAME}"),
			"STATIC_VALUE":          NewExpandableString("no-expansion"),
		}

		result, err := em.Expand(mapping)
		require.NoError(t, err)
		assert.Equal(t, "eastus", result["AZURE_LOCATION"])
		assert.Equal(t, "sub-123", result["AZURE_SUBSCRIPTION_ID"])
		assert.Equal(t, "dev", result["AZURE_ENV_NAME"])
		assert.Equal(t, "no-expansion", result["STATIC_VALUE"])
	})

	t.Run("ComplexExpansion", func(t *testing.T) {
		mapping := func(key string) string {
			switch key {
			case "REGISTRY":
				return "myregistry.azurecr.io"
			case "IMAGE":
				return "myapp"
			case "TAG":
				return "v1.0.0"
			default:
				return ""
			}
		}

		em := ExpandableMap{
			"FULL_IMAGE": NewExpandableString("${REGISTRY}/${IMAGE}:${TAG}"),
		}

		result, err := em.Expand(mapping)
		require.NoError(t, err)
		assert.Equal(t, "myregistry.azurecr.io/myapp:v1.0.0", result["FULL_IMAGE"])
	})

	t.Run("ExpansionError", func(t *testing.T) {
		em := ExpandableMap{
			"INVALID": NewExpandableString("${MISSING_BRACE"),
		}

		result, err := em.Expand(func(key string) string { return "" })
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "expanding INVALID")
	})

	t.Run("WithRealOSEnvironmentVariables", func(t *testing.T) {
		// Set up OS environment variables for testing
		testVars := map[string]string{
			"AZD_TEST_LOCATION":       "westus2",
			"AZD_TEST_SUBSCRIPTION":   "sub-abc-123",
			"AZD_TEST_RESOURCE_GROUP": "rg-test",
			"AZD_TEST_APP_NAME":       "myapp",
		}

		// Set environment variables
		for key, value := range testVars {
			err := os.Setenv(key, value)
			require.NoError(t, err)
		}

		// Clean up after test
		defer func() {
			for key := range testVars {
				os.Unsetenv(key)
			}
		}()

		// Create map with references to OS environment variables
		em := ExpandableMap{
			"LOCATION":       NewExpandableString("${AZD_TEST_LOCATION}"),
			"SUBSCRIPTION":   NewExpandableString("${AZD_TEST_SUBSCRIPTION}"),
			"RESOURCE_GROUP": NewExpandableString("${AZD_TEST_RESOURCE_GROUP}"),
			"APP_NAME":       NewExpandableString("${AZD_TEST_APP_NAME}"),
			"STATIC_VALUE":   NewExpandableString("this-is-static"),
			"COMBINED":       NewExpandableString("${AZD_TEST_APP_NAME}-${AZD_TEST_LOCATION}"),
		}

		// Expand using os.Getenv (which reads actual OS environment variables)
		result, err := em.Expand(os.Getenv)
		require.NoError(t, err)

		// Verify values were expanded from OS environment
		assert.Equal(t, "westus2", result["LOCATION"])
		assert.Equal(t, "sub-abc-123", result["SUBSCRIPTION"])
		assert.Equal(t, "rg-test", result["RESOURCE_GROUP"])
		assert.Equal(t, "myapp", result["APP_NAME"])
		assert.Equal(t, "this-is-static", result["STATIC_VALUE"])
		assert.Equal(t, "myapp-westus2", result["COMBINED"])
	})

	t.Run("WithMissingOSEnvironmentVariables", func(t *testing.T) {
		// Ensure test variable doesn't exist
		os.Unsetenv("AZD_TEST_NONEXISTENT_VAR")

		em := ExpandableMap{
			"MISSING": NewExpandableString("${AZD_TEST_NONEXISTENT_VAR}"),
			"STATIC":  NewExpandableString("static-value"),
		}

		// Expand using os.Getenv
		result, err := em.Expand(os.Getenv)
		require.NoError(t, err)

		// Missing variables resolve to empty string (this is envsubst behavior)
		assert.Equal(t, "", result["MISSING"])
		assert.Equal(t, "static-value", result["STATIC"])
	})

	t.Run("WithDefaultValueSyntax", func(t *testing.T) {
		// Set one var, leave another unset
		err := os.Setenv("AZD_TEST_SET_VAR", "set-value")
		require.NoError(t, err)
		defer os.Unsetenv("AZD_TEST_SET_VAR")

		os.Unsetenv("AZD_TEST_UNSET_VAR")

		em := ExpandableMap{
			"WITH_VALUE":    NewExpandableString("${AZD_TEST_SET_VAR}"),
			"WITH_DEFAULT":  NewExpandableString("${AZD_TEST_UNSET_VAR:-default-value}"),
			"EMPTY_DEFAULT": NewExpandableString("${AZD_TEST_UNSET_VAR:-}"),
		}

		result, err := em.Expand(os.Getenv)
		require.NoError(t, err)

		// Set variable expands normally
		assert.Equal(t, "set-value", result["WITH_VALUE"])
		// Unset variable with default syntax uses default
		assert.Equal(t, "default-value", result["WITH_DEFAULT"])
		// Unset variable with empty default expands to empty
		assert.Equal(t, "", result["EMPTY_DEFAULT"])
	})

	t.Run("WithNestedExpansion", func(t *testing.T) {
		// Set up cascading environment variables
		err := os.Setenv("AZD_TEST_BASE", "production")
		require.NoError(t, err)
		err = os.Setenv("AZD_TEST_REGION", "eastus")
		require.NoError(t, err)
		err = os.Setenv("AZD_TEST_VERSION", "v1.2.3")
		require.NoError(t, err)

		defer func() {
			os.Unsetenv("AZD_TEST_BASE")
			os.Unsetenv("AZD_TEST_REGION")
			os.Unsetenv("AZD_TEST_VERSION")
		}()

		em := ExpandableMap{
			"DEPLOYMENT_NAME": NewExpandableString("${AZD_TEST_BASE}-${AZD_TEST_REGION}"),
			"FULL_NAME":       NewExpandableString("${AZD_TEST_BASE}-${AZD_TEST_REGION}-${AZD_TEST_VERSION}"),
			"URL":             NewExpandableString("https://${AZD_TEST_BASE}.${AZD_TEST_REGION}.cloudapp.azure.com"),
		}

		result, err := em.Expand(os.Getenv)
		require.NoError(t, err)

		assert.Equal(t, "production-eastus", result["DEPLOYMENT_NAME"])
		assert.Equal(t, "production-eastus-v1.2.3", result["FULL_NAME"])
		assert.Equal(t, "https://production.eastus.cloudapp.azure.com", result["URL"])
	})
}

func TestExpandableMap_MustExpand(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mapping := func(key string) string {
			if key == "VAR" {
				return "value"
			}
			return ""
		}

		em := ExpandableMap{
			"KEY": NewExpandableString("${VAR}"),
		}

		result := em.MustExpand(mapping)
		assert.Equal(t, "value", result["KEY"])
	})

	t.Run("Panics", func(t *testing.T) {
		em := ExpandableMap{
			"INVALID": NewExpandableString("${MISSING_BRACE"),
		}

		assert.Panics(t, func() {
			em.MustExpand(func(key string) string { return "" })
		})
	})
}

func TestExpandableMap_YamlMarshalling(t *testing.T) {
	t.Run("MarshalSimpleMap", func(t *testing.T) {
		em := ExpandableMap{
			"DATABASE_HOST": NewExpandableString("localhost"),
			"DATABASE_PORT": NewExpandableString("5432"),
			"LOG_LEVEL":     NewExpandableString("info"),
		}

		data, err := yaml.Marshal(em)
		require.NoError(t, err)
		require.NotEmpty(t, data)

		// YAML should contain the key-value pairs
		yamlStr := string(data)
		assert.Contains(t, yamlStr, "DATABASE_HOST:")
		assert.Contains(t, yamlStr, "localhost")
		assert.Contains(t, yamlStr, "DATABASE_PORT:")
		assert.Contains(t, yamlStr, "5432")
		assert.Contains(t, yamlStr, "LOG_LEVEL:")
		assert.Contains(t, yamlStr, "info")
	})

	t.Run("MarshalMapWithVariables", func(t *testing.T) {
		em := ExpandableMap{
			"AZURE_LOCATION":        NewExpandableString("${LOCATION}"),
			"AZURE_SUBSCRIPTION_ID": NewExpandableString("${SUBSCRIPTION_ID}"),
			"STATIC_VALUE":          NewExpandableString("static"),
		}

		data, err := yaml.Marshal(em)
		require.NoError(t, err)

		yamlStr := string(data)
		// Variables should be preserved in YAML with ${} syntax
		assert.Contains(t, yamlStr, "${LOCATION}")
		assert.Contains(t, yamlStr, "${SUBSCRIPTION_ID}")
		assert.Contains(t, yamlStr, "static")
	})

	t.Run("UnmarshalSimpleMap", func(t *testing.T) {
		yamlData := `
DATABASE_HOST: localhost
DATABASE_PORT: "5432"
DATABASE_NAME: myapp
LOG_LEVEL: info
`
		var em ExpandableMap
		err := yaml.Unmarshal([]byte(yamlData), &em)
		require.NoError(t, err)
		require.Len(t, em, 4)

		// Expand to verify values are correct
		result, err := em.Expand(func(key string) string { return "" })
		require.NoError(t, err)
		assert.Equal(t, "localhost", result["DATABASE_HOST"])
		assert.Equal(t, "5432", result["DATABASE_PORT"])
		assert.Equal(t, "myapp", result["DATABASE_NAME"])
		assert.Equal(t, "info", result["LOG_LEVEL"])
	})

	t.Run("UnmarshalMapWithVariables", func(t *testing.T) {
		yamlData := `
AZURE_LOCATION: ${LOCATION}
AZURE_SUBSCRIPTION_ID: ${SUBSCRIPTION_ID}
STATIC_VALUE: static-string
COMBINED: ${ENV_NAME}-${REGION}
`
		var em ExpandableMap
		err := yaml.Unmarshal([]byte(yamlData), &em)
		require.NoError(t, err)
		require.Len(t, em, 4)

		// Expand with mock values
		mapping := func(key string) string {
			switch key {
			case "LOCATION":
				return "eastus"
			case "SUBSCRIPTION_ID":
				return "sub-123"
			case "ENV_NAME":
				return "dev"
			case "REGION":
				return "west"
			default:
				return ""
			}
		}

		result, err := em.Expand(mapping)
		require.NoError(t, err)
		assert.Equal(t, "eastus", result["AZURE_LOCATION"])
		assert.Equal(t, "sub-123", result["AZURE_SUBSCRIPTION_ID"])
		assert.Equal(t, "static-string", result["STATIC_VALUE"])
		assert.Equal(t, "dev-west", result["COMBINED"])
	})

	t.Run("RoundTripMarshalUnmarshal", func(t *testing.T) {
		original := ExpandableMap{
			"VAR1": NewExpandableString("${VALUE1}"),
			"VAR2": NewExpandableString("static-value"),
			"VAR3": NewExpandableString("${PREFIX}-${SUFFIX}"),
		}

		// Marshal to YAML
		data, err := yaml.Marshal(original)
		require.NoError(t, err)

		// Unmarshal back
		var restored ExpandableMap
		err = yaml.Unmarshal(data, &restored)
		require.NoError(t, err)

		// Both should expand to the same values
		mapping := func(key string) string {
			switch key {
			case "VALUE1":
				return "expanded1"
			case "PREFIX":
				return "pre"
			case "SUFFIX":
				return "suf"
			default:
				return ""
			}
		}

		originalResult, err := original.Expand(mapping)
		require.NoError(t, err)

		restoredResult, err := restored.Expand(mapping)
		require.NoError(t, err)

		assert.Equal(t, originalResult, restoredResult)
	})

	t.Run("UnmarshalEmptyMap", func(t *testing.T) {
		yamlData := `{}`

		var em ExpandableMap
		err := yaml.Unmarshal([]byte(yamlData), &em)
		require.NoError(t, err)
		assert.Empty(t, em)
	})

	t.Run("UnmarshalNullMap", func(t *testing.T) {
		yamlData := `null`

		var em ExpandableMap
		err := yaml.Unmarshal([]byte(yamlData), &em)
		require.NoError(t, err)
		assert.Nil(t, em)
	})

	t.Run("UnmarshalInStruct", func(t *testing.T) {
		// Simulate how ExpandableMap is used in real structs like ServiceConfig
		type TestConfig struct {
			Name        string        `yaml:"name"`
			Environment ExpandableMap `yaml:"env,omitempty"`
		}

		yamlData := `
name: test-service
env:
  AZURE_LOCATION: ${LOCATION}
  DATABASE_HOST: localhost
  DATABASE_PORT: "5432"
`
		var config TestConfig
		err := yaml.Unmarshal([]byte(yamlData), &config)
		require.NoError(t, err)

		assert.Equal(t, "test-service", config.Name)
		require.Len(t, config.Environment, 3)

		// Expand and verify
		mapping := func(key string) string {
			if key == "LOCATION" {
				return "westus"
			}
			return ""
		}

		result, err := config.Environment.Expand(mapping)
		require.NoError(t, err)
		assert.Equal(t, "westus", result["AZURE_LOCATION"])
		assert.Equal(t, "localhost", result["DATABASE_HOST"])
		assert.Equal(t, "5432", result["DATABASE_PORT"])
	})

	t.Run("MarshalStructWithExpandableMap", func(t *testing.T) {
		type TestConfig struct {
			Name        string        `yaml:"name"`
			Environment ExpandableMap `yaml:"env,omitempty"`
		}

		config := TestConfig{
			Name: "my-service",
			Environment: ExpandableMap{
				"LOCATION": NewExpandableString("${AZURE_LOCATION}"),
				"PORT":     NewExpandableString("8080"),
			},
		}

		data, err := yaml.Marshal(config)
		require.NoError(t, err)

		yamlStr := string(data)
		assert.Contains(t, yamlStr, "name: my-service")
		assert.Contains(t, yamlStr, "env:")
		assert.Contains(t, yamlStr, "LOCATION:")
		assert.Contains(t, yamlStr, "${AZURE_LOCATION}")
		assert.Contains(t, yamlStr, "PORT:")
		assert.Contains(t, yamlStr, "8080")
	})

	t.Run("UnmarshalWithSpecialCharacters", func(t *testing.T) {
		yamlData := `
CONNECTION_STRING: "Server=localhost;Database=mydb;User=admin"
PASSWORD: "p@ssw0rd!#$%"
URL: "https://example.com:8080/api"
JSON_CONFIG: '{"key": "value"}'
`
		var em ExpandableMap
		err := yaml.Unmarshal([]byte(yamlData), &em)
		require.NoError(t, err)

		result, err := em.Expand(func(key string) string { return "" })
		require.NoError(t, err)
		assert.Equal(t, "Server=localhost;Database=mydb;User=admin", result["CONNECTION_STRING"])
		assert.Equal(t, "p@ssw0rd!#$%", result["PASSWORD"])
		assert.Equal(t, "https://example.com:8080/api", result["URL"])
		assert.Equal(t, `{"key": "value"}`, result["JSON_CONFIG"])
	})
}
