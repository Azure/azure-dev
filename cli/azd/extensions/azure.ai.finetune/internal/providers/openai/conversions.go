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
	OpenAIStatusPausing         = "pausing"
	OpenAIStatusResuming        = "resuming"
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
	case OpenAIStatusPausing:
		return models.StatusPausing
	case OpenAIStatusResuming:
		return models.StatusResuming
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
	// Extract extra fields from the API response
	extraFields := make(map[string]interface{})
	for key, field := range openaiJob.JSON.ExtraFields {
		// Parse the raw JSON value
		var value interface{}
		if err := json.Unmarshal([]byte(field.Raw()), &value); err == nil {
			extraFields[key] = value
		} else {
			extraFields[key] = string(field.Raw())
		}
	}

	var graderJSON json.RawMessage
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
		// Extract grader using the common function
		graderData := ExtractGraderFromOpenAI(openaiJob.Method.Reinforcement.Grader)
		if graderData != nil {
			graderBytes, err := json.Marshal(graderData)
			if err == nil {
				graderJSON = graderBytes
			}
		}
	} else {
		// Fallback to top-level hyperparameters (for backward compatibility)
		openaiJob.Method.Type = "supervised"
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
		Grader:          graderJSON,
		Seed:            openaiJob.Seed,
		ExtraFields:     extraFields,
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
		if len(grader) > 0 {
			graderType, _ := grader["type"].(string)
			if graderType == "multi" {
				// Handle multi-grader via extraBody due to SDK limitations
				// SDK type definition doesn't match API spec (expects map not union)
				multiGraderData := buildMultiGraderData(grader)
				if multiGraderData != nil {
					if config.ExtraBody == nil {
						config.ExtraBody = make(map[string]interface{})
					}
					// Use dot-path key so WithJSONSet merges ONLY the grader field
					// This preserves method.type, hyperparameters, and any existing extra_body fields
					config.ExtraBody["method.reinforcement.grader"] = multiGraderData
				}
			} else {
				// Convert grader map to SDK param type using the common function
				reinforcementMethod.Grader = ConvertGraderMapToSDKParam(grader)
			}
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

// ExtractGraderFromOpenAI extracts grader data from OpenAI SDK response to a clean map
// This is used when cloning a job to YAML - only extracts relevant fields per grader type
func ExtractGraderFromOpenAI(grader openai.ReinforcementMethodGraderUnion) map[string]interface{} {
	if grader.Type == "" {
		return nil
	}

	graderType := grader.Type
	var graderData map[string]interface{}

	switch graderType {
	case "python":
		g := grader.AsPythonGrader()
		graderData = map[string]interface{}{
			"type":   graderType,
			"name":   g.Name,
			"source": g.Source,
		}
		if g.ImageTag != "" {
			graderData["image_tag"] = g.ImageTag
		}
	case "string_check":
		g := grader.AsStringCheckGrader()
		graderData = map[string]interface{}{
			"type":      graderType,
			"input":     g.Input,
			"name":      g.Name,
			"operation": string(g.Operation),
			"reference": g.Reference,
		}
	case "text_similarity":
		g := grader.AsTextSimilarityGrader()
		graderData = map[string]interface{}{
			"type":              graderType,
			"input":             g.Input,
			"name":              g.Name,
			"reference":         g.Reference,
			"evaluation_metric": string(g.EvaluationMetric),
		}
	case "score_model":
		g := grader.AsScoreModelGrader()
		graderData = map[string]interface{}{
			"type":  graderType,
			"input": g.Input,
			"name":  g.Name,
			"model": g.Model,
		}
		// Extract sampling params if present
		samplingData := map[string]interface{}{}
		if g.SamplingParams.Temperature != 0 {
			samplingData["temperature"] = g.SamplingParams.Temperature
		}
		if g.SamplingParams.TopP != 0 {
			samplingData["top_p"] = g.SamplingParams.TopP
		}
		if g.SamplingParams.MaxCompletionsTokens != 0 {
			samplingData["max_completion_tokens"] = g.SamplingParams.MaxCompletionsTokens
		}
		if g.SamplingParams.Seed != 0 {
			samplingData["seed"] = g.SamplingParams.Seed
		}
		if len(samplingData) > 0 {
			graderData["sampling_params"] = samplingData
		}
	case "multi":
		g := grader.AsMultiGrader()
		graderData = map[string]interface{}{
			"type":             graderType,
			"name":             g.Name,
			"calculate_output": g.CalculateOutput,
		}
		// Note: Multi-grader sub-graders extraction is complex due to SDK union flattening
		// For now, we store just the top-level multi-grader fields
		// Users may need to manually add sub-graders in the YAML
	}

	return graderData
}

// ConvertGraderMapToSDKParam converts a grader map (from YAML or extracted) to OpenAI SDK param type
// This is the reverse operation - used when creating a job from config
func ConvertGraderMapToSDKParam(graderMap map[string]interface{}) openai.ReinforcementMethodGraderUnionParam {
	if graderMap == nil {
		return openai.ReinforcementMethodGraderUnionParam{}
	}

	graderType, _ := graderMap["type"].(string)

	switch graderType {
	case "python":
		grader := openai.PythonGraderParam{
			Name:   getString(graderMap, "name"),
			Source: getString(graderMap, "source"),
		}
		if imageTag := getString(graderMap, "image_tag"); imageTag != "" {
			grader.ImageTag = openai.Opt(imageTag)
		}
		return openai.ReinforcementMethodGraderUnionParam{OfPythonGrader: &grader}

	case "string_check":
		grader := openai.StringCheckGraderParam{
			Input:     getString(graderMap, "input"),
			Name:      getString(graderMap, "name"),
			Operation: openai.StringCheckGraderOperation(getString(graderMap, "operation")),
			Reference: getString(graderMap, "reference"),
		}
		return openai.ReinforcementMethodGraderUnionParam{OfStringCheckGrader: &grader}

	case "text_similarity":
		grader := openai.TextSimilarityGraderParam{
			Input:            getString(graderMap, "input"),
			Name:             getString(graderMap, "name"),
			Reference:        getString(graderMap, "reference"),
			EvaluationMetric: openai.TextSimilarityGraderEvaluationMetric(getString(graderMap, "evaluation_metric")),
		}
		return openai.ReinforcementMethodGraderUnionParam{OfTextSimilarityGrader: &grader}

	case "score_model":
		grader := openai.ScoreModelGraderParam{
			Input: getScoreModelInput(graderMap, "input"),
			Name:  getString(graderMap, "name"),
			Model: getString(graderMap, "model"),
		}
		// Handle sampling parameters
		if samplingMap, ok := graderMap["sampling_params"].(map[string]interface{}); ok {
			if temp := getFloat(samplingMap, "temperature"); temp != nil {
				grader.SamplingParams.Temperature = openai.Opt(*temp)
			}
			if topP := getFloat(samplingMap, "top_p"); topP != nil {
				grader.SamplingParams.TopP = openai.Opt(*topP)
			}
			if maxTokens := getInt(samplingMap, "max_completion_tokens"); maxTokens != nil {
				grader.SamplingParams.MaxCompletionsTokens = openai.Opt(*maxTokens)
			}
			if seed := getInt(samplingMap, "seed"); seed != nil {
				grader.SamplingParams.Seed = openai.Opt(*seed)
			}
		}
		return openai.ReinforcementMethodGraderUnionParam{OfScoreModelGrader: &grader}

	case "multi":
		// Multi-grader is not directly supported due to SDK type limitations
		// Return empty and let caller handle via extraBody
		return openai.ReinforcementMethodGraderUnionParam{}
	}

	return openai.ReinforcementMethodGraderUnionParam{}
}

// Helper functions for safe type conversions
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getScoreModelInput converts input data to ScoreModelGraderInputParam slice
func getScoreModelInput(m map[string]interface{}, key string) []openai.ScoreModelGraderInputParam {
	result := []openai.ScoreModelGraderInputParam{}
	if v, ok := m[key].([]interface{}); ok {
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				inputParam := openai.ScoreModelGraderInputParam{
					Role: getString(itemMap, "role"),
				}
				if content := getString(itemMap, "content"); content != "" {
					inputParam.Content = openai.ScoreModelGraderInputContentUnionParam{
						OfString: openai.String(content),
					}
				}
				if itemType := getString(itemMap, "type"); itemType != "" {
					inputParam.Type = itemType
				}
				result = append(result, inputParam)
			}
		}
	}
	return result
}

