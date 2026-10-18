// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Mode identifies an agent-init selection that owns a distinct allowed-flag set.
type Mode string

const (
	ModeHostedFromCode Mode = "hosted-from-code"
	ModePrompt         Mode = "prompt"
	ModePromptVoice    Mode = "prompt-voice"
	ModeTemplateAdopt  Mode = "template-adopt"
	ModeImage          Mode = "image"
)

// FlagSpec describes an init flag and the modes that consume explicit values.
type FlagSpec struct {
	Name          string
	Help          string
	Default       string
	AllowedValues []string
	Modes         []Mode
}

// Contract is the single source of truth for init flags.
// Future reference-doc generation should use this contract.
func Contract() []FlagSpec {
	all := []Mode{ModeHostedFromCode, ModePrompt, ModePromptVoice, ModeTemplateAdopt, ModeImage}
	hosted := []Mode{ModeHostedFromCode, ModeTemplateAdopt, ModeImage}
	code := []Mode{ModeHostedFromCode, ModeTemplateAdopt}
	return []FlagSpec{
		{Name: "project-id", Modes: all,
			Help: "Existing Microsoft Foundry Project Id to initialize your azd environment with"},
		{Name: "acr-connection", Modes: code,
			Help: "Foundry Azure Container Registry connection name to use for an existing project; " +
				"incompatible with code deploy, --image, and prompt-voice agents"},
		{Name: "model-deployment", Modes: []Mode{ModeHostedFromCode, ModePrompt, ModeTemplateAdopt, ModeImage},
			Help: "Name of an existing model deployment to use from the Foundry project. " +
				"Only used when paired with an existing Foundry project, either via --project-id or interactive prompts"},
		{Name: "model", Modes: all,
			Help: fmt.Sprintf(
				"For hosted and prompt agents, name of the AI model to deploy. "+
					"Defaults to '%s' during interactive model selection; "+
					"required to deploy a new model with --no-prompt. If --model-deployment is also provided, "+
					"--model-deployment takes precedence. For new managed prompt voice agents, selects the "+
					"service-hosted model (default: gpt-realtime); no model deployment is created.",
				defaultAgentModel)},
		{Name: "template", Modes: []Mode{ModeTemplateAdopt},
			Help: "Path or supported GitHub URI to an azure.yaml project document"},
		{Name: "manifest", Modes: []Mode{ModeTemplateAdopt}, Help: "Deprecated alias for --template"},
		{Name: "agent-name", Modes: all,
			Help: "Foundry agent name to write to azure.yaml. " +
				"Reusing a name creates a new version of the existing agent."},
		{Name: "description", Modes: []Mode{ModePrompt},
			Help: "Prompt-agent description to write to azure.yaml. Used as the agent's human-readable summary."},
		{Name: "instructions", Modes: []Mode{ModePrompt},
			Help: "System instructions for a prompt agent, including one using --harness. " +
				"Written to azure.yaml; not supported for hosted agents."},
		{Name: "src", Modes: hosted,
			Help: "Source directory for hosted agents, or target directory when adopting an azure.yaml project"},
		{Name: "protocol", Modes: []Mode{ModeHostedFromCode, ModeImage},
			Help: fmt.Sprintf("Protocols supported by the agent (%s). Can be specified multiple times.", knownProtocolNames())},
		{Name: "deploy-mode", Modes: hosted, AllowedValues: []string{"container", "code"},
			Help: "Deployment mode: 'container' (Docker image) or 'code' (ZIP upload). " +
				"Defaults to 'code' for Python/.NET projects in --no-prompt."},
		{Name: "runtime", Modes: code,
			Help: "Runtime for code deploy (e.g., 'python_3_13', 'python_3_14', 'dotnet_10'). " +
				"Required with --deploy-mode code --no-prompt."},
		{Name: "entry-point", Modes: code,
			Help: "Entry point file for code deploy (e.g., 'app.py', 'MyAgent.dll'). " +
				"Required with --deploy-mode code --no-prompt."},
		{Name: "dep-resolution", Modes: code, AllowedValues: []string{"remote_build", "bundled"},
			Help: "Dependency resolution for code deploy: 'remote_build' or 'bundled'. Defaults to 'remote_build'."},
		{Name: "image", Modes: []Mode{ModeTemplateAdopt, ModeImage},
			Help: "Pre-built container image URL (e.g., 'myacr.azurecr.io/agent:v1'). " +
				"Skips template/language selection, code scaffolding, " +
				"Dockerfile-based source setup and build configuration, and ACR setup, and requires --agent-name. " +
				"Incompatible with --deploy-mode code."},
		{Name: "registry-connection", Modes: []Mode{ModeTemplateAdopt, ModeImage},
			Help: "Name or ID of an existing Foundry project connection used to pull a private pre-built container image. " +
				"Requires a pre-built image and is incompatible with code deploy."},
		{Name: "voice", Modes: []Mode{ModePromptVoice},
			Help: "Output voice for new prompt voice agents (--kind prompt-voice or the interactive voice option). " +
				"Rejected for other init flows. For existing voice services, edit azure.yaml. " +
				"Example: en-US-Ava:DragonHDLatestNeural."},
		{Name: "force", Modes: []Mode{ModeHostedFromCode, ModePromptVoice, ModeImage},
			Help: "Allow existing hosted or voice agent service configurations to be overwritten. " +
				"With --no-prompt, pre-consent when init requires overwrite confirmation."},
		{Name: "kind", Modes: []Mode{ModeHostedFromCode, ModePrompt, ModePromptVoice, ModeImage},
			AllowedValues: []string{"hosted", "prompt", "prompt-voice"},
			Help: "Agent runtime to initialize: 'hosted' (bring your own code/container), 'prompt' " +
				"(model + instructions; Foundry runs the agent), or 'prompt-voice' (a declarative " +
				"voice agent; use --model for the speech-to-speech model and --voice for the output " +
				"voice agent). When omitted, the hosted runtime is used. With --no-prompt, " +
				"'prompt' requires --agent-name and either --model or --model-deployment."},
		{Name: "harness", Modes: []Mode{ModePrompt}, AllowedValues: []string{"github_copilot_preview"},
			Help: "Optional execution harness for --kind prompt: 'github_copilot_preview' (GitHub Copilot Brain+Hand). " +
				"Requires a Foundry project in a supported harness region; see the extension README for supported regions."},
		{Name: "infra", Modes: []Mode{ModeHostedFromCode, ModePromptVoice, ModeTemplateAdopt, ModeImage},
			AllowedValues: []string{"bicep", "terraform"},
			Help: "Eject infrastructure-as-code from azure.yaml. Existing infrastructure is preserved and " +
				"Foundry files are generated as a separate infra/foundry layer. " +
				"A bare --infra ejects Bicep; --infra=terraform ejects Terraform and sets " +
				"the Foundry layer provider to terraform; Bicep keeps the microsoft.foundry provider. " +
				"--infra=bicep is explicit Bicep. " +
				"When azure.yaml already declares a Foundry project service, runs as a standalone " +
				"eject and skips the init prompts; otherwise init runs first and the eject follows it. " +
				"Not supported with --kind prompt."},
		{Name: "rai-policy", Modes: []Mode{ModePrompt},
			Help: "Responsible AI policy for a prompt or managed agent: 'none' to inherit the account's " +
				"default content filters, a policy name on the selected Foundry account, or a policy's " +
				"full ARM resource ID. The policy must already exist; azd attaches it, it does not " +
				"create it. When omitted, you are prompted to pick from the policies on the account; " +
				"with --no-prompt no policy is attached. " +
				"Explicit --rai-policy is rejected for hosted agents and when adopting an " +
				"azure.yaml project document or a full repository template; declare policies in azure.yaml instead."},
	}
}

