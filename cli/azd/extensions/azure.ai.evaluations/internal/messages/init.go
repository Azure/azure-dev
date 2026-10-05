// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"

	"azureaieval/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// InitFlagConflict reports explicit inputs that cannot be honored together.
func InitFlagConflict(flag, requirement string) error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		fmt.Sprintf("--%s %s", flag, requirement),
		"Remove the conflicting flag, or select a compatible source and conversation mode.")
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

// SimulationModelConnectionPrompt asks which eligible project connection the simulated user should use.
func SimulationModelConnectionPrompt() string { return "Simulation model connection" }

// SimulationModelConnectionRequired refuses an invalid selection response.
func SimulationModelConnectionRequired() error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		"A simulation model connection is required",
		"Select an Azure OpenAI connection from the Foundry project.")
}

// SimulationModelDeploymentPrompt asks for the deployment on the selected connection.
func SimulationModelDeploymentPrompt() string { return "Simulation model deployment" }

// SimulationModelDeploymentHelp distinguishes simulation from grading.
func SimulationModelDeploymentHelp() string {
	return "Name the model deployment used by the simulated user. This is separate from --judge-model."
}

// SimulationModelDeploymentRequired refuses an empty deployment.
func SimulationModelDeploymentRequired() error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		"A simulation model deployment is required",
		"Enter the model deployment used by the simulated user.")
}

// NoEligibleSimulationModelConnections reports that discovery produced no usable model connection.
func NoEligibleSimulationModelConnections() error {
	return exterrors.Dependency(exterrors.CodeMissingModelConnection,
		"The Foundry project has no eligible Azure OpenAI model connections",
		"Create an Azure OpenAI connection in the Foundry project, then retry init.")
}

// SimulationModelConnectionNotFound rejects an explicit reference before authoring files.
func SimulationModelConnectionNotFound(name string) error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		fmt.Sprintf("Simulation model connection %q was not found in the Foundry project", name),
		"Use an Azure OpenAI connection listed in the Foundry project.")
}

// SimulationModelConnectionWrongKind rejects a connection that cannot serve a model deployment.
func SimulationModelConnectionWrongKind(name, kind string) error {
	if kind == "" {
		kind = "unknown"
	}
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		fmt.Sprintf("Simulation model connection %q has type %q; expected AzureOpenAI", name, kind),
		"Use an Azure OpenAI connection listed in the Foundry project.")
}

// ListingSimulationModelConnections adds operation context without treating a failed listing as empty.
func ListingSimulationModelConnections(err error) error {
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return err
	}
	if exterrors.IsCancellation(err) {
		return exterrors.Cancelled("Listing Foundry project connections was cancelled")
	}
	return exterrors.Internal(exterrors.CodeConnectionCatalogFailed,
		fmt.Sprintf("listing Foundry project connections for --simulation-model: %v", err))
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
