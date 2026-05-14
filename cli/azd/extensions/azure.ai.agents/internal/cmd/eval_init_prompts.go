// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func promptEvalInitOptions(ctx context.Context, resolved *evalResolvedContext, flags *evalInitFlags, noPrompt bool) error {
	azdClient := resolved.azdClient
	if noPrompt {
		return nil
	}

	if flags.name == "" {
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Eval suite name",
				DefaultValue:   defaultEvalName,
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for eval suite name: %w", err)
		}
		if value := strings.TrimSpace(resp.Value); value != "" {
			flags.name = value
		}
	}

	needsGeneration := flags.dataset == "" || len(flags.evaluators) == 0

	if flags.genInstruction == "" && needsGeneration && resolved.agentKind != agent_yaml.AgentKindPrompt {
		// Let the user choose between inline text or loading from a file.
		inputChoices := []*azdext.SelectChoice{
			{Label: "Type inline", Value: "inline"},
			{Label: "Load from file", Value: "file"},
		}
		defaultIdx := int32(0)
		selResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How would you like to provide the generation instruction?",
				Choices:       inputChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for instruction input method: %w", err)
		}

		if inputChoices[int(*selResp.Value)].Value == "file" {
			// Prompt for the file path.
			pathResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Path to instruction file",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for instruction file path: %w", err)
			}
			filePath := strings.TrimSpace(pathResp.Value)
			data, err := os.ReadFile(filePath) //nolint:gosec // user-provided instruction file path
			if err != nil {
				return fmt.Errorf("reading instruction file %q: %w", filePath, err)
			}
			flags.genInstruction = strings.TrimSpace(string(data))
		} else {
			// Inline text input.
			resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Describe what this agent does and what scenarios to test",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for generation instruction: %w", err)
			}
			flags.genInstruction = strings.TrimSpace(resp.Value)
		}
	}

	// TODO: Re-enable trace prompt once trace support is ready.
	// // Ask whether to include traces, unless already set via flags.
	// if flags.traceDays == 0 && needsGeneration {
	// 	confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
	// 		Options: &azdext.ConfirmOptions{
	// 			Message:      "Include agent traces for evaluation?",
	// 			DefaultValue: new(bool), // default false
	// 		},
	// 	})
	// 	if err != nil {
	// 		return fmt.Errorf("prompting for trace inclusion: %w", err)
	// 	}
	// 	if confirmResp.GetValue() {
	// 		rangeChoices := []*azdext.SelectChoice{
	// 			{Label: "Last Day", Value: "1"},
	// 			{Label: "Last 7 Days", Value: "7"},
	// 			{Label: "Last 30 Days", Value: "30"},
	// 			{Label: "Last 90 Days", Value: "90"},
	// 		}
	// 		defaultRangeIdx := int32(1) // 7 days
	// 		rangeResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
	// 			Options: &azdext.SelectOptions{
	// 				Message:       "Select trace time range",
	// 				Choices:       rangeChoices,
	// 				SelectedIndex: &defaultRangeIdx,
	// 			},
	// 		})
	// 		if err != nil {
	// 			return fmt.Errorf("prompting for trace time range: %w", err)
	// 		}
	// 		days, _ := strconv.Atoi(rangeChoices[int(*rangeResp.Value)].Value)
	// 		flags.traceDays = days
	// 	}
	// }

	if !needsGeneration {
		return nil
	}

	if !flags.evalModelSet {
		// Read the deployed model name from the azd environment to use as default.
		var deployedModel string
		if resolved.envName != "" {
			if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: resolved.envName,
				Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			}); err == nil && v.Value != "" {
				deployedModel = v.Value
			}
		}

		choices := buildModelChoices(deployedModel)
		defaultIndex := int32(0)
		resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select the model for evaluation and generation",
				Choices:       choices,
				SelectedIndex: &defaultIndex,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for evaluation model: %w", err)
		}
		selected := choices[int(*resp.Value)].Value

		// User chose to pick from another deployment in the project.
		if selected == selectOtherDeployment {
			selected, err = promptProjectDeployment(ctx, resolved)
			if err != nil {
				return err
			}
		}
		flags.evalModel = selected
	}

	if !flags.maxSamplesSet {
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Max samples",
				DefaultValue:   strconv.Itoa(defaultEvalSamples),
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for max samples: %w", err)
		}
		if value := strings.TrimSpace(resp.Value); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed <= 0 {
				return fmt.Errorf("--max-samples must be a positive integer")
			}
			flags.maxSamples = parsed
		}
	}

	return nil
}