// AllowedIn reports whether a flag is accepted in mode, including the deprecated manifest alias.
func AllowedIn(mode Mode, flagName string) bool {
	for _, spec := range Contract() {
		if spec.Name == flagName {
			return slices.Contains(spec.Modes, mode)
		}
	}
	return false
}

// RejectedFlags returns changed local flags not accepted in mode, in flag-name order.
func RejectedFlags(cmd *cobra.Command, mode Mode) []string {
	var rejected []string
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Changed {
			return
		}
		switch flag.Name {
		case "cwd", "environment", "env", "no-prompt", "output", "debug", "help", "docs",
			"trace-log-file", "trace-log-url":
			return
		}
		if !AllowedIn(mode, flag.Name) {
			rejected = append(rejected, "--"+flag.Name)
		}
	})
	return rejected
}

// ValidateModeFlags rejects explicitly supplied flags a mode cannot consume, even in interactive runs.
func ValidateModeFlags(cmd *cobra.Command, mode Mode) error {
	conflicts := RejectedFlags(cmd, mode)
	if len(conflicts) == 0 {
		return nil
	}
	message := fmt.Sprintf("%s init cannot apply these explicitly set inputs: ", mode)
	suggestion := "Remove the conflicting flags or select an init mode that supports them."
	switch mode {
	case ModePromptVoice:
		message = "new prompt voice agents cannot use these init inputs: "
		suggestion = "remove these inputs to create a prompt voice agent; use the hosted init flow for source/code settings " +
			"or the prompt init flow for prompt-only settings"
	case ModeTemplateAdopt:
		message = "azure.yaml project adoption cannot apply these explicitly set inputs: "
		suggestion = "Remove the conflicting flags or update the agent services in azure.yaml before running init."
	}
	return exterrors.Validation(
		exterrors.CodeConflictingArguments, message+strings.Join(conflicts, ", "), suggestion,
	)
}

// ApplyFlagHelp sets registered local flag usage from the contract, skipping missing flags.
func ApplyFlagHelp(cmd *cobra.Command) {
	for _, spec := range Contract() {
		if flag := cmd.LocalNonPersistentFlags().Lookup(spec.Name); flag != nil {
			flag.Usage = spec.Help
		}
	}
}

// ResolveModeFromFlags mirrors init's explicit selection priority before discovery sets defaults.
// templateSelected includes --template/--manifest presence and positional azure.yaml input.
func ResolveModeFromFlags(templateSelected bool, kind, image string) Mode {
	if templateSelected {
		return ModeTemplateAdopt
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "prompt-voice":
		return ModePromptVoice
	case "prompt":
		return ModePrompt
	}
	if image != "" {
		return ModeImage
	}
	return ModeHostedFromCode
}
