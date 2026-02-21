// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"errors"
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
	deploymentError := AzureDeploymentError{Title: "Title", Json: nonJsonError}
	errorString := deploymentError.Error()

	require.Equal(t, "\n\nTitle:\n"+nonJsonError, errorString)
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
	deploymentError := NewAzureDeploymentError("Title", errorJson, DeploymentOperationDeploy)
	errorString := deploymentError.Error()

	actualLines := strings.Split(errorString, "\n")
	expectedLines := strings.Split(string(expected), "\n")

	titleLines := actualLines[:3]
	actualLines = actualLines[3:]

	require.Empty(t, titleLines[0])
	require.Empty(t, titleLines[1])
	require.Equal(t, "Title:", titleLines[2])

	require.Equal(t, len(expectedLines), len(actualLines))

	for index, value := range actualLines {
		require.Equal(t, expectedLines[index], value)
	}
}

func Test_DeploymentErrorLine_Error(t *testing.T) {
	line := &DeploymentErrorLine{Code: "InsufficientQuota", Message: "Not enough quota"}
	require.Equal(t, "InsufficientQuota: Not enough quota", line.Error())

	lineCodeOnly := &DeploymentErrorLine{Code: "InsufficientQuota"}
	require.Equal(t, "InsufficientQuota", lineCodeOnly.Error())

	lineMessageOnly := &DeploymentErrorLine{Message: "Something failed"}
	require.Equal(t, "Something failed", lineMessageOnly.Error())

	lineEmpty := &DeploymentErrorLine{}
	require.Equal(t, "deployment error", lineEmpty.Error())
}

func Test_DeploymentErrorLine_Unwrap(t *testing.T) {
	inner1 := &DeploymentErrorLine{Code: "InsufficientQuota", Message: "Not enough"}
	inner2 := &DeploymentErrorLine{Code: "SkuNotAvailable", Message: "SKU unavailable"}
	parent := &DeploymentErrorLine{
		Code:  "ResourceDeploymentFailure",
		Inner: []*DeploymentErrorLine{inner1, inner2},
	}

	errs := parent.Unwrap()
	require.Len(t, errs, 2)
	require.Equal(t, inner1, errs[0])
	require.Equal(t, inner2, errs[1])
}

func Test_DeploymentErrorLine_Unwrap_Empty(t *testing.T) {
	line := &DeploymentErrorLine{Code: "Leaf"}
	require.Nil(t, line.Unwrap())
}

func Test_AzureDeploymentError_Unwrap(t *testing.T) {
	jsonErr := `{"error":{"code":"DeploymentFailed",` +
		`"details":[{"code":"InsufficientQuota","message":"quota exceeded"}]}}`
	deployErr := NewAzureDeploymentError(
		"test", jsonErr, DeploymentOperationDeploy)

	errs := deployErr.Unwrap()
	require.NotEmpty(t, errs)
	require.NotNil(t, deployErr.Details)
}

func Test_AzureDeploymentError_ErrorsAs_DeploymentErrorLine(t *testing.T) {
	jsonErr := `{"error":{"code":"DeploymentFailed",` +
		`"details":[{"code":"ResourceDeploymentFailure",` +
		`"details":[{"code":"FlagMustBeSetForRestore",` +
		`"message":"soft deleted resource"}]}]}}`
	deployErr := NewAzureDeploymentError(
		"test", jsonErr, DeploymentOperationDeploy)

	// errors.As should find DeploymentErrorLine anywhere in the tree
	var line *DeploymentErrorLine
	require.True(t, errors.As(deployErr, &line),
		"errors.As should find DeploymentErrorLine in tree")
}
