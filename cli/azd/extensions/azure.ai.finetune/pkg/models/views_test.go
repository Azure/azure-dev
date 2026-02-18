// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFineTuningJob_ToTableView(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		job      *FineTuningJob
		expected *FineTuningJobTableView
	}{
		{
			name: "CompleteJob",
			job: &FineTuningJob{
				ID:        "job-123",
				BaseModel: "gpt-4o-mini",
				Status:    StatusSucceeded,
				CreatedAt: now,
			},
			expected: &FineTuningJobTableView{
				ID:        "job-123",
				Status:    StatusSucceeded,
				BaseModel: "gpt-4o-mini",
				CreatedAt: now,
			},
		},
		{
			name: "PendingJob",
			job: &FineTuningJob{
				ID:        "job-456",
				BaseModel: "gpt-4",
				Status:    StatusPending,
				CreatedAt: now,
			},
			expected: &FineTuningJobTableView{
				ID:        "job-456",
				Status:    StatusPending,
				BaseModel: "gpt-4",
				CreatedAt: now,
			},
		},
		{
			name: "FailedJob",
			job: &FineTuningJob{
				ID:        "job-789",
				BaseModel: "gpt-3.5-turbo",
				Status:    StatusFailed,
				CreatedAt: now,
			},
			expected: &FineTuningJobTableView{
				ID:        "job-789",
				Status:    StatusFailed,
				BaseModel: "gpt-3.5-turbo",
				CreatedAt: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.job.ToTableView()
			require.Equal(t, tt.expected.ID, result.ID)
			require.Equal(t, tt.expected.Status, result.Status)
			require.Equal(t, tt.expected.BaseModel, result.BaseModel)
			require.Equal(t, tt.expected.CreatedAt, result.CreatedAt)
		})
	}
}

func TestFineTuningJobDetail_ToDetailViews(t *testing.T) {
	now := time.Now().UTC()
	finishedAt := now.Add(2 * time.Hour)
	estimatedFinish := now.Add(1 * time.Hour)

	tests := []struct {
		name           string
		jobDetail      *FineTuningJobDetail
		expectedID     string
		expectedModel  string
		expectedMethod string
	}{
		{
			name: "SupervisedMethod",
			jobDetail: &FineTuningJobDetail{
				ID:             "job-123",
				Status:         StatusSucceeded,
				Model:          "gpt-4o-mini",
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
				CreatedAt:      now,
				FinishedAt:     &finishedAt,
				Method:         "supervised",
				TrainingFile:   "file-train123",
				ValidationFile: "file-val456",
				Hyperparameters: &Hyperparameters{
					NEpochs:                3,
					BatchSize:              16,
					LearningRateMultiplier: 0.1,
				},
			},
			expectedID:     "job-123",
			expectedModel:  "gpt-4o-mini",
			expectedMethod: "supervised",
		},
		{
			name: "DPOMethod",
			jobDetail: &FineTuningJobDetail{
				ID:              "job-456",
				Status:          StatusRunning,
				Model:           "gpt-4",
				CreatedAt:       now,
				EstimatedFinish: &estimatedFinish,
				Method:          "dpo",
				TrainingFile:    "file-train789",
				Hyperparameters: &Hyperparameters{
					NEpochs:                5,
					BatchSize:              32,
					LearningRateMultiplier: 0.05,
					Beta:                   0.1,
				},
			},
			expectedID:     "job-456",
			expectedModel:  "gpt-4",
			expectedMethod: "dpo",
		},
		{
			name: "ReinforcementMethod",
			jobDetail: &FineTuningJobDetail{
				ID:           "job-789",
				Status:       StatusQueued,
				Model:        "gpt-4",
				CreatedAt:    now,
				Method:       "reinforcement",
				TrainingFile: "file-train101",
				Hyperparameters: &Hyperparameters{
					NEpochs:                10,
					BatchSize:              8,
					LearningRateMultiplier: 0.01,
					ComputeMultiplier:      2.0,
					EvalInterval:           100,
					EvalSamples:            50,
					ReasoningEffort:        "medium",
				},
			},
			expectedID:     "job-789",
			expectedModel:  "gpt-4",
			expectedMethod: "reinforcement",
		},
		{
			name: "NilHyperparameters",
			jobDetail: &FineTuningJobDetail{
				ID:              "job-nil",
				Status:          StatusPending,
				Model:           "gpt-4o-mini",
				CreatedAt:       now,
				Method:          "supervised",
				TrainingFile:    "file-train",
				Hyperparameters: nil,
			},
			expectedID:     "job-nil",
			expectedModel:  "gpt-4o-mini",
			expectedMethod: "supervised",
		},
		{
			name: "EmptyFineTunedModel",
			jobDetail: &FineTuningJobDetail{
				ID:             "job-empty",
				Status:         StatusRunning,
				Model:          "gpt-4o-mini",
				FineTunedModel: "",
				CreatedAt:      now,
				Method:         "supervised",
				TrainingFile:   "file-train",
				Hyperparameters: &Hyperparameters{
					NEpochs:   3,
					BatchSize: 16,
				},
			},
			expectedID:     "job-empty",
			expectedModel:  "gpt-4o-mini",
			expectedMethod: "supervised",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			views := tt.jobDetail.ToDetailViews()

			require.NotNil(t, views)
			require.NotNil(t, views.Details)
			require.NotNil(t, views.Timestamps)
			require.NotNil(t, views.Configuration)
			require.NotNil(t, views.Data)

			// Check details view
			require.Equal(t, tt.expectedID, views.Details.ID)
			require.Equal(t, tt.expectedModel, views.Details.Model)

			// Check data view
			require.Equal(t, tt.jobDetail.TrainingFile, views.Data.TrainingFile)
		})
	}
}