// selectOtherDeployment is the sentinel value for the "Select another deployment"
// choice in the model prompt.
const selectOtherDeployment = "__select_other_deployment__"

// buildModelChoices builds the initial model choices for the generation model
// prompt. When deployedModel is non-empty it appears first as the default.
// A "Select another deployment" option is always appended so the user can
// browse all deployments in the Foundry project.
func buildModelChoices(deployedModel string) []*azdext.SelectChoice {
	var choices []*azdext.SelectChoice
	if deployedModel != "" {
		choices = append(choices, &azdext.SelectChoice{
			Label: deployedModel + " (deployed)",
			Value: deployedModel,
		})
	}
	choices = append(choices, &azdext.SelectChoice{
		Label: "Select another deployment",
		Value: selectOtherDeployment,
	})
	return choices
}

// promptProjectDeployment fetches model deployments from the Foundry project
// and prompts the user to select one.
func promptProjectDeployment(ctx context.Context, resolved *evalResolvedContext) (string, error) {
	var deployments []FoundryDeploymentInfo
	if resolved.envName != "" {
		if v, err := resolved.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: resolved.envName,
			Key:     "AZURE_AI_PROJECT_ID",
		}); err == nil && v.Value != "" {
			if project, err := extractProjectDetails(v.Value); err == nil {
				if cred, err := newAgentCredential(); err == nil {
					deployments, _ = listProjectDeployments(
						ctx, cred,
						project.SubscriptionId,
						project.ResourceGroupName,
						project.AccountName,
					)
				}
			}
		}
	}
	if len(deployments) == 0 {
		return "", fmt.Errorf("no model deployments found in the Foundry project")
	}

	choices := make([]*azdext.SelectChoice, len(deployments))
	for i, d := range deployments {
		label := d.Name
		if d.ModelName != "" {
			label = fmt.Sprintf("%s (%s)", d.Name, d.ModelName)
		}
		choices[i] = &azdext.SelectChoice{Label: label, Value: d.Name}
	}

	defaultIndex := int32(0)
	resp, err := resolved.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select a model deployment",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return "", fmt.Errorf("prompting for model deployment: %w", err)
	}
	return choices[int(*resp.Value)].Value, nil
}

// promptRegenerateChoices asks the user whether to regenerate the existing
// dataset and evaluator using individual yes/no confirmations.
func promptRegenerateChoices(
	ctx context.Context,
	resolved *evalResolvedContext,
	existingCfg *evalConfig,
	flags *evalInitFlags,
) error {
	prompt := resolved.azdClient.Prompt()

	// Ask about dataset.
	datasetLabel := existingCfg.DatasetFile
	if datasetLabel == "" && existingCfg.DatasetReference != nil {
		datasetLabel = existingCfg.DatasetReference.Name
	}
	if datasetLabel != "" {
		resp, err := prompt.Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("Existing dataset: %s. Do you want to regenerate?", datasetLabel),
				DefaultValue: new(false),
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for dataset regeneration: %w", err)
		}
		if resp.Value != nil && *resp.Value {
			flags.regenerateDataset = true
		}
	}

	// Ask about evaluator — only generated (non-builtin) evaluators can be regenerated.
	generated, builtin := eval_api.SplitEvaluators(existingCfg.Evaluators)
	if len(generated) > 0 {
		generatedLabel := strings.Join(generated, ", ")
		msg := fmt.Sprintf("Existing evaluator: %s. Do you want to regenerate?", generatedLabel)
		if len(builtin) > 0 {
			msg = fmt.Sprintf(
				"Existing evaluator: %s (built-in evaluators %s will be kept). Do you want to regenerate?",
				generatedLabel, strings.Join(builtin, ", "),
			)
		}
		resp, err := prompt.Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      msg,
				DefaultValue: new(false),
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for evaluator regeneration: %w", err)
		}
		if resp.Value != nil && *resp.Value {
			flags.regenerateEvaluator = true
		}
	}

	return nil
}
