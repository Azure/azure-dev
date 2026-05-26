// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_prompts.go contains interactive resolution functions for the
// optimize command: system prompt, skill directory, config confirmation,
// and target model selection.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// resolveOptimizeSystemPrompt resolves the agent's system prompt:
//
//  1. Config dir pointer (agent.config): instruction from metadata.yaml (already resolved).
//  2. Config (eval.yaml / --config): inline instruction or file reference.
//  3. Interactive prompt: ask the user to provide inline text or a file path.
//
// Relative file paths are resolved against agentProject.
func resolveOptimizeSystemPrompt(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
	hasProject bool,
	noPrompt bool,
) error {
	// Resolve relative instruction file paths against the agent project directory.
	if cfg.Agent.Instruction.File != "" && hasProject && !filepath.IsAbs(cfg.Agent.Instruction.File) {
		cfg.Agent.Instruction.File = filepath.Join(agentProject, cfg.Agent.Instruction.File)
	}

	// Step 1: Config explicitly declares a file reference — validate it's readable.
	if cfg.Agent.Instruction.File != "" {
		if _, err := os.Stat(cfg.Agent.Instruction.File); err != nil {
			return fmt.Errorf("instruction file %q from config is not accessible: %w",
				cfg.Agent.Instruction.File, err)
		}
		return nil
	}

	// Step 1b: Config already has inline instruction — nothing to do.
	if cfg.Agent.Instruction.Value != "" {
		return nil
	}

	// Step 2: Interactive prompt — ask user to provide inline text or a file path.
	if noPrompt {
		return fmt.Errorf("instruction is required for optimization.\n\n" +
			"Provide it via one of:\n" +
			"  1. Set agent.config in eval.yaml to point to a config dir with metadata.yaml\n" +
			"  2. Set instruction in eval.yaml (agent section): inline string or file reference\n" +
			"  3. Run without --no-prompt to enter it interactively")
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return fmt.Errorf("instruction is required but could not open interactive prompt: %w", clientErr)
	}
	defer azdClient.Close()

	inputChoices := []*azdext.SelectChoice{
		{Label: "Type inline", Value: "inline"},
		{Label: "Load from file", Value: "file"},
	}
	defaultIdx := int32(0)
	selResp, selErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "No instruction found in config or baseline. " +
				"How would you like to provide it?",
			Choices:       inputChoices,
			SelectedIndex: &defaultIdx,
		},
	})
	if selErr != nil {
		return fmt.Errorf("prompting for instruction input method: %w", selErr)
	}

	if inputChoices[int(*selResp.Value)].Value == "file" {
		pathResp, pathErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Path to instruction file",
				IgnoreHintKeys: true,
			},
		})
		if pathErr != nil {
			return fmt.Errorf("prompting for instruction file path: %w", pathErr)
		}
		filePath := strings.TrimSpace(pathResp.Value)
		// Resolve relative paths against the agent project directory.
		if !filepath.IsAbs(filePath) && hasProject {
			filePath = filepath.Join(agentProject, filePath)
		}
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("instruction file %q is not accessible: %w", filePath, err)
		}
		cfg.Agent.Instruction.File = filePath
	} else {
		resp, promptErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter the agent's instruction",
				IgnoreHintKeys: true,
			},
		})
		if promptErr != nil {
			return fmt.Errorf("prompting for instruction: %w", promptErr)
		}
		cfg.Agent.Instruction.Value = strings.TrimSpace(resp.Value)
	}

	return nil
}

