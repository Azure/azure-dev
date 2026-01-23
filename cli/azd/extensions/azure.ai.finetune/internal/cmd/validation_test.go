// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRequiredFlags_AllProvided(t *testing.T) {
	flags := map[string]string{
		"id":              "job-123",
		"deployment-name": "my-deployment",
	}

	err := validateRequiredFlags(flags)

	require.NoError(t, err)
}

func TestValidateRequiredFlags_SingleMissing(t *testing.T) {
	flags := map[string]string{
		"id": "",
	}

	err := validateRequiredFlags(flags)

	require.Error(t, err)
	require.Contains(t, err.Error(), "--id is required")
}

func TestValidateRequiredFlags_MultipleMissing(t *testing.T) {
	flags := map[string]string{
		"job-id":          "",
		"deployment-name": "",
	}

	err := validateRequiredFlags(flags)

	require.Error(t, err)
	// Should contain both flags in sorted order
	require.Contains(t, err.Error(), "--deployment-name")
	require.Contains(t, err.Error(), "--job-id")
	require.Contains(t, err.Error(), "are required")
}

func TestValidateRequiredFlags_EmptyMap(t *testing.T) {
	flags := map[string]string{}

	err := validateRequiredFlags(flags)

	require.NoError(t, err)
}

func TestValidateRequiredFlags_MixedProvided(t *testing.T) {
	flags := map[string]string{
		"id":              "job-123",
		"deployment-name": "",
	}

	err := validateRequiredFlags(flags)

	require.Error(t, err)
	require.Contains(t, err.Error(), "--deployment-name is required")
}

func TestValidateRequiredFlags_HintForJobID(t *testing.T) {
	tests := []struct {
		name     string
		flagName string
	}{
		{
			name:     "id flag shows hint",
			flagName: "id",
		},
		{
			name:     "job-id flag shows hint",
			flagName: "job-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := map[string]string{
				tt.flagName: "",
			}

			err := validateRequiredFlags(flags)

			require.Error(t, err)
			require.Contains(t, err.Error(), HintFindJobID)
		})
	}
}

func TestValidateRequiredFlags_HintForDeploymentName(t *testing.T) {
	flags := map[string]string{
		"deployment-name": "",
	}

	err := validateRequiredFlags(flags)

	require.Error(t, err)
	require.Contains(t, err.Error(), HintDeploymentName)
}

func TestValidateRequiredFlags_NoHintForUnknownFlag(t *testing.T) {
	flags := map[string]string{
		"unknown-flag": "",
	}

	err := validateRequiredFlags(flags)

	require.Error(t, err)
	require.Contains(t, err.Error(), "--unknown-flag is required")
	// Should not contain any hints
	require.NotContains(t, err.Error(), "Usage:")
}

func TestValidateSubmitFlags_WithConfigFile(t *testing.T) {
	err := validateSubmitFlags("config.yaml", "", "")

	require.NoError(t, err)
}

func TestValidateSubmitFlags_WithModelAndTrainingFile(t *testing.T) {
	err := validateSubmitFlags("", "gpt-4o-mini", "file-abc123")

	require.NoError(t, err)
}

func TestValidateSubmitFlags_NeitherProvided(t *testing.T) {
	err := validateSubmitFlags("", "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "either --file or --model with --training-file is required")
	require.Contains(t, err.Error(), HintSubmitJobUsage)
}

func TestValidateSubmitFlags_OnlyModelProvided(t *testing.T) {
	err := validateSubmitFlags("", "gpt-4o-mini", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "--training-file is required when --model is provided")
}

func TestValidateSubmitFlags_OnlyTrainingFileProvided(t *testing.T) {
	err := validateSubmitFlags("", "", "file-abc123")

	require.Error(t, err)
	require.Contains(t, err.Error(), "--model is required when --training-file is provided")
}

func TestValidateSubmitFlags_ConfigFileOverridesModelAndTrainingFile(t *testing.T) {
	// When config file is provided, model and training-file are ignored
	// (config file takes precedence)
	err := validateSubmitFlags("config.yaml", "gpt-4", "file-xyz")

	require.NoError(t, err)
}

func TestHintConstants_AreNotEmpty(t *testing.T) {
	hints := []struct {
		name  string
		value string
	}{
		{"HintFindJobID", HintFindJobID},
		{"HintDeploymentName", HintDeploymentName},
		{"HintSubmitJobUsage", HintSubmitJobUsage},
	}

	for _, hint := range hints {
		t.Run(hint.name, func(t *testing.T) {
			require.NotEmpty(t, hint.value, "%s should not be empty", hint.name)
			require.NotEmpty(t, strings.TrimSpace(hint.value), "%s should not be only whitespace", hint.name)
		})
	}
}
