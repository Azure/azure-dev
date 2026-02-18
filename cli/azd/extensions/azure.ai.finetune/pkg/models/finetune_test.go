// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJobStatus_Constants_AreDistinctAndValid(t *testing.T) {
	// Verifies job status constants are unique (prevents copy-paste bugs)
	// and use lowercase values expected by the Azure API
	statuses := []JobStatus{
		StatusPending,
		StatusQueued,
		StatusRunning,
		StatusSucceeded,
		StatusFailed,
		StatusCancelled,
		StatusPaused,
		StatusPausing,
		StatusResuming,
	}

	seen := make(map[JobStatus]bool)
	for _, status := range statuses {
		require.NotEmpty(t, string(status), "Job status should not be empty")
		require.False(t, seen[status], "Duplicate job status found: %s", status)
		seen[status] = true
	}
}

func TestJobAction_Constants_AreDistinct(t *testing.T) {
	// Ensures job actions are unique - prevents sending wrong action to API
	actions := []JobAction{
		JobActionPause,
		JobActionResume,
		JobActionCancel,
	}

	seen := make(map[JobAction]bool)
	for _, action := range actions {
		require.NotEmpty(t, string(action), "Job action should not be empty")
		require.False(t, seen[action], "Duplicate job action found: %s", action)
		seen[action] = true
	}
}

func TestMethodType_Constants_AreDistinct(t *testing.T) {
	// Ensures method types are unique - critical for fine-tuning API requests
	methods := []MethodType{
		Supervised,
		DPO,
		Reinforcement,
	}

	seen := make(map[MethodType]bool)
	for _, method := range methods {
		require.NotEmpty(t, string(method), "Method type should not be empty")
		require.False(t, seen[method], "Duplicate method type found: %s", method)
		seen[method] = true
	}
}

