// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"

	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/pkg/models"
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

// convertOpenAIJobToDetailModel converts OpenAI SDK job to detailed domain model
func convertOpenAIJobToDetailModel(openaiJob *openai.FineTuningJob) *models.FineTuningJobDetail {
	// Extract hyperparameters from OpenAI job
	hyperparameters := &models.Hyperparameters{}
	hyperparameters.BatchSize = openaiJob.Hyperparameters.BatchSize.OfInt
	hyperparameters.LearningRateMultiplier = openaiJob.Hyperparameters.LearningRateMultiplier.OfFloat
	hyperparameters.NEpochs = openaiJob.Hyperparameters.NEpochs.OfInt

	jobDetail := &models.FineTuningJobDetail{
		ID:              openaiJob.ID,
		Status:          mapOpenAIStatusToJobStatus(openaiJob.Status),
		Model:           openaiJob.Model,
		FineTunedModel:  openaiJob.FineTunedModel,
		CreatedAt:       utils.UnixTimestampToUTC(openaiJob.CreatedAt),
		FinishedAt:      utils.UnixTimestampToUTC(openaiJob.FinishedAt),
		Method:          openaiJob.Method.Type,
		TrainingFile:    openaiJob.TrainingFile,
		ValidationFile:  openaiJob.ValidationFile,
		Hyperparameters: hyperparameters,
	}

	return jobDetail
}

// convertOpenAIJobEventsToModel converts OpenAI SDK job events to domain model
func convertOpenAIJobEventsToModel(eventsPage *pagination.CursorPage[openai.FineTuningJobEvent]) *models.JobEventsList {
	var events []models.JobEvent
	for _, event := range eventsPage.Data {
		jobEvent := models.JobEvent{
			ID:        event.ID,
			CreatedAt: utils.UnixTimestampToUTC(event.CreatedAt),
			Level:     string(event.Level),
			Message:   event.Message,
			Data:      event.Data,
			Type:      string(event.Type),
		}
		events = append(events, jobEvent)
	}

	return &models.JobEventsList{
		Data:    events,
		HasMore: eventsPage.HasMore,
	}
}

// convertOpenAIJobCheckpointsToModel converts OpenAI SDK job checkpoints to domain model
func convertOpenAIJobCheckpointsToModel(checkpointsPage *pagination.CursorPage[openai.FineTuningJobCheckpoint]) *models.JobCheckpointsList {
	var checkpoints []models.JobCheckpoint

	for _, checkpoint := range checkpointsPage.Data {
		metrics := &models.CheckpointMetrics{
			FullValidLoss:              checkpoint.Metrics.FullValidLoss,
			FullValidMeanTokenAccuracy: checkpoint.Metrics.FullValidMeanTokenAccuracy,
		}

		jobCheckpoint := models.JobCheckpoint{
			ID:                       checkpoint.ID,
			CreatedAt:                utils.UnixTimestampToUTC(checkpoint.CreatedAt),
			FineTunedModelCheckpoint: checkpoint.FineTunedModelCheckpoint,
			Metrics:                  metrics,
			FineTuningJobID:          checkpoint.FineTuningJobID,
			StepNumber:               checkpoint.StepNumber,
		}
		checkpoints = append(checkpoints, jobCheckpoint)
	}

	return &models.JobCheckpointsList{
		Data:    checkpoints,
		HasMore: checkpointsPage.HasMore,
	}
}
