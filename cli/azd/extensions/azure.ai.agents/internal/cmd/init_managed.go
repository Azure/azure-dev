// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/http"
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

// promptAgentManifest is a prompt-agent definition supplied through
// `--manifest` (or a positional template pointer), pre-loaded so runInitManaged
// can seed the scaffold from it instead of prompting for each field.
//
// sourceDir is the directory the manifest was read from. When the manifest is
// local, a sibling instructions file is used as the agent's instructions, which
// keeps a template's authoring layout intact instead of collapsing it to the
// default stub.
type promptAgentManifest struct {
	definition agent_yaml.PromptAgent
	sourceDir  string
}

// agentName returns the manifest's agent name, trimmed. Empty when unset.
func (m *promptAgentManifest) agentName() string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.definition.Name)
}

// model returns the manifest's model deployment name, trimmed. Empty when unset.
func (m *promptAgentManifest) model() string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.definition.Model)
}

// description returns the manifest's description, trimmed. Empty when unset.
func (m *promptAgentManifest) description() string {
	if m == nil || m.definition.Description == nil {
		return ""
	}
	return strings.TrimSpace(*m.definition.Description)
}

// instructions returns the manifest's inline instructions.
func (m *promptAgentManifest) instructions() string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.definition.Instructions)
}

// looksLikePromptAgentManifest reports whether the given YAML content is a
// prompt-agent manifest (`kind: prompt`) rather than a hosted/workflow agent
// manifest or a unified azure.yaml.
//
// It deliberately inspects only the top-level `kind` so a manifest that is
// otherwise malformed still routes to the prompt flow and fails there with a
// prompt-specific error, instead of being silently handed to the hosted flow.
func looksLikePromptAgentManifest(content []byte) bool {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return false
	}
	kind, ok := top["kind"].(string)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(kind), string(agent_yaml.AgentKindPrompt))
}

// loadPromptAgentManifest parses prompt-agent manifest content into the seed
// runInitManaged scaffolds from. sourceDir is the directory the content came
// from and may be empty for a remote pointer.
func loadPromptAgentManifest(content []byte, sourceDir string) (*promptAgentManifest, error) {
	var definition agent_yaml.PromptAgent
	if err := yaml.Unmarshal(content, &definition); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("manifest is not a valid prompt agent: %s", err),
			"fix the manifest to match the prompt agent schema (kind: prompt, name, model)",
		)
	}
	if !strings.EqualFold(string(definition.Kind), string(agent_yaml.AgentKindPrompt)) {
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("manifest declares kind %q, expected prompt", definition.Kind),
			"use kind: prompt for prompt and managed agents",
		)
	}
	return &promptAgentManifest{definition: definition, sourceDir: sourceDir}, nil
}

// loadPromptManifestFromPointer inspects `--manifest` (or the positional
// template pointer it was resolved into) and returns the parsed prompt-agent
// manifest when it declares `kind: prompt`.
//
// It returns (nil, nil) when no pointer was supplied, when the pointer cannot
// be read, or when the content is not a prompt-agent manifest — all of which
// mean "not my flow", leaving the hosted and unified-azure.yaml paths to handle
// it exactly as before. Only a pointer that is unambiguously a prompt agent but
// fails to parse surfaces an error.
func loadPromptManifestFromPointer(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	httpClient *http.Client,
) (*promptAgentManifest, error) {
	pointer := strings.TrimSpace(flags.manifestPointer)
	if pointer == "" {
		return nil, nil
	}

	content, ok := readManifestContentForInitDetection(ctx, azdClient, pointer, httpClient)
	if !ok || !looksLikePromptAgentManifest(content) {
		return nil, nil
	}

	// A sibling instructions.md is only reachable for a local pointer; for a
	// remote one the manifest must carry its instructions inline.
	sourceDir := ""
	if isLocalFilePath(pointer) {
		if abs, err := filepath.Abs(pointer); err == nil {
			sourceDir = filepath.Dir(abs)
		}
	}

	return loadPromptAgentManifest(content, sourceDir)
}