func getFloat(m map[string]interface{}, key string) *float64 {
	switch v := m[key].(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case int64:
		f := float64(v)
		return &f
	}
	return nil
}

func getInt(m map[string]interface{}, key string) *int64 {
	switch v := m[key].(type) {
	case int:
		i := int64(v)
		return &i
	case int64:
		return &v
	case float64:
		i := int64(v)
		return &i
	}
	return nil
}

// buildMultiGraderData constructs the multi-grader data structure for API submission
// This is needed because the SDK type definition doesn't match the API spec
func buildMultiGraderData(graderMap map[string]interface{}) map[string]interface{} {
	if graderMap == nil {
		return nil
	}

	result := map[string]interface{}{
		"type":             "multi",
		"name":             getString(graderMap, "name"),
		"calculate_output": getString(graderMap, "calculate_output"),
	}

	// Build the graders map from the input
	if gradersMap, ok := graderMap["graders"].(map[string]interface{}); ok {
		graders := make(map[string]interface{})
		for key, value := range gradersMap {
			graderData, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			if built := buildGraderData(graderData); built != nil {
				graders[key] = built
			}
		}
		result["graders"] = graders
	}

	return result
}

// buildGraderData constructs a grader data structure from a grader map
// Supports all grader types that can be sub-graders of multi-grader
func buildGraderData(graderMap map[string]interface{}) map[string]interface{} {
	graderType, _ := graderMap["type"].(string)

	switch graderType {
	case "python":
		grader := map[string]interface{}{
			"type":   "python",
			"name":   getString(graderMap, "name"),
			"source": getString(graderMap, "source"),
		}
		if imageTag := getString(graderMap, "image_tag"); imageTag != "" {
			grader["image_tag"] = imageTag
		}
		return grader

	case "string_check":
		return map[string]interface{}{
			"type":      "string_check",
			"name":      getString(graderMap, "name"),
			"input":     getString(graderMap, "input"),
			"reference": getString(graderMap, "reference"),
			"operation": getString(graderMap, "operation"),
		}

	case "text_similarity":
		return map[string]interface{}{
			"type":              "text_similarity",
			"name":              getString(graderMap, "name"),
			"input":             getString(graderMap, "input"),
			"reference":         getString(graderMap, "reference"),
			"evaluation_metric": getString(graderMap, "evaluation_metric"),
		}

	case "score_model":
		grader := map[string]interface{}{
			"type":  "score_model",
			"name":  getString(graderMap, "name"),
			"model": getString(graderMap, "model"),
		}
		// Copy input array if present
		if input, ok := graderMap["input"].([]interface{}); ok {
			grader["input"] = input
		}
		// Copy sampling params if present
		if samplingParams, ok := graderMap["sampling_params"].(map[string]interface{}); ok {
			grader["sampling_params"] = samplingParams
		}
		return grader

	case "label_model":
		grader := map[string]interface{}{
			"type":  "label_model",
			"name":  getString(graderMap, "name"),
			"model": getString(graderMap, "model"),
		}
		// Copy required fields for label_model
		if input, ok := graderMap["input"].([]interface{}); ok {
			grader["input"] = input
		}
		if labels, ok := graderMap["labels"].([]interface{}); ok {
			grader["labels"] = labels
		}
		if passingLabels, ok := graderMap["passing_labels"].([]interface{}); ok {
			grader["passing_labels"] = passingLabels
		}
		return grader
	}

	return nil
}
