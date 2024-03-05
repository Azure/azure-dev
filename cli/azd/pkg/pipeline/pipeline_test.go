// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ConfigOptions_SecretsAndVars(t *testing.T) {
	// Initialize the ConfigOptions instance
	projectVariables := []string{"var1", "var2"}
	projectSecrets := []string{"secret1"}

	// Define the initial variables, secrets, and environment
	initialVariables := map[string]string{
		"azdVar": "foo",
	}
	initialSecrets := map[string]string{
		"azdSecret": "foo",
	}
	env := map[string]string{
		"var1":    "foo",
		"var2":    "bar",
		"secret1": "foo",
		"secret2": "value3",
		"exraVar": "value4",
	}

	// Call the SecretsAndVars function
	variables, secrets := mergeProjectVariablesAndSecrets(
		projectVariables, projectSecrets, initialVariables, initialSecrets, env)

	// Assert the expected results
	expectedVariables := map[string]string{
		"azdVar": "foo",
		"var1":   "foo",
		"var2":   "bar",
	}
	expectedSecrets := map[string]string{
		"azdSecret": "foo",
		"secret1":   "foo",
	}
	assert.Equal(t, expectedVariables, variables)
	assert.Equal(t, expectedSecrets, secrets)
}
