package scaffold

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
)

func TestEvalSimple(t *testing.T) {
	// Create a simple resource spec
	resourceSpecYaml := `
name: test-resource
location: westus
`
	var resourceSpec yaml.Node
	err := yaml.Unmarshal([]byte(resourceSpecYaml), &resourceSpec)
	if err != nil {
		t.Fatal(err)
	}

	// Create a simple ARM resource
	armResource := `{
		"id": "test-id",
		"properties": {
			"name": "test-properties-name",
			"hostName": "test-host-name"
		}
	}`

	// Create a simple vault secret resolver
	vaultSecret := func(path string) (string, error) {
		return "secret-" + path, nil
	}

	// Create the evaluation context
	EvalEnv := EvalEnv{
		ResourceSpec: &resourceSpec,
		ArmResource:  armResource,
		VaultSecret:  vaultSecret,
	}

	// Test cases
	testCases := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			name: "Basic function test - concat",
			input: map[string]string{
				"prefix":    "prefix-",
				"suffix":    "-suffix",
				"full_name": "${concat \"prefix-\" spec.name \"-suffix\"}",
			},
			expected: map[string]string{
				"prefix":    "prefix-",
				"suffix":    "-suffix",
				"full_name": "prefix-test-resource-suffix",
			},
		},
		{
			name: "Function with property access - lower",
			input: map[string]string{
				"resource_id":   "${lower .id}",
				"resource_name": "${upper spec.name}",
			},
			expected: map[string]string{
				"resource_id":   "test-id",
				"resource_name": "TEST-RESOURCE",
			},
		},
		{
			name: "Replace function with double quotes",
			input: map[string]string{
				"original": "${.properties.hostName}",
				"modified": "${replace .properties.hostName \"test-\" \"modified-\"}",
			},
			expected: map[string]string{
				"original": "test-host-name",
				"modified": "modified-host-name",
			},
		},
		{
			name: "Replace function with single quotes",
			input: map[string]string{
				"modified": "${replace .properties.hostName 'test-' 'modified-'}",
			},
			expected: map[string]string{
				"modified": "modified-host-name",
			},
		},
		{
			name: "Variable reference as argument",
			input: map[string]string{
				"name":      "test-name",
				"formatted": "${upper name}",
			},
			expected: map[string]string{
				"name":      "test-name",
				"formatted": "TEST-NAME",
			},
		},
		{
			name: "Mixed argument types",
			input: map[string]string{
				"mixed": "${concat \"prefix-\" spec.name '-suffix'}",
			},
			expected: map[string]string{
				"mixed": "prefix-test-resource-suffix",
			},
		},
		{
			name: "Vault secret",
			input: map[string]string{
				"secret":    "${vault.my-secret}",
				"formatted": "${concat \"Secret: \" vault.my-secret}",
			},
			expected: map[string]string{
				"secret":    "secret-my-secret",
				"formatted": "Secret: secret-my-secret",
			},
		},
	}

	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Eval(tc.input, EvalEnv)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestEvalAdvanced(t *testing.T) {
	// Create custom function map
	customFuncMap := map[string]FunctionCall{}
	customFuncMap["prefix"] = func(args []string) (string, error) {
		if len(args) != 2 {
			return "", assert.AnError
		}
		return args[0] + "-" + args[1], nil
	}

	// Create test context
	resourceSpecYaml := `
name: myapp
location: eastus
`
	var resourceSpec yaml.Node
	err := yaml.Unmarshal([]byte(resourceSpecYaml), &resourceSpec)
	if err != nil {
		t.Fatal(err)
	}

	env := EvalEnv{
		ResourceSpec: &resourceSpec,
		ArmResource:  `{"id": "resource-id", "name": "resource-name"}`,
		VaultSecret:  func(s string) (string, error) { return "vault-" + s, nil },
		FuncMap:      customFuncMap,
	}

	// Test custom functions
	input := map[string]string{
		"base":       "base",
		"app_id":     "${prefix \"app\" spec.name}",
		"resource":   "${prefix \"res\" .name}",
		"combined":   "${concat app_id \"-\" resource}",
		"with_vault": "${concat vault.secretkey \"-\" \"custom\"}",
	}

	expected := map[string]string{
		"base":       "base",
		"app_id":     "app-myapp",
		"resource":   "res-resource-name",
		"combined":   "app-myapp-res-resource-name",
		"with_vault": "vault-secretkey-custom",
	}

	result, err := Eval(input, env)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}