// resolveOptimizeSkillDir resolves the agent's skill directory:
//  1. Config dir pointer (agent.config): skill_dir from metadata.yaml (already resolved).
//  2. Auto-detect: look for a "skills/" folder in the agent project — confirm with user.
//  3. Interactive prompt: ask the user to provide a path or skip.
func resolveOptimizeSkillDir(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
	noPrompt bool,
) error {
	// Step 1: Auto-detect common skill directory names.
	var detectedDir string
	for _, candidate := range []string{"skills", "skill"} {
		dir := filepath.Join(agentProject, candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			detectedDir = dir
			break
		}
	}

	if noPrompt {
		// In no-prompt mode, use whatever was detected (may be empty).
		cfg.SkillDir = detectedDir
		return nil
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		cfg.SkillDir = detectedDir
		return nil
	}
	defer azdClient.Close()

	if detectedDir != "" {
		// Found a skill directory — ask user to confirm or provide a different one.
		choices := []*azdext.SelectChoice{
			{Label: fmt.Sprintf("Use detected: %s", detectedDir), Value: "use"},
			{Label: "Provide a different path", Value: "other"},
			{Label: "Skip (no skills)", Value: "skip"},
		}
		defaultIdx := int32(0)
		selResp, selErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       fmt.Sprintf("Found skills directory: %s", detectedDir),
				Choices:       choices,
				SelectedIndex: &defaultIdx,
			},
		})
		if selErr != nil {
			cfg.SkillDir = detectedDir
			return nil
		}

		switch choices[int(*selResp.Value)].Value {
		case "use":
			cfg.SkillDir = detectedDir
			return nil
		case "skip":
			return nil
		case "other":
			// Fall through to path prompt below.
		}
	} else {
		// No skill directory found — ask if they want to provide one.
		resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "No skills directory found. Would you like to provide one?",
				DefaultValue: new(bool), // default false
			},
		})
		if promptErr != nil || !resp.GetValue() {
			return nil // skip skills
		}
	}

	// Prompt for a custom path.
	pathResp, pathErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Path to skills directory",
			IgnoreHintKeys: true,
		},
	})
	if pathErr != nil {
		return fmt.Errorf("prompting for skills directory: %w", pathErr)
	}

	dir := strings.TrimSpace(pathResp.Value)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(agentProject, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("skills directory %q is not accessible or not a directory", dir)
	}

	cfg.SkillDir = dir
	return nil
}

// promptOptimizeConfigConfirmation shows the resolved values from the baseline
// config and lets the user confirm or override instruction file, skills
// directory, and tools file.
func promptOptimizeConfigConfirmation(ctx context.Context, cfg *OptimizeConfig, agentProject string) error {
	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return nil // non-fatal — skip confirmation prompts
	}
	defer azdClient.Close()
	prompt := azdClient.Prompt()

	// Instruction file.
	instrDefault := relativeDisplay(cfg.Agent.Instruction.File, agentProject)
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
		if !filepath.IsAbs(value) && agentProject != "" {
			value = filepath.Join(agentProject, value)
		}
		if _, err := os.Stat(value); err != nil {
			return fmt.Errorf("instruction file %q is not accessible: %w", value, err)
		}
		cfg.Agent.Instruction.File = value
		cfg.Agent.Instruction.Value = ""
	}

	// Skills directory.
	skillDefault := relativeDisplay(cfg.SkillDir, agentProject)
	resp, err = prompt.Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Skills directory (enter to skip)",
			DefaultValue:   skillDefault,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return fmt.Errorf("prompting for skills directory: %w", err)
	}
	if value := strings.TrimSpace(resp.Value); value != "" {
		if !filepath.IsAbs(value) && agentProject != "" {
			value = filepath.Join(agentProject, value)
		}
		cfg.SkillDir = value
	} else {
		cfg.SkillDir = ""
	}

	// Tools file.
	toolsDefault := relativeDisplay(cfg.ToolsFile, agentProject)
	resp, err = prompt.Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Tools file (enter to skip)",
			DefaultValue:   toolsDefault,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return fmt.Errorf("prompting for tools file: %w", err)
	}
	if value := strings.TrimSpace(resp.Value); value != "" {
		if !filepath.IsAbs(value) && agentProject != "" {
			value = filepath.Join(agentProject, value)
		}
		cfg.ToolsFile = value
	} else {
		cfg.ToolsFile = ""
	}

	return nil
}

