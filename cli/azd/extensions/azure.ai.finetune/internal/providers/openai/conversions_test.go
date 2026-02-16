// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"testing"

	"azure.ai.finetune/pkg/models"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestMapOpenAIStatusToJobStatus(t *testing.T) {
	tests := []struct {
		name          string
		openaiStatus  openai.FineTuningJobStatus
		expectedModel models.JobStatus
	}{
		{
			name:          "ValidatingFiles_MapsToRunning",
			openaiStatus:  OpenAIStatusValidatingFiles,
			expectedModel: models.StatusRunning,
		},
		{
			name:          "Running_MapsToRunning",
			openaiStatus:  OpenAIStatusRunning,
			expectedModel: models.StatusRunning,
		},
		{
			name:          "Queued_MapsToQueued",
			openaiStatus:  OpenAIStatusQueued,
			expectedModel: models.StatusQueued,
		},
		{
			name:          "Succeeded_MapsToSucceeded",
			openaiStatus:  OpenAIStatusSucceeded,
			expectedModel: models.StatusSucceeded,
		},
		{
			name:          "Failed_MapsToFailed",
			openaiStatus:  OpenAIStatusFailed,
			expectedModel: models.StatusFailed,
		},
		{
			name:          "Cancelled_MapsToCancelled",
			openaiStatus:  OpenAIStatusCancelled,
			expectedModel: models.StatusCancelled,
		},
		{
			name:          "Pausing_MapsToPausing",
			openaiStatus:  OpenAIStatusPausing,
			expectedModel: models.StatusPausing,
		},
		{
			name:          "Resuming_MapsToResuming",
			openaiStatus:  OpenAIStatusResuming,
			expectedModel: models.StatusResuming,
		},
		{
			name:          "UnknownStatus_MapsToPending",
			openaiStatus:  openai.FineTuningJobStatus("unknown_status"),
			expectedModel: models.StatusPending,
		},
		{
			name:          "EmptyStatus_MapsToPending",
			openaiStatus:  openai.FineTuningJobStatus(""),
			expectedModel: models.StatusPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapOpenAIStatusToJobStatus(tt.openaiStatus)
			require.Equal(t, tt.expectedModel, result)
		})
	}
}

func TestOpenAIStatusConstants_AreDistinct(t *testing.T) {
	// Ensures OpenAI status constants are unique - prevents copy-paste bugs
	statuses := []string{
		OpenAIStatusValidatingFiles,
		OpenAIStatusQueued,
		OpenAIStatusRunning,
		OpenAIStatusSucceeded,
		OpenAIStatusFailed,
		OpenAIStatusCancelled,
		OpenAIStatusPausing,
		OpenAIStatusResuming,
	}

	seen := make(map[string]bool)
	for _, status := range statuses {
		require.NotEmpty(t, status, "OpenAI status constant should not be empty")
		require.False(t, seen[status], "Duplicate OpenAI status constant: %s", status)
		seen[status] = true
	}
}

func TestConvertOpenAIJobToModel(t *testing.T) {
	t.Run("BasicJob", func(t *testing.T) {
		openaiJob := openai.FineTuningJob{
			ID:             "ftjob-abc123",
			Status:         OpenAIStatusRunning,
			Model:          "gpt-4o-mini",
			FineTunedModel: "",
			CreatedAt:      1704067200, // 2024-01-01 00:00:00 UTC
			FinishedAt:     0,
		}

		result := convertOpenAIJobToModel(openaiJob)

		require.NotNil(t, result)
		require.Equal(t, "ftjob-abc123", result.ID)
		require.Equal(t, models.StatusRunning, result.Status)
		require.Equal(t, "gpt-4o-mini", result.BaseModel)
		require.Empty(t, result.FineTunedModel)
	})

	t.Run("CompletedJob", func(t *testing.T) {
		openaiJob := openai.FineTuningJob{
			ID:             "ftjob-xyz789",
			Status:         OpenAIStatusSucceeded,
			Model:          "gpt-4o-mini",
			FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			CreatedAt:      1704067200,        // 2024-01-01 00:00:00 UTC
			FinishedAt:     1704067200 + 3600, // +1 hour
		}

		result := convertOpenAIJobToModel(openaiJob)

		require.NotNil(t, result)
		require.Equal(t, "ftjob-xyz789", result.ID)
		require.Equal(t, models.StatusSucceeded, result.Status)
		require.Equal(t, "ft:gpt-4o-mini:my-org::abc123", result.FineTunedModel)
	})

	t.Run("FailedJob", func(t *testing.T) {
		openaiJob := openai.FineTuningJob{
			ID:        "ftjob-failed",
			Status:    OpenAIStatusFailed,
			Model:     "gpt-4",
			CreatedAt: 1704067200,
		}

		result := convertOpenAIJobToModel(openaiJob)

		require.NotNil(t, result)
		require.Equal(t, models.StatusFailed, result.Status)
	})
}

