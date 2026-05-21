// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_init_prompts.go implements interactive prompts for the eval init
// command, including eval suite name, instruction source, trace inclusion,
// eval model selection, and regeneration choices for existing configs.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// promptEvalInitOptions runs interactive prompts for eval init options that
// were not provided via flags: name, instruction, trace days, eval model,
// and max samples.
func promptEvalInitOptions(ctx context.Context, resolved *evalResolvedContext, flags *evalInitFlags, noPrompt bool) error {
	azdClient := resolved.azdClient
	if noPrompt {
		return nil
	}

	if flags.name == "" {
		defaultName := defaultEvalName
		if resolved.agentName != "" {
			defaultName = resolved.agentName
		}
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Eval suite name",
				DefaultValue:   defaultName,
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

	needsGeneration := true // adaptive evaluator is always generated
	needsEvalGen := true

	if flags.configFile != "" && needsGeneration {
		// Config detected — show resolved values and let the user confirm or override.
		if err := promptConfigConfirmation(ctx, azdClient, resolved, flags); err != nil {
			return err
		}
	} else if flags.instruction == "" && flags.instructionFile == "" && needsGeneration {
		// Let the user choose between inline text or loading from a file.
		inputChoices := []*azdext.SelectChoice{
			{Label: "Type inline", Value: "inline"},
			{Label: "Load from file", Value: "file"},
		}
		defaultIdx := int32(0)
		selResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How would you like to provide the agent instruction?",
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
					Message:        "Path to agent instruction file",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for instruction file path: %w", err)
			}
			filePath := strings.TrimSpace(pathResp.Value)
			// Resolve relative paths against the agent project directory.
			if !filepath.IsAbs(filePath) && resolved.projectRoot != "" {
				filePath = filepath.Join(resolved.projectRoot, filePath)
			}
			if _, err := os.Stat(filePath); err != nil {
				return fmt.Errorf("instruction file %q is not accessible: %w", filePath, err)
			}
			flags.instructionFile = filePath
		} else {
			// Inline text input.
			resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Describe what this agent does and what scenarios to test",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for instruction: %w", err)
			}
			flags.instruction = strings.TrimSpace(resp.Value)
		}
	}

	// Ask whether to include traces for evaluator generation, unless already set via flags.
	if flags.traceDays == 0 && needsEvalGen {
		confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Include agent traces for evaluator generation?",
				DefaultValue: new(bool), // default false
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for trace inclusion: %w", err)
		}
		if confirmResp.GetValue() {
			rangeChoices := []*azdext.SelectChoice{
				{Label: "Last Day", Value: "1"},
				{Label: "Last 7 Days", Value: "7"},
				{Label: "Last 30 Days", Value: "30"},
				{Label: "Last 90 Days", Value: "90"},
			}
			defaultRangeIdx := int32(1) // 7 days
			rangeResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:       "Select trace time range",
					Choices:       rangeChoices,
					SelectedIndex: &defaultRangeIdx,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for trace time range: %w", err)
			}
			days, _ := strconv.Atoi(rangeChoices[int(*rangeResp.Value)].Value)
			flags.traceDays = days
		}
	}

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
				Message:        "Max samples (between 15 and 1000)",
				DefaultValue:   strconv.Itoa(defaultEvalSamples),
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for max samples: %w", err)
		}
		if value := strings.TrimSpace(resp.Value); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 15 || parsed > 1000 {
				return fmt.Errorf("--max-samples must be between 15 and 1000")
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

	// Ask about evaluator.
	if len(existingCfg.Evaluators) > 0 {
		evalLabel := strings.Join(existingCfg.Evaluators.Names(), ", ")
		resp, err := prompt.Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("Existing evaluator: %s. Do you want to regenerate?", evalLabel),
				DefaultValue: new(false),
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for evaluator regeneration: %w", err)
		}
		if resp.Value != nil && *resp.Value {
			flags.regenerateEvaluator = true
		}
	} else {
		// No evaluators exist — generate one by default.
		flags.regenerateEvaluator = true
	}

	return nil
}

// promptConfigConfirmation shows the resolved instruction file from
// metadata.yaml and lets the user confirm or override it.
func promptConfigConfirmation(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	resolved *evalResolvedContext,
	flags *evalInitFlags,
) error {
	prompt := azdClient.Prompt()
	projectDir := resolved.agentProject

	// Instruction file.
	instrDefault := relativeDisplay(flags.instructionFile, projectDir)
	resp, err := prompt.Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Instruction file",
			DefaultValue:   instrDefault,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return fmt.Errorf("prompting for instruction file: %w", err)
	}
	if value := strings.TrimSpace(resp.Value); value != "" {
		if !filepath.IsAbs(value) && projectDir != "" {
			value = filepath.Join(projectDir, value)
		}
		if _, err := os.Stat(value); err != nil {
			return fmt.Errorf("instruction file %q is not accessible: %w", value, err)
		}
		flags.instructionFile = value
		flags.instruction = "" // file takes precedence
	}

	return nil
}
