// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"fmt"

	"azureaieval/internal/exterrors"
)

// InitFlagConflict reports explicit inputs that cannot be honored together.
func InitFlagConflict(flag, requirement string) error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		fmt.Sprintf("--%s %s", flag, requirement),
		"Remove the conflicting flag, or select a compatible source and conversation mode.")
}

// InitDatasetFileConflict refuses a file that would be ignored by add-only authoring.
func InitDatasetFileConflict(name, path string) error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		fmt.Sprintf("Dataset %q is already declared with a different file or no local file; "+
			"%q would reuse that name. Choose a file with a different filename stem to add this dataset.", name, path),
		"Init never replaces dataset declarations. To reuse the existing dataset, supply its name or its current file path.")
}

// InitFlagRange names both the input and its supported bounds.
func InitFlagRange(flag string, value, minimum, maximum int) error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		fmt.Sprintf("--%s is %d; it accepts %d to %d", flag, value, minimum, maximum),
		"Choose a value in the supported range, or omit the flag for the default.")
}

// ConversationModeNotAChoice reports an unsupported authoring mode.
func ConversationModeNotAChoice(value string) error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		fmt.Sprintf("--conversation-mode %q is not supported; choose static or simulation", value),
		"Use static for completed messages, or simulation for scenario seeds and an agent target.")
}

// SelectConversationModePrompt asks whether the conversations already exist.
func SelectConversationModePrompt() string { return "How should the conversations be evaluated?" }

// ConversationModeChoices explains the difference before any files are written.
func ConversationModeChoices() []string {
	return []string{
		"Static      Score completed messages without invoking an agent",
		"Simulation  Generate conversations from scenario seeds against an agent",
	}
}

// SimulationModelRequired names the independent model choice needed for simulation.
func SimulationModelRequired() error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		"--simulation-model is required for --conversation-mode simulation",
		"Name a deployed model for the simulated user, independently of --judge-model and the generation model.")
}

// SimulationModelPrompt asks for the simulated user's deployment, without guessing.
func SimulationModelPrompt() string { return "Simulation model deployment" }

// SimulationModelHelp explains why the generation or judge model is not a default.
func SimulationModelHelp() string {
	return "Name a deployed model for the simulated user. This is separate from the generation model and --judge-model."
}

// JudgeModelPrompt asks for a deployment when local configuration has none.
func JudgeModelPrompt() string { return "Judge model deployment" }

// JudgeModelHelp distinguishes grading from conversation generation.
func JudgeModelHelp() string { return "Name a deployed model for the evaluators to judge with." }

// HandoffEvaluatorIncompatible explains why a generated rubric is not in the next command.
func HandoffEvaluatorIncompatible(name string) string {
	return fmt.Sprintf("  warning: evaluator %q does not support the generated dataset's evaluation level. "+
		"It remains in the catalogue; the init command uses builtin.task_completion instead.\n", name)
}

// InitHandoffGuidance distinguishes the interactive next command from unattended use.
func InitHandoffGuidance(simulation bool) string {
	modelFlags := "--judge-model <judge-deployment>"
	if simulation {
		modelFlags += " --simulation-model <simulation-deployment>"
	}
	return "  Run this init command interactively to resolve missing inputs.\n" +
		"  For unattended use, add --no-prompt " + modelFlags +
		". Choose these deployments independently of --generation-model.\n"
}