// resolveOptimizeEvalModel prompts the user to select an evaluation model
// when --eval-model was not provided. In --no-prompt mode, returns an error.
func resolveOptimizeEvalModel(
	ctx context.Context,
	cfg *OptimizeConfig,
	noPrompt bool,
) error {
	if noPrompt {
		return fmt.Errorf("options.eval_model is required: use --eval-model <model> to specify the evaluation model")
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return fmt.Errorf("eval_model is required but cannot prompt: %w", clientErr)
	}
	defer azdClient.Close()

	deployedModel := getDeployedModelFromEnv(ctx, azdClient)

	selected, err := promptModelSelection(ctx, azdClient, "Select the model for evaluation", deployedModel)
	if err != nil {
		return err
	}

	cfg.Options.EvalModel = selected
	return nil
}

// resolveOptimizeDataset prompts the user to provide a dataset when none was
// specified via config or --dataset flag. In --no-prompt mode, returns an error.
func resolveOptimizeDataset(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
	noPrompt bool,
) error {
	if noPrompt {
		return fmt.Errorf(
			"a dataset is required: use --dataset <file-or-name>, or provide dataset_file / dataset_reference " +
				"in your config, or run 'azd ai agent eval init' to generate one")
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return fmt.Errorf("dataset is required but cannot prompt: %w", clientErr)
	}
	defer azdClient.Close()

	file, ref, err := promptDatasetSelection(ctx, azdClient, agentProject)
	if err != nil {
		return err
	}
	cfg.DatasetFile = file
	cfg.DatasetReference = ref
	return nil
}

// hasModelConfig reports whether OptimizationConfig contains a "model" entry.
func hasModelConfig(oc opt_eval.OptimizationConfig) bool {
	if oc == nil {
		return false
	}
	_, ok := oc["model"]
	return ok
}

// resolveOptimizeTargetModels prompts the user to select model candidates
// for optimization. Fetches actual deployments from the
// Foundry project and allows multi-select.
func resolveOptimizeTargetModels(
	ctx context.Context,
	cfg *OptimizeConfig,
) error {
	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return nil
	}
	defer azdClient.Close()

	currentModel := cfg.Agent.Model

	resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Would you like to specify target models for optimization?",
			DefaultValue: new(bool), // default false
		},
	})
	if promptErr != nil || !resp.GetValue() {
		return nil
	}

	// Fetch deployed models from the Foundry project.
	choices := buildOptimizeModelChoices(ctx, azdClient, currentModel)

	message := "Select target models for optimization"
	if currentModel != "" {
		message = fmt.Sprintf("Select target models for optimization (current: %s)", currentModel)
	}

	multiResp, multiErr := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: message,
			Choices: choices,
		},
	})
	if multiErr != nil {
		return fmt.Errorf("prompting for target models: %w", multiErr)
	}

	var models []string
	for _, v := range multiResp.Values {
		models = append(models, v.Value)
	}

	if len(models) > 0 {
		modelJSON, _ := json.Marshal(models)
		if cfg.Options.OptimizationConfig == nil {
			cfg.Options.OptimizationConfig = make(opt_eval.OptimizationConfig)
		}
		cfg.Options.OptimizationConfig["model"] = modelJSON
	}

	return nil
}

// recommendedOptimizationModels is the set of model names recommended as
// optimization models by the server (exact match, case-insensitive).
var recommendedOptimizationModels = []string{"gpt-5", "gpt-5.1", "gpt-5.2"}

// isRecommendedOptimizationModel checks whether a model name matches a
// recommended optimization model (exact match, case-insensitive).
func isRecommendedOptimizationModel(modelName string) bool {
	for _, rec := range recommendedOptimizationModels {
		if strings.EqualFold(modelName, rec) {
			return true
		}
	}
	return false
}

