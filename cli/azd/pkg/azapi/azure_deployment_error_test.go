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

func Test_RootCause_DeepNested(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code: "",
			Inner: []*DeploymentErrorLine{
				{
					Code: "DeploymentFailed",
					Inner: []*DeploymentErrorLine{
						{
							Code: "ResourceDeploymentFailure",
							Inner: []*DeploymentErrorLine{
								{Code: "InsufficientQuota", Message: "Not enough quota"},
							},
						},
					},
				},
			},
		},
	}

	root := err.RootCause()
	require.NotNil(t, root)
	require.Equal(t, "InsufficientQuota", root.Code)
}

func Test_RootCause_FlatError(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code:    "InvalidTemplate",
			Message: "Template is invalid",
		},
	}

	root := err.RootCause()
	require.NotNil(t, root)
	require.Equal(t, "InvalidTemplate", root.Code)
}

func Test_RootCause_NilDetails(t *testing.T) {
	err := &AzureDeploymentError{Details: nil}
	root := err.RootCause()
	require.Nil(t, root)
}

func Test_RootCause_NoCode(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code: "",
			Inner: []*DeploymentErrorLine{
				{Code: "", Message: "some message"},
			},
		},
	}

	root := err.RootCause()
	require.Nil(t, root)
}

func Test_RootCauseHint_Known(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code: "",
			Inner: []*DeploymentErrorLine{
				{Code: "InsufficientQuota", Message: "Quota exceeded"},
			},
		},
	}

	hint := err.RootCauseHint()
	require.Contains(t, hint, "insufficient quota")
}

func Test_RootCauseHint_Unknown(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code: "SomeRandomCode",
		},
	}

	hint := err.RootCauseHint()
	require.Empty(t, hint)
}

func Test_RootCause_MultipleBranches(t *testing.T) {
	err := &AzureDeploymentError{
		Details: &DeploymentErrorLine{
			Code: "",
			Inner: []*DeploymentErrorLine{
				{
					Code: "Conflict",
					Inner: []*DeploymentErrorLine{
						{Code: "AuthorizationFailed"},
					},
				},
				{
					Code: "ValidationError",
				},
			},
		},
	}

	root := err.RootCause()
	require.NotNil(t, root)
	// AuthorizationFailed is at depth 2, ValidationError at depth 1
	// The depth-tracking algorithm should pick the deeper one
	require.Equal(t, "AuthorizationFailed", root.Code)
}