func TestFineTuningJobDetail_ToDetailViews_ConfigurationTypes(t *testing.T) {
	now := time.Now().UTC()

	t.Run("SupervisedConfiguration", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:           "job-sup",
			Status:       StatusSucceeded,
			Model:        "gpt-4o-mini",
			CreatedAt:    now,
			Method:       "supervised",
			TrainingFile: "file-train",
			Hyperparameters: &Hyperparameters{
				NEpochs:                3,
				BatchSize:              16,
				LearningRateMultiplier: 0.1,
			},
		}

		views := jobDetail.ToDetailViews()
		config, ok := views.Configuration.(*BaseConfigurationView)
		require.True(t, ok, "Expected BaseConfigurationView for supervised method")
		require.Equal(t, "supervised", config.TrainingType)
		require.Equal(t, int64(3), config.Epochs)
		require.Equal(t, int64(16), config.BatchSize)
	})

	t.Run("DPOConfiguration", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:           "job-dpo",
			Status:       StatusSucceeded,
			Model:        "gpt-4o-mini",
			CreatedAt:    now,
			Method:       "dpo",
			TrainingFile: "file-train",
			Hyperparameters: &Hyperparameters{
				NEpochs:                5,
				BatchSize:              32,
				LearningRateMultiplier: 0.05,
				Beta:                   0.1,
			},
		}

		views := jobDetail.ToDetailViews()
		config, ok := views.Configuration.(*DPOConfigurationView)
		require.True(t, ok, "Expected DPOConfigurationView for dpo method")
		require.Equal(t, "dpo", config.TrainingType)
		require.Equal(t, int64(5), config.Epochs)
		require.Equal(t, int64(32), config.BatchSize)
		require.Equal(t, "0.1", config.Beta)
	})

	t.Run("ReinforcementConfiguration", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:           "job-rl",
			Status:       StatusSucceeded,
			Model:        "gpt-4o-mini",
			CreatedAt:    now,
			Method:       "reinforcement",
			TrainingFile: "file-train",
			Hyperparameters: &Hyperparameters{
				NEpochs:                10,
				BatchSize:              8,
				LearningRateMultiplier: 0.01,
				ComputeMultiplier:      2.0,
				EvalInterval:           100,
				EvalSamples:            50,
				ReasoningEffort:        "high",
			},
		}

		views := jobDetail.ToDetailViews()
		config, ok := views.Configuration.(*ReinforcementConfigurationView)
		require.True(t, ok, "Expected ReinforcementConfigurationView for reinforcement method")
		require.Equal(t, "reinforcement", config.TrainingType)
		require.Equal(t, int64(10), config.Epochs)
		require.Equal(t, int64(8), config.BatchSize)
		require.Equal(t, "2", config.ComputeMultiplier)
		require.Equal(t, "100", config.EvalInterval)
		require.Equal(t, "50", config.EvalSamples)
		require.Equal(t, "high", config.ReasoningEffort)
	})

	t.Run("UnknownMethodUsesBaseConfiguration", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:           "job-unknown",
			Status:       StatusSucceeded,
			Model:        "gpt-4o-mini",
			CreatedAt:    now,
			Method:       "unknown_method",
			TrainingFile: "file-train",
			Hyperparameters: &Hyperparameters{
				NEpochs:   3,
				BatchSize: 16,
			},
		}

		views := jobDetail.ToDetailViews()
		_, ok := views.Configuration.(*BaseConfigurationView)
		require.True(t, ok, "Expected BaseConfigurationView for unknown method")
	})
}