// runInitManaged is the entry point for `azd ai agent init` when the user has
// selected the prompt agent kind. It produces a first-class azd project
// so prompt agents follow the same `azd up` / `azd deploy` lifecycle as hosted
// agents:
//
//  1. Scaffolds (or reuses) an azd project + infra via ensureProject — the
//     same azd-ai-starter-basic template the hosted flow uses.
//  2. Writes an agent.yaml (kind: prompt) into the service directory.
//  3. Adds an azure.yaml service entry (Host=azure.ai.agent) whose config
//     carries the harness connection details in a promptAgent block.
//
// The create/invoke/delete then happen through the service-target provider
// during `azd deploy` / `azd up`, exactly like hosted agents — no bespoke
// standalone deploy command or sidecar config file.
//
// harness selects the prompt agent flavor. An empty harness scaffolds a plain
// prompt agent that Foundry runs directly; a non-empty harness
// ("github_copilot_preview")
// scaffolds a managed agent whose Brain+Hand sandbox the platform provisions.
//
// manifest, when non-nil, seeds the agent name, description, model, and
// instructions from a supplied template so `--manifest` works for both prompt
// flavors. Explicit flags always win over manifest values.
func runInitManaged(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	harness string,
	manifest *promptAgentManifest,
) error {
	// Every prompt-agent init converges here — interactive picker, --kind prompt,
	// and manifest adoption alike — so this is the one place the preview notice
	// reaches all of them. Emitted before validation so it is seen even when the
	// run is about to fail on a missing --no-prompt input.
	warnPromptAgentPreview(os.Stdout)

	// Fail before anything is written when non-interactive mode is missing an
	// input that has no deterministic fallback. ensureProject below creates a
	// project folder and azd environment, so a late failure would strand a
	// half-scaffolded project with no services: entry.
	if err := validateManagedNoPromptInputs(flags, manifest); err != nil {
		return err
	}

	// Prompt for the conceptual agent details first: name and description.
	agentName, err := promptManagedAgentName(ctx, azdClient, flags, manifest, harness)
	if err != nil {
		return err
	}

	description, err := promptManagedAgentDescription(ctx, azdClient, flags, manifest)
	if err != nil {
		return err
	}

	// Treat a manifest's model as if it had been passed as --model so the whole
	// downstream resolution (catalog lookup, region availability, quota, SKU)
	// targets the template's model rather than the generic default.
	if strings.TrimSpace(flags.model) == "" && strings.TrimSpace(flags.modelDeployment) == "" {
		flags.model = manifest.model()
	}

	// The prompt-agent init experience mirrors hosted:
	// we always walk subscription -> Foundry project -> model so the workspace
	// tuple and model endpoint come from a real project. In --no-prompt the
	// same walk runs unattended, resolving each step from flags and the azd
	// environment (AZURE_SUBSCRIPTION_ID, AZURE_LOCATION, --project-id,
	// --model-deployment, --model) instead of prompting.
	settings := project.DefaultPromptAgentSettings()

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

	// Resolve the instructions before ensureProject changes the working
	// directory: a manifest-supplied instructions.md is read relative to the
	// manifest, which may be a path relative to the original cwd.
	instructions, err := promptManagedAgentInstructions(ctx, azdClient, flags, manifest)
	if err != nil {
		return err
	}

	// Scaffold or locate the azd project + infra. On a fresh scaffold this
	// downloads the starter template and changes into the new project folder.
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
	// deployment to provision and reference. It runs in both interactive and
	// non-interactive mode so the harness target is always configured; without
	// it a --no-prompt scaffold would carry only placeholder routing values and
	// `azd up` would fail to find a Foundry project.
	var model string
	deployment, foundryProject, credential, err := resolvePromptHarnessTarget(ctx, azdClient, flags, env, &settings)
	if err != nil {
		return err
	}
	if deployment != nil {
		model = deployment.Name
	}
	if strings.TrimSpace(model) == "" {
		model, err = promptManagedAgentModel(ctx, azdClient, flags, manifest)
		if err != nil {
			return err
		}
	}

	// Resolve guardrails against the same Foundry account the model was
	// resolved on, while its credential is still in hand. Nothing is written
	// yet: the selection is applied after the manifest carry-over below so an
	// authored policy set is never silently replaced.
	raiPolicy, err := resolvePromptRaiPolicy(ctx, azdClient, flags, manifest, foundryProject, credential)
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
		// A nil harness is omitted from azure.yaml entirely, which is what
		// distinguishes a plain prompt agent from a managed (harnessed) one.
		Harness: promptScaffoldHarness(harness, manifest),
		// Instructions are inline, matching the prompt-agent API schema.
		Instructions: promptScaffoldInstructions(instructions),
	}
	// Carry the authored parts of a supplied manifest through to the scaffold.
	// Tools, skills, connections, and the toolbox reference are the reason a
	// user supplies a template at all; dropping them would silently produce a
	// bare agent that does not match the template they asked for.
	//
	// displayName and metadata come along for the same reason: a hosted agent's
	// azure.yaml carries description and metadata.tags straight from its
	// template, and both reach the same CreateAgentRequest fields for a prompt
	// agent, so a prompt agent scaffolded from a template should not silently
	// lose the catalog labels the template author wrote.
	if manifest != nil {
		promptAgent.Skills = manifest.definition.Skills
		promptAgent.Tools = manifest.definition.Tools
		promptAgent.ToolChoice = manifest.definition.ToolChoice
		promptAgent.StructuredInputs = manifest.definition.StructuredInputs
		promptAgent.Policies = manifest.definition.Policies
		promptAgent.Connections = manifest.definition.Connections
		promptAgent.Toolbox = manifest.definition.Toolbox
		promptAgent.Memory = manifest.definition.Memory
		promptAgent.AgentDefinition.DisplayName = manifest.definition.DisplayName
		promptAgent.AgentDefinition.Metadata = manifest.definition.Metadata
	}
	if strings.TrimSpace(description) != "" {
		desc := strings.TrimSpace(description)
		promptAgent.AgentDefinition.Description = &desc
	}
	// Applied after the manifest carry-over so a manifest that declares its own
	// policies keeps them; resolvePromptRaiPolicy returns "not attached" in that
	// case, making this a no-op.
	if err := applyRaiPolicySelection(ctx, azdClient, env.Name, &promptAgent, raiPolicy); err != nil {
		return err
	}

	if err := addPromptAgentService(ctx, azdClient, agentName, serviceRelPath, &promptAgent); err != nil {
		return err
	}

	// Model deployments, connections and skills live on sibling Foundry
	// services, not on the agent service, so a prompt agent's azure.yaml has the
	// same shape as a hosted agent's and each host is owned by the extension
	// that implements it. emitResourceServices also wires the agent's uses: list
	// so `azd provision` creates the project (and its deployments) first and
	// `azd deploy` publishes the skills before the agent that references them.
	var deployments []project.Deployment
	if deployment != nil {
		deployments = []project.Deployment{*deployment}
	}
	resources, err := promptResourceServices(ctx, azdClient, &promptAgent, serviceRelPath)
	if err != nil {
		return err
	}
	resources.Deployments = deployments
	endpointRef, err := recordFoundryProjectEnv(ctx, azdClient, env.Name, foundryProject)
	if err != nil {
		return err
	}
	if _, err := emitResourceServices(
		ctx, azdClient, agentName,
		endpointRef,
		resources,
	); err != nil {
		return err
	}

	// Persist the deployment name (matching hosted) so other commands can
	// resolve the model deployment from the azd environment.
	if deployment != nil {
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", deployment.Name); err != nil {
			return err
		}
	}

	printManagedInitSummary(agentName, model, harness, serviceRelPath, projectTargetDir, existingProject, &settings)
	return nil
}

