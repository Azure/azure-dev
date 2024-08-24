package project

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapToStringSlice(t *testing.T) {
	// Test case 1: Empty map
	m1 := make(map[string]string)
	expected1 := []string(nil)
	result1 := mapToStringSlice(m1, ":")
	assert.ElementsMatch(t, expected1, result1)

	// Test case 2: Map with values
	m2 := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	expected2 := []string{"key1:value1", "key2:value2", "key3:value3"}
	result2 := mapToStringSlice(m2, ":")
	assert.ElementsMatch(t, expected2, result2)

	// Test case 3: Map with empty values
	m3 := map[string]string{
		"key1": "",
		"key2": "",
		"key3": "",
	}
	expected3 := []string{"key1", "key2", "key3"}
	result3 := mapToStringSlice(m3, ":")
	assert.ElementsMatch(t, expected3, result3)
}

func TestEvaluateArgsWithConfig(t *testing.T) {
	envParamName := "param4"
	envParamKey := strings.TrimSuffix(scaffold.EnvFormat(envParamName)[2:], "}")
	envParamExpected := "valueFromEnv"
	t.Setenv(envParamKey, envParamExpected)

	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
			"param3": {
				Type:  "parameter.v0",
				Value: "{param3.inputs.iParam}",
				Inputs: map[string]apphost.Input{
					"iParam": {
						Type: "string",
					},
				},
			},
			envParamName: {
				Type:  "parameter.v0",
				Value: fmt.Sprintf("{%s.inputs.foo}", envParamName),
				Inputs: map[string]apphost.Input{
					"foo": {
						Type: "string",
					},
				},
			},
		},
	}

	args := map[string]string{
		"arg1": "{param1.value}",
		"arg2": "{param2.value}",
		"arg3": "constant",
		"arg4": "{param3.value}",
		"arg5": "{param4.value}",
	}

	expected := map[string]string{
		// evaluation completed
		"arg1": "value1",
		"arg2": "value2",
		// constant value
		"arg3": "constant",
		// evaluation delayed until building container
		"arg4": "{infra.parameters.param3}",
		// evaluation from environment variable
		"arg5": envParamExpected,
	}

	result, err := evaluateArgsWithConfig(manifest, args)
	require.NoError(t, err)
	require.ElementsMatch(t, mapToStringSlice(expected, ","), mapToStringSlice(result, ","))
}

func TestBuildArgsArrayAndEnv(t *testing.T) {
	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
		},
	}

	bArgs := map[string]apphost.ContainerV1BuildSecrets{
		"arg1": {
			Type:  "env",
			Value: to.Ptr("{param1.value}"),
		},
		"arg2": {
			Type:   "file",
			Source: to.Ptr("/path/to/secret"),
		},
	}

	expectedArgs := []string{
		"id=arg1",
		"id=arg2,src=/path/to/secret",
	}

	expectedEnv := []string{
		"arg1=value1",
	}

	args, env, err := buildArgsArrayAndEnv(manifest, bArgs)
	require.NoError(t, err)
	assert.ElementsMatch(t, expectedArgs, args)
	assert.ElementsMatch(t, expectedEnv, env)
}
