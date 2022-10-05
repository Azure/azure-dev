// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type testCommandExecutor struct {
	runImpl func(name string, args []string) (bool, string, error)
}

func (tc testCommandExecutor) Run(ctx context.Context, name string, args []string) (bool, string, error) {
	return tc.runImpl(name, args)
}

func TestEvalWorksWithEmptyInput(t *testing.T) {
	evaluatorCalled := false
	result, err := Eval(context.Background(), "", testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, "", result)
	require.False(t, evaluatorCalled)
}

func TestEmptyInvocation(t *testing.T) {
	// This is not a valid command, so it should be left alone
	evaluatorCalled := false
	result, err := Eval(context.Background(), " $()  ", testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, " $()  ", result)
	require.False(t, evaluatorCalled)
}

func TestEvalNoSubstitution(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "location": {
			"value": "${AZURE_LOCATION}"
		  },
		  "principalId": {
			"value": "${AZURE_PRINCIPAL_ID}"
		  }
		}
	  }`

	evaluatorCalled := false
	result, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, input, result)
	require.False(t, evaluatorCalled)
}

func TestSubstitution(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(randomPassword)"
		  } 
		}
	  }`
	const expected = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "very-secret"
		  } 
		}
	  }`

	result, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "randomPassword" {
				return true, "very-secret", nil
			} else {
				return false, "", nil
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestEvaluatorReportingUnknownCommand(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(unknownCmd)"
		  } 
		}
	  }`

	evaluatorCalled := false
	result, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			evaluatorCalled = true
			return false, "", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, input, result) // No substitution, input preserved
	require.True(t, evaluatorCalled)
}

func TestEvaluatorError(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$(causesError 1 alpha)"
		  } 
		}
	  }`

	evaluatorErr := fmt.Errorf("Something bad happened")
	_, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			return false, "", evaluatorErr
		},
	})

	require.Error(t, err)
	require.Equal(t, evaluatorErr, err)
}

func TestParameterExtraction(t *testing.T) {
	const input = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "$( randomPassword alpha  bravo 17 foobar-1)"
		  } 
		}
	  }`

	const expected = `{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
		  "name": {
			"value": "${AZURE_ENV_NAME}"
		  },
		  "password": {
			"value": "randomPassword called with [alpha bravo 17 foobar-1]"
		  } 
		}
	  }`

	result, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "randomPassword" {
				return true, fmt.Sprintf("%s called with %v", name, args), nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestMultipleSubstitutions(t *testing.T) {
	const input = `$(say alpha maybe) bravo $(say charlie)
	$(say delta) echo
	$(say foxtrot) golf $(say hotel for sure)`

	const expected = `alpha bravo charlie
	delta echo
	foxtrot golf hotel`

	result, err := Eval(context.Background(), input, testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "say" {
				return true, args[0], nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	})

	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestFullSubstitution(t *testing.T) {
	cmd := testCommandExecutor{
		runImpl: func(name string, args []string) (bool, string, error) {
			if name == "say" {
				return true, args[0], nil
			} else {
				return false, "", fmt.Errorf("Unknown command '%s', should not happen", name)
			}
		},
	}

	// The whole input needs to be substituted
	var input = `$(say alpha)`
	var expected = `alpha`
	result, err := Eval(context.Background(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)

	// Special case that is important for us
	input = `"$(say alpha)"`
	expected = `"alpha"`
	result, err = Eval(context.Background(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)

	// Once more, with whitespace, which should be preserved
	input = ` " $(say alpha)"  `
	expected = ` " alpha"  `
	result, err = Eval(context.Background(), input, cmd)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestCommandContainsInvocation(t *testing.T) {
	// Empty doc
	var input = ""
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// No commands
	input = "alpha bravo charlie"
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// Empty command (invalid invocation)
	input = "alpha bravo $() charlie"
	require.False(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation, but different command
	input = "alpha $(otherCmd) charlie"
	require.False(t, ContainsCommandInvocation(input, "Cmd"))

	// Invocation at the beginning
	input = "$(cmd)alpha bravo charlie"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation at the end
	input = "alpha bravo charlie$(cmd)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Invocation in the middle
	input = "alpha bravo$(cmd) charlie"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Multiple invocations
	input = "alpha $(cmd) bravo charlie$(cmd)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))

	// Parameters with dash characters
	input = "$(cmd foo-1 foo-2)"
	require.True(t, ContainsCommandInvocation(input, "cmd"))
}