// addPromptAgentService registers the prompt agent as an azure.yaml service
// entry with Host=azure.ai.agent. Unlike hosted agents there is no
// Docker/Language — the harness owns the runtime.
//
// The agent definition is written inline as service-level properties, the same
// unified shape hosted and voice agents use, so the whole agent is authored in
// azure.yaml and `kind: prompt` on the entry is what identifies it. Deploy also
// accepts a definition behind a `$ref:` include; init does not scaffold one
// because a second file adds nothing when there is only one agent to describe.
//
// No promptAgent config block is written. Every value it used to carry —
// subscription, resource group, workspace, project endpoint — is recorded in
// the azd environment by `azd provision` and read from there at deploy time, so
// the block could only have held a copy of the environment or a set of ${VAR}
// references pointing back at it.
//
// Model deployments are deliberately NOT recorded here: they belong to the
// sibling azure.ai.project service that emitResourceServices writes, the
// same shape hosted agents use.
func addPromptAgentService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentName, serviceRelPath string,
	promptAgent *agent_yaml.PromptAgent,
) error {
	agentProps, err := project.PromptAgentDefinitionToServiceProperties(*promptAgent)
	if err != nil {
		return err
	}

	req := &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:                 agentName,
			RelativePath:         serviceRelPath,
			Host:                 AiAgentHost,
			AdditionalProperties: agentProps,
		},
	}
	if _, err := azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding prompt agent service to project: %w", err)
	}
	return nil
}