func TestConvertHyperparameterToInt(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected *int64
	}{
		{
			name:     "IntValue",
			input:    3,
			expected: int64Ptr(3),
		},
		{
			name:     "Int64Value",
			input:    int64(5),
			expected: int64Ptr(5),
		},
		{
			name:     "Float64Value_Truncates",
			input:    float64(10.9),
			expected: int64Ptr(10),
		},
		{
			name:     "StringAuto_ReturnsNil",
			input:    "auto",
			expected: nil,
		},
		{
			name:     "NilValue",
			input:    nil,
			expected: nil,
		},
		{
			name:     "UnsupportedType_ReturnsNil",
			input:    true,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertHyperparameterToInt(tt.input)
			if tt.expected == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, *tt.expected, *result)
			}
		})
	}
}

func TestConvertHyperparameterToFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected *float64
	}{
		{
			name:     "IntValue_ConvertsToFloat",
			input:    3,
			expected: float64Ptr(3.0),
		},
		{
			name:     "Int64Value_ConvertsToFloat",
			input:    int64(5),
			expected: float64Ptr(5.0),
		},
		{
			name:     "Float64Value",
			input:    float64(0.1),
			expected: float64Ptr(0.1),
		},
		{
			name:     "StringAuto_ReturnsNil",
			input:    "auto",
			expected: nil,
		},
		{
			name:     "NilValue",
			input:    nil,
			expected: nil,
		},
		{
			name:     "UnsupportedType_ReturnsNil",
			input:    true,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertHyperparameterToFloat(tt.input)
			if tt.expected == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.InDelta(t, *tt.expected, *result, 0.0001)
			}
		})
	}
}