func TestFormatHelpers(t *testing.T) {
	t.Run("formatFloatOrDash", func(t *testing.T) {
		require.Equal(t, "-", formatFloatOrDash(0))
		require.Equal(t, "0.1", formatFloatOrDash(0.1))
		require.Equal(t, "2.5", formatFloatOrDash(2.5))
		require.Equal(t, "100", formatFloatOrDash(100.0))
	})

	t.Run("formatInt64OrDash", func(t *testing.T) {
		require.Equal(t, "-", formatInt64OrDash(0))
		require.Equal(t, "1", formatInt64OrDash(1))
		require.Equal(t, "100", formatInt64OrDash(100))
		require.Equal(t, "999999", formatInt64OrDash(999999))
	})

	t.Run("stringOrDash", func(t *testing.T) {
		require.Equal(t, "-", stringOrDash(""))
		require.Equal(t, "test", stringOrDash("test"))
		require.Equal(t, "hello world", stringOrDash("hello world"))
	})

	t.Run("formatTimeOrDash", func(t *testing.T) {
		require.Equal(t, "-", formatTimeOrDash(time.Time{}))

		now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
		require.Equal(t, "2024-06-15 14:30", formatTimeOrDash(now))
	})

	t.Run("formatTimePointerOrDash", func(t *testing.T) {
		require.Equal(t, "-", formatTimePointerOrDash(nil))

		zeroTime := time.Time{}
		require.Equal(t, "-", formatTimePointerOrDash(&zeroTime))

		now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
		require.Equal(t, "2024-06-15 14:30", formatTimePointerOrDash(&now))
	})
}

func TestTimestampsView_Formatting(t *testing.T) {
	now := time.Now().UTC()
	finishedAt := now.Add(2 * time.Hour)

	jobDetail := &FineTuningJobDetail{
		ID:           "job-123",
		Status:       StatusSucceeded,
		Model:        "gpt-4o-mini",
		CreatedAt:    now,
		FinishedAt:   &finishedAt,
		Method:       "supervised",
		TrainingFile: "file-train",
		Hyperparameters: &Hyperparameters{
			NEpochs: 3,
		},
	}

	views := jobDetail.ToDetailViews()

	require.NotEmpty(t, views.Timestamps.Created)
	require.NotEmpty(t, views.Timestamps.Finished)
	require.Equal(t, "-", views.Timestamps.EstimatedETA)
}

func TestDataView_ValidationFile(t *testing.T) {
	now := time.Now().UTC()

	t.Run("WithValidationFile", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:              "job-123",
			Status:          StatusSucceeded,
			Model:           "gpt-4o-mini",
			CreatedAt:       now,
			Method:          "supervised",
			TrainingFile:    "file-train",
			ValidationFile:  "file-val",
			Hyperparameters: &Hyperparameters{NEpochs: 3},
		}

		views := jobDetail.ToDetailViews()
		require.Equal(t, "file-train", views.Data.TrainingFile)
		require.Equal(t, "file-val", views.Data.ValidationFile)
	})

	t.Run("WithoutValidationFile", func(t *testing.T) {
		jobDetail := &FineTuningJobDetail{
			ID:              "job-456",
			Status:          StatusSucceeded,
			Model:           "gpt-4o-mini",
			CreatedAt:       now,
			Method:          "supervised",
			TrainingFile:    "file-train",
			ValidationFile:  "",
			Hyperparameters: &Hyperparameters{NEpochs: 3},
		}

		views := jobDetail.ToDetailViews()
		require.Equal(t, "file-train", views.Data.TrainingFile)
		require.Equal(t, "-", views.Data.ValidationFile)
	})
}
