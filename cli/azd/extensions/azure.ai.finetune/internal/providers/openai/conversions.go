// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/pkg/models"
	"github.com/openai/openai-go/v3"
)

// OpenAI Status Constants - matches OpenAI SDK values
const (
	OpenAIStatusValidatingFiles = "validating_files"
	OpenAIStatusQueued          = "queued"
	OpenAIStatusRunning         = "running"
	OpenAIStatusSucceeded       = "succeeded"
	OpenAIStatusFailed          = "failed"
	OpenAIStatusCancelled       = "cancelled"
)

// mapOpenAIStatusToJobStatus converts OpenAI SDK status to domain model JobStatus
func mapOpenAIStatusToJobStatus(openaiStatus openai.FineTuningJobStatus) models.JobStatus {
	switch openaiStatus {
	case OpenAIStatusValidatingFiles, OpenAIStatusRunning:
		return models.StatusRunning
	case OpenAIStatusQueued:
		return models.StatusQueued
	case OpenAIStatusSucceeded:
		return models.StatusSucceeded
	case OpenAIStatusFailed:
		return models.StatusFailed
	case OpenAIStatusCancelled:
		return models.StatusCancelled
	default:
		return models.StatusPending // Default fallback
	}
}

// convertOpenAIJobToModel converts OpenAI SDK job to domain model
func convertOpenAIJobToModel(openaiJob openai.FineTuningJob) *models.FineTuningJob {
	return &models.FineTuningJob{
		ID:             openaiJob.ID,
		Status:         mapOpenAIStatusToJobStatus(openaiJob.Status),
		BaseModel:      openaiJob.Model,
		FineTunedModel: openaiJob.FineTunedModel,
		CreatedAt:      utils.UnixTimestampToUTC(openaiJob.CreatedAt),
	}
}

// ConvertYAMLToJobParams converts a YAML fine-tuning configuration to OpenAI job parameters
func convertInternalJobParamToOpenAiJobParams(config *models.CreateFineTuningRequest) (openai.FineTuningJobNewParams, error) {
	jobParams := openai.FineTuningJobNewParams{
		Model:        openai.FineTuningJobNewParamsModel(config.BaseModel),
		TrainingFile: config.TrainingDataID,
	}

	if config.ValidationDataID != "" {
		jobParams.ValidationFile = openai.String(config.ValidationDataID)
	}

	// Set optional fields
	if config.Suffix != "" {
		jobParams.Suffix = openai.String(config.Suffix)
	}

	if config.Seed != 0 {
		jobParams.Seed = openai.Int(config.Seed)
	}

	// Set metadata if provided
	if len(config.Metadata) > 0 {
		jobParams.Metadata = make(map[string]string)
		for k, v := range config.Metadata {
			jobParams.Metadata[k] = v
		}
	}

	//TODO Need to set hyperparameters, method, integrations
	return jobParams, nil
}