func TestGetReasoningEffortValue(t *testing.T) {
	tests := []struct {
		name     string
		effort   string
		expected openai.ReinforcementHyperparametersReasoningEffort
	}{
		{
			name:     "Low_Lowercase",
			effort:   "low",
			expected: openai.ReinforcementHyperparametersReasoningEffortLow,
		},
		{
			name:     "Low_Uppercase",
			effort:   "LOW",
			expected: openai.ReinforcementHyperparametersReasoningEffortLow,
		},
		{
			name:     "Medium_Lowercase",
			effort:   "medium",
			expected: openai.ReinforcementHyperparametersReasoningEffortMedium,
		},
		{
			name:     "Medium_MixedCase",
			effort:   "Medium",
			expected: openai.ReinforcementHyperparametersReasoningEffortMedium,
		},
		{
			name:     "High_Lowercase",
			effort:   "high",
			expected: openai.ReinforcementHyperparametersReasoningEffortHigh,
		},
		{
			name:     "Unknown_ReturnsDefault",
			effort:   "unknown",
			expected: openai.ReinforcementHyperparametersReasoningEffortDefault,
		},
		{
			name:     "Empty_ReturnsDefault",
			effort:   "",
			expected: openai.ReinforcementHyperparametersReasoningEffortDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getReasoningEffortValue(tt.effort)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertInternalJobParamToOpenAiJobParams_BasicRequest(t *testing.T) {
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
	}

	params, extraBody, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, openai.FineTuningJobNewParamsModel("gpt-4o-mini"), params.Model)
	require.Equal(t, "file-abc123", params.TrainingFile)
	require.Nil(t, extraBody)
}

func TestConvertInternalJobParamToOpenAiJobParams_WithValidationFile(t *testing.T) {
	validationFile := "file-val456"
	config := &models.CreateFineTuningRequest{
		BaseModel:      "gpt-4o-mini",
		TrainingFile:   "file-abc123",
		ValidationFile: &validationFile,
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.NotNil(t, params.ValidationFile)
}

func TestConvertInternalJobParamToOpenAiJobParams_WithOptionalFields(t *testing.T) {
	suffix := "my-custom"
	seed := int64(42)
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
		Suffix:       &suffix,
		Seed:         &seed,
		Metadata: map[string]string{
			"project": "test",
			"team":    "ai",
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.NotNil(t, params.Suffix)
	require.NotNil(t, params.Seed)
	require.NotNil(t, params.Metadata)
	require.Len(t, params.Metadata, 2)
}

func TestConvertInternalJobParamToOpenAiJobParams_SupervisedMethod(t *testing.T) {
	epochs := 3
	batchSize := 16
	lr := 0.1
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
		Method: models.MethodConfig{
			Type: "supervised",
			Supervised: &models.SupervisedConfig{
				Hyperparameters: models.HyperparametersConfig{
					Epochs:                 &epochs,
					BatchSize:              &batchSize,
					LearningRateMultiplier: &lr,
				},
			},
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, "supervised", params.Method.Type)
}

func TestConvertInternalJobParamToOpenAiJobParams_DPOMethod(t *testing.T) {
	epochs := 5
	beta := 0.1
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4",
		TrainingFile: "file-abc123",
		Method: models.MethodConfig{
			Type: "dpo",
			DPO: &models.DPOConfig{
				Hyperparameters: models.HyperparametersConfig{
					Epochs: &epochs,
					Beta:   &beta,
				},
			},
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, "dpo", params.Method.Type)
}

func TestConvertInternalJobParamToOpenAiJobParams_ReinforcementMethod(t *testing.T) {
	epochs := 10
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4",
		TrainingFile: "file-abc123",
		Method: models.MethodConfig{
			Type: "reinforcement",
			Reinforcement: &models.ReinforcementConfig{
				Hyperparameters: models.HyperparametersConfig{
					Epochs:          &epochs,
					ReasoningEffort: "medium",
				},
			},
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, "reinforcement", params.Method.Type)
}

func TestConvertInternalJobParamToOpenAiJobParams_WithIntegrations(t *testing.T) {
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
		Integrations: []models.Integration{
			{
				Type: "wandb",
				Config: map[string]interface{}{
					"project": "my-project",
					"entity":  "my-org",
				},
			},
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.NotEmpty(t, params.Integrations)
	require.Equal(t, "wandb", string(params.Integrations[0].Type))
}

func TestConvertInternalJobParamToOpenAiJobParams_WithExtraBody(t *testing.T) {
	config := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
		ExtraBody: map[string]interface{}{
			"custom_field": "custom_value",
		},
	}

	params, extraBody, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.NotNil(t, extraBody)
	require.Equal(t, "custom_value", extraBody["custom_field"])
}

func TestConvertGraderMapToSDKParam_StringCheck(t *testing.T) {
	graderMap := map[string]interface{}{
		"type":      "string_check",
		"name":      "exact",
		"input":     "{{sample.output_text}}",
		"reference": "{{item.reference_answer}}",
		"operation": "eq",
	}

	result := ConvertGraderMapToSDKParam(graderMap)

	require.NotNil(t, result.OfStringCheckGrader)
	require.Equal(t, "exact", result.OfStringCheckGrader.Name)
	require.Equal(t, "{{sample.output_text}}", result.OfStringCheckGrader.Input)
	require.Equal(t, "{{item.reference_answer}}", result.OfStringCheckGrader.Reference)
	require.Equal(t, openai.StringCheckGraderOperation("eq"), result.OfStringCheckGrader.Operation)
}

func TestConvertGraderMapToSDKParam_TextSimilarity(t *testing.T) {
	graderMap := map[string]interface{}{
		"type":              "text_similarity",
		"name":              "fuzzy",
		"input":             "{{sample.output_text}}",
		"reference":         "{{item.reference_answer}}",
		"evaluation_metric": "fuzzy_match",
	}

	result := ConvertGraderMapToSDKParam(graderMap)

	require.NotNil(t, result.OfTextSimilarityGrader)
	require.Equal(t, "fuzzy", result.OfTextSimilarityGrader.Name)
	require.Equal(t, "{{sample.output_text}}", result.OfTextSimilarityGrader.Input)
	require.Equal(t, "{{item.reference_answer}}", result.OfTextSimilarityGrader.Reference)
	require.Equal(t, openai.TextSimilarityGraderEvaluationMetric("fuzzy_match"), result.OfTextSimilarityGrader.EvaluationMetric)
}

func TestConvertGraderMapToSDKParam_PythonGrader(t *testing.T) {
	graderMap := map[string]interface{}{
		"type":      "python",
		"name":      "custom_grader",
		"source":    "def grade(output): return 1.0",
		"image_tag": "v1.0",
	}

	result := ConvertGraderMapToSDKParam(graderMap)

	require.NotNil(t, result.OfPythonGrader)
	require.Equal(t, "custom_grader", result.OfPythonGrader.Name)
	require.Equal(t, "def grade(output): return 1.0", result.OfPythonGrader.Source)
	require.NotNil(t, result.OfPythonGrader.ImageTag.Value)
	require.Equal(t, "v1.0", result.OfPythonGrader.ImageTag.Value)
}

func TestConvertGraderMapToSDKParam_ScoreModel(t *testing.T) {
	graderMap := map[string]interface{}{
		"type":  "score_model",
		"name":  "llm_grader",
		"model": "gpt-4",
		"input": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Grade this",
				"type":    "text",
			},
		},
		"sampling_params": map[string]interface{}{
			"temperature":           0.7,
			"top_p":                 0.9,
			"max_completion_tokens": 100,
			"seed":                  42,
		},
	}

	result := ConvertGraderMapToSDKParam(graderMap)

	require.NotNil(t, result.OfScoreModelGrader)
	require.Equal(t, "llm_grader", result.OfScoreModelGrader.Name)
	require.Equal(t, "gpt-4", result.OfScoreModelGrader.Model)
	require.Len(t, result.OfScoreModelGrader.Input, 1)
	require.NotNil(t, result.OfScoreModelGrader.SamplingParams.Temperature.Value)
	require.InDelta(t, 0.7, result.OfScoreModelGrader.SamplingParams.Temperature.Value, 0.001)
}

func TestConvertGraderMapToSDKParam_MultiGrader(t *testing.T) {
	// Multi-grader returns empty because it's handled via extraBody
	graderMap := map[string]interface{}{
		"type":             "multi",
		"name":             "strict_partial_credit",
		"calculate_output": "0.9 * exact + 0.1 * fuzzy",
		"graders": map[string]interface{}{
			"exact": map[string]interface{}{
				"type":      "string_check",
				"name":      "exact",
				"input":     "{{sample.output_text}}",
				"reference": "{{item.reference_answer}}",
				"operation": "eq",
			},
			"fuzzy": map[string]interface{}{
				"type":              "text_similarity",
				"name":              "fuzzy",
				"input":             "{{sample.output_text}}",
				"reference":         "{{item.reference_answer}}",
				"evaluation_metric": "fuzzy_match",
			},
		},
	}

	result := ConvertGraderMapToSDKParam(graderMap)

	// Multi-grader returns empty union - actual handling is via extraBody
	require.Nil(t, result.OfMultiGrader)
	require.Nil(t, result.OfStringCheckGrader)
	require.Nil(t, result.OfTextSimilarityGrader)
	require.Nil(t, result.OfPythonGrader)
	require.Nil(t, result.OfScoreModelGrader)
}

func TestBuildMultiGraderData(t *testing.T) {
	graderMap := map[string]interface{}{
		"type":             "multi",
		"name":             "strict_partial_credit",
		"calculate_output": "0.9 * exact + 0.1 * fuzzy",
		"graders": map[string]interface{}{
			"exact": map[string]interface{}{
				"type":      "string_check",
				"name":      "exact",
				"input":     "{{sample.output_text}}",
				"reference": "{{item.reference_answer}}",
				"operation": "eq",
			},
			"fuzzy": map[string]interface{}{
				"type":              "text_similarity",
				"name":              "fuzzy",
				"input":             "{{sample.output_text}}",
				"reference":         "{{item.reference_answer}}",
				"evaluation_metric": "fuzzy_match",
			},
		},
	}

	result := buildMultiGraderData(graderMap)

	require.NotNil(t, result)
	require.Equal(t, "multi", result["type"])
	require.Equal(t, "strict_partial_credit", result["name"])
	require.Equal(t, "0.9 * exact + 0.1 * fuzzy", result["calculate_output"])

	graders, ok := result["graders"].(map[string]interface{})
	require.True(t, ok)
	require.Len(t, graders, 2)

	exactGrader, hasExact := graders["exact"].(map[string]interface{})
	require.True(t, hasExact)
	require.Equal(t, "string_check", exactGrader["type"])
	require.Equal(t, "exact", exactGrader["name"])
	require.Equal(t, "eq", exactGrader["operation"])

	fuzzyGrader, hasFuzzy := graders["fuzzy"].(map[string]interface{})
	require.True(t, hasFuzzy)
	require.Equal(t, "text_similarity", fuzzyGrader["type"])
	require.Equal(t, "fuzzy", fuzzyGrader["name"])
	require.Equal(t, "fuzzy_match", fuzzyGrader["evaluation_metric"])
}

func TestConvertGraderMapToSDKParam_EmptyMap(t *testing.T) {
	result := ConvertGraderMapToSDKParam(nil)
	require.Nil(t, result.OfStringCheckGrader)
	require.Nil(t, result.OfTextSimilarityGrader)
	require.Nil(t, result.OfPythonGrader)
	require.Nil(t, result.OfScoreModelGrader)
}

func TestConvertGraderMapToSDKParam_UnknownType(t *testing.T) {
	graderMap := map[string]interface{}{
		"type": "unknown_type",
	}
	result := ConvertGraderMapToSDKParam(graderMap)
	require.Nil(t, result.OfStringCheckGrader)
	require.Nil(t, result.OfTextSimilarityGrader)
	require.Nil(t, result.OfPythonGrader)
	require.Nil(t, result.OfScoreModelGrader)
}

func TestConvertInternalJobParamToOpenAiJobParams_ReinforcementWithMultiGrader(t *testing.T) {
	epochs := 3
	config := &models.CreateFineTuningRequest{
		BaseModel:    "o4-mini-2025-04-16",
		TrainingFile: "file-abc123",
		Method: models.MethodConfig{
			Type: "reinforcement",
			Reinforcement: &models.ReinforcementConfig{
				Hyperparameters: models.HyperparametersConfig{
					Epochs:          &epochs,
					ReasoningEffort: "high",
				},
				Grader: map[string]interface{}{
					"type":             "multi",
					"name":             "strict_partial_credit",
					"calculate_output": "0.9 * exact + 0.1 * fuzzy",
					"graders": map[string]interface{}{
						"exact": map[string]interface{}{
							"type":      "string_check",
							"name":      "exact",
							"input":     "{{sample.output_text}}",
							"reference": "{{item.reference_answer}}",
							"operation": "eq",
						},
						"fuzzy": map[string]interface{}{
							"type":              "text_similarity",
							"name":              "fuzzy",
							"input":             "{{sample.output_text}}",
							"reference":         "{{item.reference_answer}}",
							"evaluation_metric": "fuzzy_match",
						},
					},
				},
			},
		},
	}

	params, extraBody, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, "reinforcement", params.Method.Type)
	// Multi-grader is set via extraBody, not SDK params
	require.Nil(t, params.Method.Reinforcement.Grader.OfMultiGrader)

	// Verify extraBody contains the grader
	require.NotNil(t, extraBody)
	method, hasMethod := extraBody["method"].(map[string]interface{})
	require.True(t, hasMethod)
	reinforcement, hasReinforcement := method["reinforcement"].(map[string]interface{})
	require.True(t, hasReinforcement)
	grader, hasGrader := reinforcement["grader"].(map[string]interface{})
	require.True(t, hasGrader)
	require.Equal(t, "multi", grader["type"])
	require.Equal(t, "strict_partial_credit", grader["name"])
	require.Equal(t, "0.9 * exact + 0.1 * fuzzy", grader["calculate_output"])

	graders, hasGraders := grader["graders"].(map[string]interface{})
	require.True(t, hasGraders)
	require.Len(t, graders, 2)
}