// validateManagedNoPromptInputs rejects a non-interactive invocation that is
// missing an input with no deterministic fallback, before runInitManaged writes
// anything to disk.
//
// The individual prompt helpers below also guard on flags.noPrompt, but they run
// at different points in the flow — the model resolution in particular happens
// after ensureProject has already created a project folder and azd environment.
// Checking everything up front keeps a failed --no-prompt init from leaving a
// partially scaffolded project behind.
func validateManagedNoPromptInputs(flags *initFlags, manifest *promptAgentManifest) error {
	if !flags.noPrompt {
		return nil
	}
	if strings.TrimSpace(flags.agentName) == "" && manifest.agentName() == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-name is required in non-interactive mode for prompt agents",
			"pass --agent-name <name>, or supply a manifest with --manifest that declares name:",
		)
	}
	if strings.TrimSpace(flags.model) == "" &&
		strings.TrimSpace(flags.modelDeployment) == "" &&
		manifest.model() == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--model or --model-deployment is required in non-interactive mode for prompt agents",
			"pass --model <model-name> to deploy a new model, --model-deployment <name> to reuse an "+
				"existing deployment, or supply a manifest with --manifest that declares model:",
		)
	}
	return nil
}

// defaultPromptAgentName returns the suggested agent name for the flavor being
// scaffolded. The two flavors get distinct defaults because they produce
// different projects: accepting the default twice in the same folder would
// otherwise collide, and the name is also the Foundry agent identity, where a
// reused name silently creates a new version of the existing agent.
func defaultPromptAgentName(harness string) string {
	if strings.TrimSpace(harness) != "" {
		return "my-copilot-agent"
	}
	return "my-prompt-agent"
}

// promptManagedAgentName asks for the agent's name. The name is the Foundry
// agent identity and (for a fresh project) the project folder name. It matches
// the hosted flow's message, help text, and validation so the two flows feel
// the same. A manifest-supplied name seeds the interactive default and is used
// outright when --agent-name is absent in non-interactive mode.
func promptManagedAgentName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifest *promptAgentManifest,
	harness string,
) (string, error) {
	if strings.TrimSpace(flags.agentName) != "" {
		return validateInitAgentName(flags.agentName)
	}
	defaultName := manifest.agentName()
	if flags.noPrompt {
		if defaultName != "" {
			return validateInitAgentName(defaultName)
		}
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-name is required in non-interactive mode for prompt agents",
			"pass --agent-name <name>, or supply a manifest with --manifest that declares name:",
		)
	}
	if defaultName == "" {
		defaultName = defaultPromptAgentName(harness)
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter a name for your agent",
			DefaultValue: defaultName,
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
		name = defaultName
	}
	return validateInitAgentName(name)
}

