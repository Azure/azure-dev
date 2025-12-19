// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/openai/openai-go/v3"

	FTYaml "azure.ai.finetune/internal/fine_tuning_yaml"
)

// ConvertYAMLToJobParams converts a YAML fine-tuning configuration to OpenAI job parameters
func ConvertYAMLToJobParams(config *FTYaml.FineTuningConfig, trainingFileID, validationFileID string) (openai.FineTuningJobNewParams, error) {
	jobParams := openai.FineTuningJobNewParams{
		Model:        openai.FineTuningJobNewParamsModel(config.Model),
		TrainingFile: trainingFileID,
	}

	if validationFileID != "" {
		jobParams.ValidationFile = openai.String(validationFileID)
	}

	// Set optional fields
	if config.Suffix != nil {
		jobParams.Suffix = openai.String(*config.Suffix)
	}

	if config.Seed != nil {
		jobParams.Seed = openai.Int(*config.Seed)
	}

	// Set metadata if provided
	if config.Metadata != nil && len(config.Metadata) > 0 {
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

		jobParams.Method = openai.FineTuningJobNewParamsMethod{
			Type:          "reinforcement",
			Reinforcement: reinforcementMethod,
		}
	}

	return jobParams, nil
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