func TestConvertInternalJobParamToOpenAiJobParams_ReinforcementWithStringCheckGrader(t *testing.T) {
	// Verify that non-multi graders still work via SDK params
	epochs := 3
	config := &models.CreateFineTuningRequest{
		BaseModel:    "o4-mini-2025-04-16",
		TrainingFile: "file-abc123",
		Method: models.MethodConfig{
			Type: "reinforcement",
			Reinforcement: &models.ReinforcementConfig{
				Hyperparameters: models.HyperparametersConfig{
					Epochs:          &epochs,
					ReasoningEffort: "high",
				},
				Grader: map[string]interface{}{
					"type":      "string_check",
					"name":      "exact",
					"input":     "{{sample.output_text}}",
					"reference": "{{item.reference_answer}}",
					"operation": "eq",
				},
			},
		},
	}

	params, _, err := convertInternalJobParamToOpenAiJobParams(config)

	require.NoError(t, err)
	require.NotNil(t, params)
	require.Equal(t, "reinforcement", params.Method.Type)
	// Non-multi graders should be set via SDK params
	require.NotNil(t, params.Method.Reinforcement.Grader.OfStringCheckGrader)
	require.Equal(t, "exact", params.Method.Reinforcement.Grader.OfStringCheckGrader.Name)
}

// Helper functions
func int64Ptr(i int64) *int64 {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}