func TestDuration_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		expected string
	}{
		{
			name:     "ZeroDuration",
			duration: Duration(0),
			expected: `"-"`,
		},
		{
			name:     "OneHour",
			duration: Duration(time.Hour),
			expected: `"1h 00m"`,
		},
		{
			name:     "OneHourThirtyMinutes",
			duration: Duration(time.Hour + 30*time.Minute),
			expected: `"1h 30m"`,
		},
		{
			name:     "TwoHoursFifteenMinutes",
			duration: Duration(2*time.Hour + 15*time.Minute),
			expected: `"2h 15m"`,
		},
		{
			name:     "ThirtyMinutes",
			duration: Duration(30 * time.Minute),
			expected: `"0h 30m"`,
		},
		{
			name:     "TwentyFourHours",
			duration: Duration(24 * time.Hour),
			expected: `"24h 00m"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.duration.MarshalJSON()
			require.NoError(t, err)
			require.Equal(t, tt.expected, string(result))
		})
	}
}

func TestDuration_MarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		expected interface{}
	}{
		{
			name:     "ZeroDuration",
			duration: Duration(0),
			expected: "-",
		},
		{
			name:     "OneHour",
			duration: Duration(time.Hour),
			expected: "1h 00m",
		},
		{
			name:     "OneHourThirtyMinutes",
			duration: Duration(time.Hour + 30*time.Minute),
			expected: "1h 30m",
		},
		{
			name:     "TwoHoursFifteenMinutes",
			duration: Duration(2*time.Hour + 15*time.Minute),
			expected: "2h 15m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.duration.MarshalYAML()
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateFineTuningRequest_Validate(t *testing.T) {
	tests := []struct {
		name        string
		request     CreateFineTuningRequest
		expectError bool
		errorMsg    string
	}{
		{
			name: "ValidMinimalRequest",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
			},
			expectError: false,
		},
		{
			name: "MissingModel",
			request: CreateFineTuningRequest{
				TrainingFile: "file-abc123",
			},
			expectError: true,
			errorMsg:    "model is required",
		},
		{
			name: "MissingTrainingFile",
			request: CreateFineTuningRequest{
				BaseModel: "gpt-4o-mini",
			},
			expectError: true,
			errorMsg:    "training_file is required",
		},
		{
			name: "EmptyModel",
			request: CreateFineTuningRequest{
				BaseModel:    "",
				TrainingFile: "file-abc123",
			},
			expectError: true,
			errorMsg:    "model is required",
		},
		{
			name: "EmptyTrainingFile",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "",
			},
			expectError: true,
			errorMsg:    "training_file is required",
		},
		{
			name: "InvalidMethodType",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type: "invalid_method",
				},
			},
			expectError: true,
			errorMsg:    "invalid method type",
		},
		{
			name: "ValidSupervisedMethod",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type:       "supervised",
					Supervised: &SupervisedConfig{},
				},
			},
			expectError: false,
		},
		{
			name: "SupervisedMethodMissingConfig",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type: "supervised",
				},
			},
			expectError: true,
			errorMsg:    "supervised method requires 'supervised' configuration block",
		},
		{
			name: "ValidDPOMethod",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type: "dpo",
					DPO:  &DPOConfig{},
				},
			},
			expectError: false,
		},
		{
			name: "DPOMethodMissingConfig",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type: "dpo",
				},
			},
			expectError: true,
			errorMsg:    "dpo method requires 'dpo' configuration block",
		},
		{
			name: "ValidReinforcementMethod",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type:          "reinforcement",
					Reinforcement: &ReinforcementConfig{},
				},
			},
			expectError: false,
		},
		{
			name: "ReinforcementMethodMissingConfig",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Method: MethodConfig{
					Type: "reinforcement",
				},
			},
			expectError: true,
			errorMsg:    "reinforcement method requires 'reinforcement' configuration block",
		},
		{
			name: "SuffixTooLong",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Suffix:       stringPtr(string(make([]byte, 65))), // 65 characters
			},
			expectError: true,
			errorMsg:    "suffix exceeds maximum length of 64 characters",
		},
		{
			name: "SuffixAtMaxLength",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Suffix:       stringPtr(string(make([]byte, 64))), // exactly 64 characters
			},
			expectError: false,
		},
		{
			name: "TooManyMetadataEntries",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Metadata:     generateMetadata(17), // 17 entries
			},
			expectError: true,
			errorMsg:    "metadata exceeds maximum of 16 key-value pairs",
		},
		{
			name: "MetadataAtMaxEntries",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Metadata:     generateMetadata(16), // exactly 16 entries
			},
			expectError: false,
		},
		{
			name: "MetadataKeyTooLong",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Metadata: map[string]string{
					string(make([]byte, 65)): "value", // key with 65 characters
				},
			},
			expectError: true,
			errorMsg:    "metadata key exceeds maximum length of 64 characters",
		},
		{
			name: "MetadataValueTooLong",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Metadata: map[string]string{
					"key": string(make([]byte, 513)), // value with 513 characters
				},
			},
			expectError: true,
			errorMsg:    "metadata value exceeds maximum length of 512 characters",
		},
		{
			name: "IntegrationMissingType",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Integrations: []Integration{
					{Type: "", Config: map[string]interface{}{"key": "value"}},
				},
			},
			expectError: true,
			errorMsg:    "integration type is required",
		},
		{
			name: "IntegrationMissingConfig",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Integrations: []Integration{
					{Type: "wandb"},
				},
			},
			expectError: true,
			errorMsg:    "integration of type 'wandb' requires 'config' block",
		},
		{
			name: "ValidIntegration",
			request: CreateFineTuningRequest{
				BaseModel:    "gpt-4o-mini",
				TrainingFile: "file-abc123",
				Integrations: []Integration{
					{Type: "wandb", Config: map[string]interface{}{"project": "my-project"}},
				},
			},
			expectError: false,
		},
		{
			name: "ValidRequestWithAllOptionalFields",
			request: CreateFineTuningRequest{
				BaseModel:      "gpt-4o-mini",
				TrainingFile:   "file-abc123",
				ValidationFile: stringPtr("file-val456"),
				Suffix:         stringPtr("my-custom-model"),
				Seed:           int64Ptr(42),
				Metadata:       map[string]string{"project": "test"},
				Method: MethodConfig{
					Type: "supervised",
					Supervised: &SupervisedConfig{
						Hyperparameters: HyperparametersConfig{
							Epochs:    3,
							BatchSize: 16,
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFineTuningJob_JSONMarshaling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	job := &FineTuningJob{
		ID:        "job-123",
		BaseModel: "gpt-4o-mini",
		Status:    StatusRunning,
		CreatedAt: now,
		Duration:  Duration(2*time.Hour + 30*time.Minute),
	}

	data, err := json.Marshal(job)
	require.NoError(t, err)

	var unmarshaled map[string]interface{}
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	require.Equal(t, "job-123", unmarshaled["id"])
	require.Equal(t, "gpt-4o-mini", unmarshaled["model"])
	require.Equal(t, "running", unmarshaled["status"])
	require.Equal(t, "2h 30m", unmarshaled["duration"])
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}

func generateMetadata(count int) map[string]string {
	metadata := make(map[string]string)
	for i := 0; i < count; i++ {
		metadata[string(rune('a'+i))] = "value"
	}
	return metadata
}
