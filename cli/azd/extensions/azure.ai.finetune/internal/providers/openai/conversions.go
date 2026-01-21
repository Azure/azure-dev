// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"encoding/json"
	"strings"
	"time"

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
		Duration:       models.Duration(utils.CalculateDuration(openaiJob.CreatedAt, openaiJob.FinishedAt)),
	}
}

// convertOpenAIJobToDetailModel converts OpenAI SDK job to detailed domain model
func convertOpenAIJobToDetailModel(openaiJob *openai.FineTuningJob) *models.FineTuningJobDetail {
	// Extract hyperparameters from OpenAI job
	hyperparameters := &models.Hyperparameters{}
	if openaiJob.Method.Type == "supervised" {
		hyperparameters.BatchSize = openaiJob.Method.Supervised.Hyperparameters.BatchSize.OfInt
		hyperparameters.LearningRateMultiplier = openaiJob.Method.Supervised.Hyperparameters.LearningRateMultiplier.OfFloat
		hyperparameters.NEpochs = openaiJob.Method.Supervised.Hyperparameters.NEpochs.OfInt
	} else if openaiJob.Method.Type == "dpo" {
		hyperparameters.BatchSize = openaiJob.Method.Dpo.Hyperparameters.BatchSize.OfInt
		hyperparameters.LearningRateMultiplier = openaiJob.Method.Dpo.Hyperparameters.LearningRateMultiplier.OfFloat
		hyperparameters.NEpochs = openaiJob.Method.Dpo.Hyperparameters.NEpochs.OfInt
		hyperparameters.Beta = openaiJob.Method.Dpo.Hyperparameters.Beta.OfFloat
	} else if openaiJob.Method.Type == "reinforcement" {
		hyperparameters.BatchSize = openaiJob.Method.Reinforcement.Hyperparameters.BatchSize.OfInt
		hyperparameters.LearningRateMultiplier = openaiJob.Method.Reinforcement.Hyperparameters.LearningRateMultiplier.OfFloat
		hyperparameters.NEpochs = openaiJob.Method.Reinforcement.Hyperparameters.NEpochs.OfInt
		hyperparameters.ComputeMultiplier = openaiJob.Method.Reinforcement.Hyperparameters.ComputeMultiplier.OfFloat
		hyperparameters.EvalInterval = openaiJob.Method.Reinforcement.Hyperparameters.EvalInterval.OfInt
		hyperparameters.EvalSamples = openaiJob.Method.Reinforcement.Hyperparameters.EvalSamples.OfInt
		if openaiJob.Method.Reinforcement.Hyperparameters.ReasoningEffort != "" {
			hyperparameters.ReasoningEffort = string(openaiJob.Method.Reinforcement.Hyperparameters.ReasoningEffort)
		}
	} else {
		// Fallback to top-level hyperparameters (for backward compatibility)
		hyperparameters.BatchSize = openaiJob.Hyperparameters.BatchSize.OfInt
		hyperparameters.LearningRateMultiplier = openaiJob.Hyperparameters.LearningRateMultiplier.OfFloat
		hyperparameters.NEpochs = openaiJob.Hyperparameters.NEpochs.OfInt
	}

	status := mapOpenAIStatusToJobStatus(openaiJob.Status)

	// Only set FinishedAt for terminal states
	var finishedAt *time.Time
	if utils.IsTerminalStatus(status) && openaiJob.FinishedAt > 0 {
		t := utils.UnixTimestampToUTC(openaiJob.FinishedAt)
		finishedAt = &t
	}

	// Only set EstimatedFinish for non-terminal states
	var estimatedFinish *time.Time
	if !utils.IsTerminalStatus(status) && openaiJob.EstimatedFinish > 0 {
		t := utils.UnixTimestampToUTC(openaiJob.EstimatedFinish)
		estimatedFinish = &t
	}

	jobDetail := &models.FineTuningJobDetail{
		ID:              openaiJob.ID,
		Status:          status,
		Model:           openaiJob.Model,
		FineTunedModel:  openaiJob.FineTunedModel,
		CreatedAt:       utils.UnixTimestampToUTC(openaiJob.CreatedAt),
		FinishedAt:      finishedAt,
		EstimatedFinish: estimatedFinish,
		Method:          openaiJob.Method.Type,
		TrainingFile:    openaiJob.TrainingFile,
		ValidationFile:  openaiJob.ValidationFile,
		Hyperparameters: hyperparameters,
		Seed:            openaiJob.Seed,
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

// Converts the internal create finetuning request model to OpenAI job parameters
func convertInternalJobParamToOpenAiJobParams(config *models.CreateFineTuningRequest) (*openai.FineTuningJobNewParams, map[string]interface{}, error) {
	jobParams := openai.FineTuningJobNewParams{
		Model:        openai.FineTuningJobNewParamsModel(config.BaseModel),
		TrainingFile: config.TrainingFile,
	}

	if config.ValidationFile != nil && *config.ValidationFile != "" {
		jobParams.ValidationFile = openai.String(*config.ValidationFile)
	}

	// Set optional fields
	if config.Suffix != nil && *config.Suffix != "" {
		jobParams.Suffix = openai.String(*config.Suffix)
	}

	if config.Seed != nil {
		jobParams.Seed = openai.Int(*config.Seed)
	}

	// Set metadata if provided
	if len(config.Metadata) > 0 {
		jobParams.Metadata = make(map[string]string)
		for k, v := range config.Metadata {
			jobParams.Metadata[k] = v
		}
	}

	// Set hyperparameters if provided
	if config.Method.Type == "supervised" && config.Method.Supervised != nil {
		hp := config.Method.Supervised.Hyperparameters
		supervisedMethod := openai.SupervisedMethodParam{
			Hyperparameters: openai.SupervisedHyperparameters{},
		}

		if hp.BatchSize != nil {
			if batchSize := convertHyperparameterToInt(hp.BatchSize); batchSize != nil {
				supervisedMethod.Hyperparameters.BatchSize = openai.SupervisedHyperparametersBatchSizeUnion{
					OfInt: openai.Int(*batchSize),
				}
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				supervisedMethod.Hyperparameters.LearningRateMultiplier = openai.SupervisedHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				supervisedMethod.Hyperparameters.NEpochs = openai.SupervisedHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			}
		}

		jobParams.Method = openai.FineTuningJobNewParamsMethod{
			Type:       "supervised",
			Supervised: supervisedMethod,
		}

	} else if config.Method.Type == "dpo" && config.Method.DPO != nil {
		hp := config.Method.DPO.Hyperparameters
		dpoMethod := openai.DpoMethodParam{
			Hyperparameters: openai.DpoHyperparameters{},
		}

		if hp.BatchSize != nil {
			if batchSize := convertHyperparameterToInt(hp.BatchSize); batchSize != nil {
				dpoMethod.Hyperparameters.BatchSize = openai.DpoHyperparametersBatchSizeUnion{
					OfInt: openai.Int(*batchSize),
				}
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				dpoMethod.Hyperparameters.LearningRateMultiplier = openai.DpoHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				dpoMethod.Hyperparameters.NEpochs = openai.DpoHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			}
		}

		if hp.Beta != nil {
			if beta := convertHyperparameterToFloat(hp.Beta); beta != nil {
				dpoMethod.Hyperparameters.Beta = openai.DpoHyperparametersBetaUnion{
					OfFloat: openai.Float(*beta),
				}
			}
		}

		jobParams.Method = openai.FineTuningJobNewParamsMethod{
			Type: "dpo",
			Dpo:  dpoMethod,
		}

	} else if config.Method.Type == "reinforcement" && config.Method.Reinforcement != nil {
		hp := config.Method.Reinforcement.Hyperparameters
		reinforcementMethod := openai.ReinforcementMethodParam{
			Hyperparameters: openai.ReinforcementHyperparameters{},
		}

		if hp.BatchSize != nil {
			if batchSize := convertHyperparameterToInt(hp.BatchSize); batchSize != nil {
				reinforcementMethod.Hyperparameters.BatchSize = openai.ReinforcementHyperparametersBatchSizeUnion{
					OfInt: openai.Int(*batchSize),
				}
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				reinforcementMethod.Hyperparameters.LearningRateMultiplier = openai.ReinforcementHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				reinforcementMethod.Hyperparameters.NEpochs = openai.ReinforcementHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			}
		}

		if hp.ComputeMultiplier != nil {
			if compute := convertHyperparameterToFloat(hp.ComputeMultiplier); compute != nil {
				reinforcementMethod.Hyperparameters.ComputeMultiplier = openai.ReinforcementHyperparametersComputeMultiplierUnion{
					OfFloat: openai.Float(*compute),
				}
			}
		}

		if hp.EvalInterval != nil {
			if evalSteps := convertHyperparameterToInt(hp.EvalInterval); evalSteps != nil {
				reinforcementMethod.Hyperparameters.EvalInterval = openai.ReinforcementHyperparametersEvalIntervalUnion{
					OfInt: openai.Int(*evalSteps),
				}
			}
		}

		if hp.EvalSamples != nil {
			if evalSamples := convertHyperparameterToInt(hp.EvalSamples); evalSamples != nil {
				reinforcementMethod.Hyperparameters.EvalSamples = openai.ReinforcementHyperparametersEvalSamplesUnion{
					OfInt: openai.Int(*evalSamples),
				}
			}
		}

		if hp.ReasoningEffort != "" {
			reinforcementMethod.Hyperparameters.ReasoningEffort = getReasoningEffortValue(hp.ReasoningEffort)
		}

		grader := config.Method.Reinforcement.Grader
		if grader != nil {
			// Convert grader to JSON and unmarshal to ReinforcementMethodGraderUnionParam
			graderJSON, err := json.Marshal(grader)
			if err != nil {
				return nil, nil, err
			}

			var graderUnion openai.ReinforcementMethodGraderUnionParam
			err = json.Unmarshal(graderJSON, &graderUnion)
			if err != nil {
				return nil, nil, err
			}
			reinforcementMethod.Grader = graderUnion
		}

		jobParams.Method = openai.FineTuningJobNewParamsMethod{
			Type:          "reinforcement",
			Reinforcement: reinforcementMethod,
		}
	}

	// Set integrations if provided
	if len(config.Integrations) > 0 {
		var integrations []openai.FineTuningJobNewParamsIntegration

		for _, integration := range config.Integrations {
			if integration.Type == "" || integration.Type == "wandb" {

				wandbConfigJSON, err := json.Marshal(integration.Config)
				if err != nil {
					return nil, nil, err
				}

				var wandbConfig openai.FineTuningJobNewParamsIntegrationWandb
				err = json.Unmarshal(wandbConfigJSON, &wandbConfig)
				if err != nil {
					return nil, nil, err
				}
				integrations = append(integrations, openai.FineTuningJobNewParamsIntegration{
					Type:  "wandb",
					Wandb: wandbConfig,
				})
			}
		}

		if len(integrations) > 0 {
			jobParams.Integrations = integrations
		}
	}

	// Return extraBody as second value for passing via WithJSONSet
	return &jobParams, config.ExtraBody, nil
}

// convertHyperparameterToInt converts interface{} hyperparameter to *int64
func convertHyperparameterToInt(value interface{}) *int64 {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case int:
		val := int64(v)
		return &val
	case int64:
		return &v
	case float64:
		val := int64(v)
		return &val
	case string:
		// "auto" string handled separately
		return nil
	default:
		return nil
	}
}

// convertHyperparameterToFloat converts interface{} hyperparameter to *float64
func convertHyperparameterToFloat(value interface{}) *float64 {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case int:
		val := float64(v)
		return &val
	case int64:
		val := float64(v)
		return &val
	case float64:
		return &v
	case string:
		// "auto" string handled separately
		return nil
	default:
		return nil
	}
}

func getReasoningEffortValue(effort string) openai.ReinforcementHyperparametersReasoningEffort {

	switch strings.ToLower(effort) {
	case "low":
		return openai.ReinforcementHyperparametersReasoningEffortLow
	case "medium":
		return openai.ReinforcementHyperparametersReasoningEffortMedium
	case "high":
		return openai.ReinforcementHyperparametersReasoningEffortHigh
	default:
		return openai.ReinforcementHyperparametersReasoningEffortDefault
	}
}
