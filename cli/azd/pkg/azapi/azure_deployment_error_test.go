// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Parse_Azure_ARM_Deploy_Error_01(t *testing.T) {
	assertOutputsMatch(t, "testdata/arm_sample_error_01.json", "testdata/arm_sample_error_01.txt")
}

func Test_Parse_Azure_ARM_Deploy_Error_02(t *testing.T) {
	assertOutputsMatch(t, "testdata/arm_sample_error_02.json", "testdata/arm_sample_error_02.txt")
}

func Test_Parse_Azure_ARM_Deploy_Error_03(t *testing.T) {
	assertOutputsMatch(t, "testdata/arm_sample_error_03.json", "testdata/arm_sample_error_03.txt")
}

func Test_Parse_Azure_ARM_Deploy_Error_04(t *testing.T) {
	assertOutputsMatch(t, "testdata/arm_sample_error_04.json", "testdata/arm_sample_error_04.txt")
}

func Test_Not_Json_Error(t *testing.T) {
	nonJsonError := "I'm just a regular error message"
	deploymentError := AzureDeploymentError{Json: nonJsonError}
	errorString := deploymentError.Error()

	require.Equal(t, nonJsonError, errorString)
}

func assertOutputsMatch(t *testing.T, jsonPath string, expectedOutputPath string) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		require.Error(t, err)
	}

	expected, err := os.ReadFile(expectedOutputPath)
	if err != nil {
		require.Error(t, err)
	}

	errorJson := string(data)
	deploymentError := NewAzureDeploymentError(errorJson)
	errorString := deploymentError.Error()

	actualLines := strings.Split(errorString, "\n")
	expectedLines := strings.Split(string(expected), "\n")

	require.Equal(t, len(expectedLines), len(actualLines))

	for index, value := range actualLines {
		require.Equal(t, expectedLines[index], value)
	}
}
