// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// runInitManaged is the entry point for `azd ai agent init` when the user has
// selected the "prompt" (kind=managed) agent kind. It produces a first-class
// azd project so prompt agents follow the same `azd up` / `azd deploy`
// lifecycle as hosted agents:
//
//  1. Scaffolds (or reuses) an azd project + infra via ensureProject — the
//     same azd-ai-starter-basic template the hosted flow uses.
//  2. Writes an agent.yaml (kind=managed) into the service directory.
//  3. Adds an azure.yaml service entry (Host=azure.ai.agent) whose config
//     carries the harness connection details in a promptAgent block.
//
// The harness create/invoke/delete then happen through the service-target
// provider during `azd deploy` / `azd up`, exactly like hosted agents — no
// bespoke standalone deploy command or sidecar config file.
func runInitManaged(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) error {
	// Prompt for the conceptual agent details first: name and description.
	agentName, err := promptManagedAgentName(ctx, azdClient, flags)
	if err != nil {
		return err
	}

	description, err := promptManagedAgentDescription(ctx, azdClient, flags)
	if err != nil {
		return err
	}

	// The harness base URL is where the agent runtime lives (env-overridable).
	// Independently of that, the prompt-agent init experience mirrors hosted:
	// in interactive mode we always walk subscription -> Foundry project ->
	// model so the workspace tuple and model endpoint come from a real project.
	// --no-prompt skips the interactive Azure resolution and uses flags/env.
	settings := project.DefaultPromptAgentSettings()
	if envBaseURL := strings.TrimSpace(os.Getenv(project.PromptBaseURLEnvVar)); envBaseURL != "" {
		settings.BaseURL = envBaseURL
	}
	useGuidedFoundry := !flags.noPrompt

	// Decide where the project lives and where the agent.yaml goes within it.
	// When an azd project already exists in the cwd we add the agent as a new
	// service in a subfolder; otherwise we scaffold a brand-new project folder
	// named after the agent and place agent.yaml at its root.
	existingProject := fileExists("azure.yaml")
	folderName := sanitizeAgentName(agentName)
	if folderName == "" || folderName == "." || folderName == ".." || strings.ContainsAny(folderName, `/\`) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("cannot derive a safe folder name from agent name %q", agentName),
			"choose an agent name that contains alphanumerics or hyphens",
		)
	}

	var projectTargetDir, serviceRelPath string
	if existingProject {
		projectTargetDir = "."
		serviceRelPath = folderName
	} else {
		projectTargetDir = folderName
		serviceRelPath = "."
	}

	// Scaffold or locate the azd project + infra. On a fresh scaffold this
	// downloads the starter template and chdirs into the new project folder.
	if _, err := ensureProject(ctx, flags, azdClient, projectTargetDir); err != nil {
		return err
	}

	// Ensure an azd environment exists so `azd up`/`azd deploy` (and the
	// guided Azure resolution below) have one to read/write.
	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		env, err = createNewEnvironment(ctx, azdClient, flags.env)
		if err != nil {
			return err
		}
	}

	// Resolve the model deployment. The guided path walks subscription ->
	// Foundry project -> model (version/SKU/capacity/name) and returns a full
	// deployment to provision and reference; otherwise we use the curated/custom
	// model prompt (or --model in --no-prompt mode).
	var (
		model      string
		deployment *project.Deployment
	)
	if useGuidedFoundry {
		deployment, err = resolvePromptHarnessTarget(ctx, azdClient, flags, env, &settings)
		if err != nil {
			return err
		}
		if deployment != nil {
			model = deployment.Name
		}
	}
	if strings.TrimSpace(model) == "" {
		model, err = promptManagedAgentModel(ctx, azdClient, flags)
		if err != nil {
			return err
		}
	}

	instructions, err := promptManagedAgentInstructions(ctx, azdClient, flags)
	if err != nil {
		return err
	}

	// cwd is now the project root. Create the service directory when nested.
	if serviceRelPath != "." {
		if err := os.MkdirAll(serviceRelPath, osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("creating service folder %q: %w", serviceRelPath, err)
		}
	}

	promptAgent := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Name: agentName,
			Kind: agent_yaml.AgentKindPrompt,
		},
		Model: model,
		// Instructions are written to a sibling instructions.md by the
		// convention scaffolding below, so they are omitted inline here. The
		// deploy engine reads instructions.md when no inline value is present.
	}
	if strings.TrimSpace(description) != "" {
		desc := strings.TrimSpace(description)
		promptAgent.AgentDefinition.Description = &desc
	}
	if err := writePromptAgentYAML(serviceRelPath, &promptAgent); err != nil {
		return err
	}

	// Scaffold the convention-based authoring layout (instructions.md + an
	// empty skills/ folder) so the deploy engine's folder conventions are
	// discoverable from a fresh init.
	if err := scaffoldPromptConventionFolders(serviceRelPath, instructions); err != nil {
		return err
	}

	if err := addPromptAgentService(ctx, azdClient, agentName, serviceRelPath, &settings, deployment); err != nil {
		return err
	}

	// Persist the deployment name (matching hosted) so other commands can
	// resolve the model deployment from the azd environment.
	if deployment != nil {
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", deployment.Name); err != nil {
			return err
		}
	}

	printManagedInitSummary(agentName, model, serviceRelPath, projectTargetDir, existingProject, &settings)
	return nil
}

// addPromptAgentService registers the prompt agent as an azure.yaml service
// entry with Host=azure.ai.agent and a promptAgent config block. Unlike hosted
// agents there is no Docker/Language — the harness owns the runtime. When a
// resolved model deployment is supplied it is recorded under the service config
// so `azd provision` creates it (via AI_PROJECT_DEPLOYMENTS), mirroring hosted.
func addPromptAgentService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentName, serviceRelPath string,
	settings *project.PromptAgentSettings,
	deployment *project.Deployment,
) error {
	agentConfig := project.ServiceTargetAgentConfig{
		PromptAgent: settings,
	}
	if deployment != nil {
		agentConfig.Deployments = []project.Deployment{*deployment}
	}
	configStruct, err := project.MarshalStruct(&agentConfig)
	if err != nil {
		return fmt.Errorf("marshaling prompt agent service config: %w", err)
	}

	req := &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:         agentName,
			RelativePath: serviceRelPath,
			Host:         AiAgentHost,
			Config:       configStruct,
		},
	}
	if _, err := azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding prompt agent service to project: %w", err)
	}
	return nil
}

// promptManagedAgentName asks for the agent's name. The name is the Foundry
// agent identity and (for a fresh project) the project folder name. It matches
// the hosted flow's message, help text, and validation so the two flows feel
// the same.
func promptManagedAgentName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
) (string, error) {
	if strings.TrimSpace(flags.agentName) != "" {
		return validateInitAgentName(flags.agentName)
	}
	if flags.noPrompt {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-name is required in non-interactive mode for prompt agents",
			"pass --agent-name <name> on the command line",
		)
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter a name for your agent",
			DefaultValue: "my-prompt-agent",
			HelpMessage: "Foundry agents are unique by name within a project. " +
				"Reusing a name creates a new version of the existing agent.",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("agent name prompt was cancelled")
		}
		return "", fmt.Errorf("prompting for agent name: %w", err)
	}
	name := strings.TrimSpace(resp.Value)
	if name == "" {
		name = "my-prompt-agent"
	}
	return validateInitAgentName(name)
}

// promptManagedAgentDescription asks for an optional human-readable
// description, mirroring the hosted flow. Blank is allowed. In --no-prompt
// mode the --description flag value (or empty) is used.
func promptManagedAgentDescription(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
) (string, error) {
	if strings.TrimSpace(flags.description) != "" {
		return strings.TrimSpace(flags.description), nil
	}
	if flags.noPrompt {
		return "", nil
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter a description for your agent (optional)",
			DefaultValue:   "",
			Required:       false,
			IgnoreHintKeys: true,
			HelpMessage:    "A short summary of what this agent does. Written to agent.yaml and shown in Foundry.",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("description prompt was cancelled")
		}
		return "", fmt.Errorf("prompting for description: %w", err)
	}
	return strings.TrimSpace(resp.Value), nil
}

// promptManagedAgentModelChoices is the curated list of common Foundry chat
// models offered in the guided model prompt. The first entry is the default
// selection. A final "custom" option lets the user enter any deployment name.
var promptManagedAgentModelChoices = []string{
	"gpt-4.1-mini",
	"gpt-4.1",
	"gpt-4.1-nano",
	"gpt-4o",
	"gpt-4o-mini",
	"o4-mini",
}

// promptManagedAgentModel asks which model deployment the agent should call.
// Unlike a bare text field, it offers a curated list of common models plus a
// "custom" escape hatch — a guided experience closer to the hosted model
// selection. The --model flag (or --no-prompt) bypasses the prompt.
func promptManagedAgentModel(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
) (string, error) {
	if strings.TrimSpace(flags.model) != "" {
		return strings.TrimSpace(flags.model), nil
	}
	if flags.noPrompt {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--model is required in non-interactive mode for prompt agents",
			"pass --model <deployment-name> on the command line",
		)
	}

	const customLabel = "Enter a custom model deployment name"
	choices := make([]*azdext.SelectChoice, 0, len(promptManagedAgentModelChoices)+1)
	for _, m := range promptManagedAgentModelChoices {
		choices = append(choices, &azdext.SelectChoice{Label: m, Value: m})
	}
	choices = append(choices, &azdext.SelectChoice{Label: customLabel, Value: customLabel})

	defaultIndex := int32(0)
	selectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select the model deployment your agent will call",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
			HelpMessage: "The name of a model deployment in your Foundry project. " +
				"Provision it with `azd up`, or pick an existing deployment name.",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("model selection was cancelled")
		}
		return "", fmt.Errorf("prompting for model: %w", err)
	}

	selected := choices[*selectResp.Value].Value
	if selected != customLabel {
		return selected, nil
	}

	// Custom path: free-text deployment name.
	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter the model deployment name",
			DefaultValue: "gpt-4.1-mini",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("model selection was cancelled")
		}
		return "", fmt.Errorf("prompting for model: %w", err)
	}
	model := strings.TrimSpace(resp.Value)
	if model == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"model must not be empty",
			"provide a non-empty model deployment name",
		)
	}
	return model, nil
}

// promptManagedAgentInstructions asks for the agent's system instructions.
// In no-prompt mode it returns a stub the user can edit later.
func promptManagedAgentInstructions(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
) (string, error) {
	if flags.noPrompt {
		return "You are a helpful AI assistant. Replace these instructions before deploying.", nil
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter system instructions for your agent",
			DefaultValue: "You are a helpful AI assistant.",
			HelpMessage:  "The system/developer message inserted into the model context before every turn.",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("instructions input was cancelled")
		}
		return "", fmt.Errorf("prompting for instructions: %w", err)
	}
	instructions := strings.TrimSpace(resp.Value)
	if instructions == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"instructions must not be empty",
			"provide non-empty system instructions for the agent",
		)
	}
	return instructions, nil
}

// writePromptAgentYAML serializes the PromptAgent and writes it to
// <targetDir>/agent.yaml. A schema annotation comment is prepended for editor
// validation parity with the hosted agent flow.
func writePromptAgentYAML(targetDir string, promptAgent *agent_yaml.PromptAgent) error {
	content, err := yaml.Marshal(promptAgent)
	if err != nil {
		return fmt.Errorf("marshaling prompt agent to YAML: %w", err)
	}

	annotation := "# yaml-language-server: " +
		"$schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/PromptAgent.yaml"
	buf := bytes.NewBufferString(annotation + "\n\n")
	if _, err := buf.Write(content); err != nil {
		return fmt.Errorf("preparing agent.yaml file contents: %w", err)
	}

	filePath := filepath.Join(targetDir, "agent.yaml")
	if err := os.WriteFile(filePath, buf.Bytes(), osutil.PermissionFile); err != nil {
		return fmt.Errorf("saving file to %s: %w", filePath, err)
	}
	log.Printf("Wrote prompt agent.yaml at %s", filePath)
	return nil
}

// scaffoldPromptConventionFolders writes the convention-based authoring layout
// next to agent.yaml so the deploy engine's folder conventions are discoverable
// from a fresh init:
//
//   - instructions.md — the agent's instructions (deploy uses this when the
//     agent.yaml has no inline instructions).
//   - skills/         — add one subfolder per skill (each with a SKILL.md).
//
// The empty folders are kept with a .gitkeep placeholder. The deploy scanners
// ignore dotfiles, so .gitkeep never contributes content. An existing
// instructions.md is never overwritten so re-running init preserves edits.
func scaffoldPromptConventionFolders(targetDir, instructions string) error {
	if strings.TrimSpace(instructions) == "" {
		instructions = "You are a helpful AI assistant."
	}

	instructionsPath := filepath.Join(targetDir, "instructions.md")
	if !fileExists(instructionsPath) {
		content := strings.TrimRight(instructions, "\n") + "\n"
		if err := os.WriteFile(instructionsPath, []byte(content), osutil.PermissionFile); err != nil {
			return fmt.Errorf("writing instructions.md: %w", err)
		}
		log.Printf("Wrote instructions.md at %s", instructionsPath)
	}

	for _, sub := range []string{"skills"} {
		dir := filepath.Join(targetDir, sub)
		if err := os.MkdirAll(dir, osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("creating %s folder: %w", sub, err)
		}
		keep := filepath.Join(dir, ".gitkeep")
		if !fileExists(keep) {
			if err := os.WriteFile(keep, []byte{}, osutil.PermissionFile); err != nil {
				return fmt.Errorf("writing %s/.gitkeep: %w", sub, err)
			}
		}
	}
	return nil
}

// printManagedInitSummary prints a concise summary plus next-step hint.
func printManagedInitSummary(
	agentName, model, serviceRelPath, projectTargetDir string,
	existingProject bool,
	settings *project.PromptAgentSettings,
) {
	color.Green("\nInitialized prompt agent %q.", agentName)

	agentFile := "agent.yaml"
	if serviceRelPath != "." {
		agentFile = filepath.ToSlash(filepath.Join(serviceRelPath, "agent.yaml"))
	}
	fmt.Printf("  Agent file:    %s\n", agentFile)
	fmt.Printf("  Model:         %s\n", model)
	fmt.Printf("  Service entry: added to azure.yaml (host: %s)\n", AiAgentHost)
	fmt.Printf("  Harness URL:   %s\n", settings.BaseURL)
	// Surface the resolved Foundry target when it isn't the local-dev default
	// (i.e. the guided subscription -> project -> model path ran).
	if settings.Workspace != project.DefaultPromptWorkspace {
		fmt.Printf("  Workspace:     %s\n", settings.Workspace)
	}
	if settings.ModelEndpoint != "" && settings.ModelEndpoint != project.DefaultPromptModelEndpoint {
		fmt.Printf("  Model endpoint: %s\n", settings.ModelEndpoint)
	}

	// Point at the convention-based authoring layout the scaffold created.
	dirPrefix := ""
	if serviceRelPath != "." {
		dirPrefix = filepath.ToSlash(serviceRelPath) + "/"
	}
	fmt.Println()
	fmt.Println("Authoring layout (edit these to add capabilities):")
	fmt.Printf("  %sinstructions.md  the agent's instructions\n", dirPrefix)
	fmt.Printf("  %sskills/          add a subfolder per skill (each with a SKILL.md)\n", dirPrefix)

	fmt.Println()
	fmt.Println("Next steps:")
	if !existingProject && projectTargetDir != "." {
		fmt.Printf("  cd %q\n", projectTargetDir)
	}
	fmt.Println("  # Provision infrastructure and deploy the agent")
	fmt.Println("  azd up")
	fmt.Println("  # Or, once provisioned, just (re)deploy the agent")
	fmt.Println("  azd deploy")
	fmt.Println("  # Invoke it")
	fmt.Println("  azd ai agent invoke \"hello\"")
}
