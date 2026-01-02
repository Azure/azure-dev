// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"encoding/json"
	"strings"

	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/pkg/models"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared/constant"
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

// ConvertOpenAIJobToModel converts OpenAI SDK job to domain model
func ConvertOpenAIJobToModel(openaiJob openai.FineTuningJob) *models.FineTuningJob {
	return &models.FineTuningJob{
		ID:             openaiJob.ID,
		Status:         mapOpenAIStatusToJobStatus(openaiJob.Status),
		BaseModel:      openaiJob.Model,
		FineTunedModel: openaiJob.FineTunedModel,
		CreatedAt:      utils.UnixTimestampToUTC(openaiJob.CreatedAt),
	}
}

// Converts the internal create finetuning request model to OpenAI job parameters
func ConvertInternalJobParamToOpenAiJobParams(config *models.CreateFineTuningRequest) (*openai.FineTuningJobNewParams, error) {
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
			} else if strVal, ok := hp.BatchSize.(string); ok && strVal == "auto" {
				supervisedMethod.Hyperparameters.BatchSize = openai.SupervisedHyperparametersBatchSizeUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				supervisedMethod.Hyperparameters.LearningRateMultiplier = openai.SupervisedHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			} else if strVal, ok := hp.LearningRateMultiplier.(string); ok && strVal == "auto" {
				supervisedMethod.Hyperparameters.LearningRateMultiplier = openai.SupervisedHyperparametersLearningRateMultiplierUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				supervisedMethod.Hyperparameters.NEpochs = openai.SupervisedHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			} else if strVal, ok := hp.Epochs.(string); ok && strVal == "auto" {
				supervisedMethod.Hyperparameters.NEpochs = openai.SupervisedHyperparametersNEpochsUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
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
		} else if strVal, ok := hp.BatchSize.(string); ok && strVal == "auto" {
			dpoMethod.Hyperparameters.BatchSize = openai.DpoHyperparametersBatchSizeUnion{
				OfAuto: constant.ValueOf[constant.Auto](),
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				dpoMethod.Hyperparameters.LearningRateMultiplier = openai.DpoHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			} else if strVal, ok := hp.LearningRateMultiplier.(string); ok && strVal == "auto" {
				dpoMethod.Hyperparameters.LearningRateMultiplier = openai.DpoHyperparametersLearningRateMultiplierUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				dpoMethod.Hyperparameters.NEpochs = openai.DpoHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			} else if strVal, ok := hp.Epochs.(string); ok && strVal == "auto" {
				dpoMethod.Hyperparameters.NEpochs = openai.DpoHyperparametersNEpochsUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.Beta != nil {
			if beta := convertHyperparameterToFloat(hp.Beta); beta != nil {
				dpoMethod.Hyperparameters.Beta = openai.DpoHyperparametersBetaUnion{
					OfFloat: openai.Float(*beta),
				}
			} else if strVal, ok := hp.Beta.(string); ok && strVal == "auto" {
				dpoMethod.Hyperparameters.Beta = openai.DpoHyperparametersBetaUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
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
			} else if strVal, ok := hp.BatchSize.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.BatchSize = openai.ReinforcementHyperparametersBatchSizeUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.LearningRateMultiplier != nil {
			if lr := convertHyperparameterToFloat(hp.LearningRateMultiplier); lr != nil {
				reinforcementMethod.Hyperparameters.LearningRateMultiplier = openai.ReinforcementHyperparametersLearningRateMultiplierUnion{
					OfFloat: openai.Float(*lr),
				}
			} else if strVal, ok := hp.LearningRateMultiplier.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.LearningRateMultiplier = openai.ReinforcementHyperparametersLearningRateMultiplierUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.Epochs != nil {
			if epochs := convertHyperparameterToInt(hp.Epochs); epochs != nil {
				reinforcementMethod.Hyperparameters.NEpochs = openai.ReinforcementHyperparametersNEpochsUnion{
					OfInt: openai.Int(*epochs),
				}
			} else if strVal, ok := hp.Epochs.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.NEpochs = openai.ReinforcementHyperparametersNEpochsUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.ComputeMultiplier != nil {
			if compute := convertHyperparameterToFloat(hp.ComputeMultiplier); compute != nil {
				reinforcementMethod.Hyperparameters.ComputeMultiplier = openai.ReinforcementHyperparametersComputeMultiplierUnion{
					OfFloat: openai.Float(*compute),
				}
			} else if strVal, ok := hp.ComputeMultiplier.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.ComputeMultiplier = openai.ReinforcementHyperparametersComputeMultiplierUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.EvalInterval != nil {
			if evalSteps := convertHyperparameterToInt(hp.EvalInterval); evalSteps != nil {
				reinforcementMethod.Hyperparameters.EvalInterval = openai.ReinforcementHyperparametersEvalIntervalUnion{
					OfInt: openai.Int(*evalSteps),
				}
			} else if strVal, ok := hp.EvalInterval.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.EvalInterval = openai.ReinforcementHyperparametersEvalIntervalUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
				}
			}
		}

		if hp.EvalSamples != nil {
			if evalSamples := convertHyperparameterToInt(hp.EvalSamples); evalSamples != nil {
				reinforcementMethod.Hyperparameters.EvalSamples = openai.ReinforcementHyperparametersEvalSamplesUnion{
					OfInt: openai.Int(*evalSamples),
				}
			} else if strVal, ok := hp.EvalSamples.(string); ok && strVal == "auto" {
				reinforcementMethod.Hyperparameters.EvalSamples = openai.ReinforcementHyperparametersEvalSamplesUnion{
					OfAuto: constant.ValueOf[constant.Auto](),
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
				return nil, err
			}

			var graderUnion openai.ReinforcementMethodGraderUnionParam
			err = json.Unmarshal(graderJSON, &graderUnion)
			if err != nil {
				return nil, err
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
					return nil, err
				}

				var wandbConfig openai.FineTuningJobNewParamsIntegrationWandb
				err = json.Unmarshal(wandbConfigJSON, &wandbConfig)
				if err != nil {
					return nil, err
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

	return &jobParams, nil
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