// promptManagedAgentDescription asks for an optional human-readable
// description, mirroring the hosted flow. Blank is allowed. In --no-prompt
// mode the --description flag value (or the manifest's, or empty) is used.
func promptManagedAgentDescription(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifest *promptAgentManifest,
) (string, error) {
	if strings.TrimSpace(flags.description) != "" {
		return strings.TrimSpace(flags.description), nil
	}
	defaultDescription := manifest.description()
	if flags.noPrompt {
		return defaultDescription, nil
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter a description for your agent (optional)",
			DefaultValue:   defaultDescription,
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
// selection. --model-deployment, --model, a manifest model, or --no-prompt all
// bypass the prompt.
//
// This is only reached when the guided Foundry resolution did not produce a
// deployment (for example when the target project could not be resolved), so it
// records the model name without provisioning anything.
func promptManagedAgentModel(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifest *promptAgentManifest,
) (string, error) {
	if strings.TrimSpace(flags.modelDeployment) != "" {
		return strings.TrimSpace(flags.modelDeployment), nil
	}
	if strings.TrimSpace(flags.model) != "" {
		return strings.TrimSpace(flags.model), nil
	}
	if manifestModel := manifest.model(); manifestModel != "" {
		return manifestModel, nil
	}
	if flags.noPrompt {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--model or --model-deployment is required in non-interactive mode for prompt agents",
			"pass --model <model-name> or --model-deployment <deployment-name> on the command line",
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
// A manifest's instructions (inline, or a sibling instructions.md) are used
// verbatim — a template author already wrote them, so re-prompting would only
// invite the user to overwrite them by accident. Otherwise, in no-prompt mode
// it returns a stub the user can edit later.
func promptManagedAgentInstructions(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifest *promptAgentManifest,
) (string, error) {
	if instructions := strings.TrimSpace(flags.instructions); instructions != "" {
		return instructions, nil
	}
	if manifestInstructions := manifest.instructions(); manifestInstructions != "" {
		return manifestInstructions, nil
	}
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

// promptScaffoldInstructions returns the instructions to write inline into a
// scaffolded agent definition, falling back to a neutral default so a freshly
// initialized agent is deployable without editing.
func promptScaffoldInstructions(instructions string) string {
	if trimmed := strings.TrimSpace(instructions); trimmed != "" {
		return trimmed
	}
	return "You are a helpful AI assistant."
}

// promptScaffoldHarness builds the `harness:` block for a scaffolded agent.yaml,
// or nil for a plain prompt agent so the key is omitted entirely.
//
// harnessType is already resolved from --harness and --kind, so it wins over the
// manifest's own type. The manifest's remaining harness configuration — pinned
// skills, sandbox sizing, built-in capability filter — is carried through, since
// dropping it would scaffold an agent that does not match the template the user
// asked for.
func promptScaffoldHarness(harnessType string, manifest *promptAgentManifest) *agent_yaml.PromptHarness {
	harness := agent_yaml.NewPromptHarness(harnessType)
	if harness == nil {
		return nil
	}
	if manifest != nil && manifest.definition.Harness != nil {
		harness.Skills = manifest.definition.Harness.Skills
		harness.Environment = manifest.definition.Harness.Environment
		harness.BuiltinTools = manifest.definition.Harness.BuiltinTools
	}
	return harness
}

// printManagedInitSummary prints a concise summary plus next-step hint.
func printManagedInitSummary(
	agentName, model, harness, serviceRelPath, projectTargetDir string,
	existingProject bool,
	settings *project.PromptAgentSettings,
) {
	color.Green("\nInitialized prompt agent %q.", agentName)

	fmt.Println("  Agent config:  azure.yaml")
	fmt.Printf("  Model:         %s\n", model)
	fmt.Printf("  Service entry: added to azure.yaml (host: %s)\n", AiAgentHost)
	if harness != "" {
		fmt.Printf("  Harness:       %s\n", harness)
	}
	if settings.ProjectEndpoint != "" {
		fmt.Printf("  Project:       %s\n", settings.ProjectEndpoint)
	}
	if settings.ModelEndpoint != "" && settings.ModelEndpoint != project.DefaultPromptModelEndpoint {
		fmt.Printf("  Model endpoint: %s\n", settings.ModelEndpoint)
	}

	fmt.Println()
	fmt.Println("Authoring:")
	fmt.Printf("  %-16s the agent's definition and instructions\n", "azure.yaml")
	printPromptInitNextSteps(promptInitFolderDisplay(projectTargetDir, existingProject))
}

func promptInitFolderDisplay(projectTargetDir string, existingProject bool) string {
	if existingProject || projectTargetDir == "." {
		return ""
	}
	return projectTargetDir
}

// printPromptInitNextSteps prints the shared prompt/managed post-init guidance.
func printPromptInitNextSteps(folderDisplay string) {
	fmt.Println()
	fmt.Println("Next steps:")
	if folderDisplay != "" {
		fmt.Printf("  cd %q\n", folderDisplay)
	}
	fmt.Println("  # Provision infrastructure and deploy the agent")
	fmt.Println("  azd up")
	fmt.Println("  # Or, once provisioned, just (re)deploy the agent")
	fmt.Println("  azd deploy")
	fmt.Println("  # Invoke it")
	fmt.Println("  azd ai agent invoke \"hello\"")
}