// resolveOptimizeOptimizationModel prompts the user to select an optimization
// model. The eval model is offered as default (Skip), followed by a "Select
// another deployment" option to browse all deployments. If the user picks a
// model not in the recommended set, a warning is printed.
func resolveOptimizeOptimizationModel(ctx context.Context, cfg *OptimizeConfig) error {
	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return nil
	}
	defer azdClient.Close()

	allowedList := strings.Join(recommendedOptimizationModels, ", ")

	var choices []*azdext.SelectChoice
	// First choice: skip (use the eval model).
	choices = append(choices, &azdext.SelectChoice{
		Label: fmt.Sprintf("Skip (use eval model: %s)", cfg.Options.EvalModel),
		Value: "",
	})
	// Second choice: browse deployments.
	choices = append(choices, &azdext.SelectChoice{
		Label: "Select another deployment",
		Value: selectOtherDeploymentValue,
	})

	message := fmt.Sprintf("Select an optimization model (recommended: %s)", allowedList)
	defaultIndex := int32(0)
	selectResp, selectErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       message,
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if selectErr != nil || selectResp.Value == nil {
		return nil
	}

	selected := choices[int(*selectResp.Value)].Value
	if selected == "" {
		// Skip — leave OptimizationModel empty (server uses eval model).
		return nil
	}

	if selected == selectOtherDeploymentValue {
		picked, err := promptAllDeployments(ctx, azdClient)
		if err != nil {
			return nil // no deployments — silently skip
		}
		selected = picked
	}

	// Warn if the selected deployment's model is not in the recommended set.
	deployments := listDeploymentsFromEnv(ctx, azdClient)
	for _, d := range deployments {
		if d.Name == selected && !isRecommendedOptimizationModel(d.ModelName) {
			fmt.Printf("%s deployment %q uses model %q which is not in the recommended "+
				"optimization model set (%s). The server may reject it.\n", color.YellowString("Warning:"), selected, d.ModelName, allowedList)
			break
		}
	}

	cfg.Options.OptimizationModel = selected
	return nil
}

// buildOptimizeModelChoices fetches Foundry project deployments and returns
// MultiSelectChoice items. The current deployed model is pre-selected.
// Falls back to an empty list if deployments cannot be fetched.
func buildOptimizeModelChoices(ctx context.Context, azdClient *azdext.AzdClient, currentModel string) []*azdext.MultiSelectChoice {
	deployments := listDeploymentsFromEnv(ctx, azdClient)

	var choices []*azdext.MultiSelectChoice
	seen := make(map[string]bool)

	// If current model is present in deployments, it will be marked below.
	// If not (and it's non-empty), prepend it as a pre-selected entry.
	if currentModel != "" {
		found := false
		for _, d := range deployments {
			if d.Name == currentModel {
				found = true
				break
			}
		}
		if !found {
			choices = append(choices, &azdext.MultiSelectChoice{
				Label:    currentModel + " (current)",
				Value:    currentModel,
				Selected: true,
			})
			seen[currentModel] = true
		}
	}

	for _, d := range deployments {
		if seen[d.Name] {
			continue
		}
		label := d.Name
		if d.ModelName != "" && d.ModelName != d.Name {
			label = fmt.Sprintf("%s (%s)", d.Name, d.ModelName)
		}
		selected := d.Name == currentModel
		if selected {
			label += " (current)"
		}
		choices = append(choices, &azdext.MultiSelectChoice{
			Label:    label,
			Value:    d.Name,
			Selected: selected,
		})
		seen[d.Name] = true
	}

	return choices
}

// listDeploymentsFromEnv reads AZURE_AI_PROJECT_ID from the azd environment
// and returns the Foundry project's model deployments. Returns nil on failure.
func listDeploymentsFromEnv(ctx context.Context, azdClient *azdext.AzdClient) []FoundryDeploymentInfo {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil || envResp.Environment == nil {
		return nil
	}

	v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil || v.Value == "" {
		return nil
	}

	project, err := extractProjectDetails(v.Value)
	if err != nil {
		return nil
	}

	cred, err := newAgentCredential()
	if err != nil {
		return nil
	}

	deployments, _ := listProjectDeployments(
		ctx, cred,
		project.SubscriptionId,
		project.ResourceGroupName,
		project.AccountName,
	)
	return deployments
}
