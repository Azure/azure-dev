// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_deploy.go implements the "optimize deploy" command, which deploys
// an optimization candidate directly to a Foundry agent (without requiring
// an azd project). It fetches the candidate config, patches the agent, and
// creates a new agent version.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/optimize_api"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type optimizeDeployFlags struct {
	candidate string
	agent     string
	optimizeConnectionFlags
}

func newOptimizeDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeDeployFlags{}
	action := &OptimizeDeployAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "deploy [agent-name]",
		Short: "Deploy a winning optimization candidate as a new agent version via the API.",
		Long: `Deploy an optimization candidate directly via the Foundry agent API.

This creates a new agent version with the optimized configuration applied.
Use 'optimize apply' instead if you want to localize the config into your azd project first.`,
		Example: `  # Deploy candidate directly
  azd ai agent optimize deploy --candidate candidate_abc123 --agent my-agent

  # Deploy with explicit endpoint
  azd ai agent optimize deploy --candidate candidate_abc123 --agent my-agent --project-endpoint https://...`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			// Read extCtx fields here (after PersistentPreRunE has populated them
			// from -e / AZD_ENVIRONMENT), not at command construction time.
			action.envName = extCtx.Environment

			if len(args) > 0 && flags.agent == "" {
				flags.agent = args[0]
			}

			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVar(&flags.candidate, "candidate", "", "Candidate ID from optimization results (required)")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Agent service name from azure.yaml, or Foundry agent name outside a project")
	_ = cmd.MarkFlagRequired("candidate")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

// OptimizeDeployAction implements the optimize deploy command.
type OptimizeDeployAction struct {
	flags   *optimizeDeployFlags
	envName string
}

func (a *OptimizeDeployAction) Run(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)

	return a.runDirect(ctx, out, bold)
}

// runDirect deploys a candidate directly via the Foundry agent API.
// TODO: Change this to full remote deployment here if not in an azd project
func (a *OptimizeDeployAction) runDirect(
	ctx context.Context,
	out io.Writer,
	bold *color.Color,
) error {
	// Resolve agent name from flag or azd project environment.
	resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, a.envName, false)
	if err != nil {
		return err
	}
	agentName := resolved.agentName

	// Resolve project endpoint (for Foundry agent API).
	projectEndpoint, err := resolveProjectEndpointForDeploy(ctx, &a.flags.optimizeConnectionFlags, a.envName)
	if err != nil {
		return err
	}

	_, _ = bold.Fprintf(out, "Deploying candidate %s to agent %s...\n\n", a.flags.candidate, agentName)

	// Step 1: Fetch candidate config from optimization service.
	fmt.Fprintf(out, "  Fetching candidate config...\n")
	credential, err := newAgentCredential()
	if err != nil {
		return err
	}
	optClient := optimize_api.NewOptimizeClient(projectEndpoint, credential)

	// Resolve the optimization job ID — candidate endpoints are nested under it.
	jobID := loadOptimizeJobIDForAgent(ctx, optimizeEnvKeyName(resolved.serviceName, agentName), a.envName)
	if jobID == "" {
		return fmt.Errorf(
			"no optimization job found in the environment; run 'azd ai agent optimize' first")
	}

	candidateConfig, err := optClient.GetCandidateConfig(ctx, jobID, a.flags.candidate)
	if err != nil {
		return fmt.Errorf("failed to fetch candidate config: %w", err)
	}

	// Step 2: Fetch current agent from Foundry.
	fmt.Fprintf(out, "  Fetching current agent definition...\n")
	agentClient := agent_api.NewAgentClient(projectEndpoint, credential)

	agentObj, err := agentClient.GetAgent(ctx, agentName, DefaultAgentAPIVersion, false)
	if err != nil {
		return fmt.Errorf("failed to get agent %q: %w", agentName, err)
	}

	// Extract definition from latest version using map[string]any for flexibility.
	latestDef, err := extractLatestDefinition(agentObj)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("Optimized: candidate %s", a.flags.candidate)
	metadata := map[string]string{"optimized_from": a.flags.candidate}
	var versionObj *agent_api.AgentVersionObject

	isPromptAgent := stringFromMap(latestDef, "kind") == string(agent_api.AgentKindPrompt)
	if isPromptAgent {
		newDef, err := buildPromptDeployDefinition(latestDef, candidateConfig)
		if err != nil {
			return err
		}
		headers := optimizeDeployAgentHeaders(latestDef, projectEndpoint)
		fmt.Fprintf(out, "  Creating new prompt agent version...\n")
		updatedAgent, err := agentClient.UpdateAgentWithHeaders(
			ctx,
			agentName,
			&agent_api.UpdateAgentRequest{
				Description: &description,
				Metadata:    metadata,
				Definition:  newDef,
			},
			DefaultAgentAPIVersion,
			headers,
		)
		if err != nil {
			return fmt.Errorf("failed to create prompt agent version: %w", err)
		}
		versionObj = &updatedAgent.Versions.Latest
		if versionObj.Status != "active" {
			fmt.Fprintf(out, "  Waiting for version %s to become active...\n", versionObj.Version)
			versionObj, err = pollPromptVersionActive(ctx, agentClient, agentName, headers)
			if err != nil {
				return err
			}
		}
	} else {
		// Hosted agents select the candidate at runtime through OPTIMIZATION_CONFIG.
		configJSON, err := json.Marshal(candidateConfig)
		if err != nil {
			return fmt.Errorf("failed to serialize candidate config: %w", err)
		}
		envVars := extractEnvVars(latestDef)
		envVars["OPTIMIZATION_CONFIG"] = string(configJSON)

		fmt.Fprintf(out, "  Creating new agent version...\n")
		versionObj, err = agentClient.CreateAgentVersion(
			ctx,
			agentName,
			&agent_api.CreateAgentVersionRequest{
				Description: &description,
				Metadata:    metadata,
				Definition:  buildDeployDefinition(latestDef, envVars),
			},
			DefaultAgentAPIVersion,
		)
		if err != nil {
			// Check for reserved env var error (AGENT_* and FOUNDRY_* are platform-reserved).
			if isReservedEnvVarError(err) {
				return fmt.Errorf("the platform reserves AGENT_* environment variables for internal use.\n\n" +
					"Deploying optimization candidates for hosted (container) agents requires the\n" +
					"optimization service to create versions with elevated privileges.\n\n" +
					"Contact the platform team to promote via the optimization service API")
			}
			return fmt.Errorf("failed to create agent version: %w", err)
		}

		fmt.Fprintf(out, "  Waiting for version %s to become active...\n", versionObj.Version)
		if err := pollVersionActive(ctx, agentClient, agentName, versionObj.Version); err != nil {
			return err
		}
	}

	// Step 4: Report the deployment to the optimization service (best-effort).
	if err := optClient.ReportDeploymentWithHeaders(ctx, jobID, &optimize_api.DeploymentReport{
		CandidateID:  a.flags.candidate,
		AgentName:    agentName,
		AgentVersion: versionObj.Version,
	}, optimizationPromotionHeaders(isPromptAgent)); err != nil {
		// Non-fatal — deployment succeeded, just log the reporting failure.
		fmt.Fprintf(out, "  %s failed to report deployment to optimization service: %s\n",
			color.YellowString("warning:"), err)
	}

	// Step 5: Print success.
	fmt.Fprintln(out)
	_, _ = color.New(color.FgGreen, color.Bold).Fprintf(out,
		"  \u2713 Successfully deployed candidate %s as version %s\n", a.flags.candidate, versionObj.Version)
	fmt.Fprintf(out, "\n  Agent:   %s\n", agentName)
	fmt.Fprintf(out, "  Version: %s\n", versionObj.Version)

	return nil
}

func optimizeDeployAgentHeaders(def map[string]any, projectEndpoint string) map[string]string {
	if stringFromMap(def, "kind") != string(agent_api.AgentKindPrompt) {
		return nil
	}

	settings := &projectpkg.PromptAgentSettings{ProjectEndpoint: projectEndpoint}
	headers := map[string]string{
		"x-model-endpoint": settings.EffectiveModelEndpoint(),
	}

	var features []string
	if harnessTypeFromMap(def) == agent_api.ManagedAgentHarnessGitHubCopilot {
		features = append(features, agent_api.GitHubCopilotPreviewFeature)
	}
	if skills, ok := def["skills"].([]any); ok && len(skills) > 0 {
		features = append(features, agent_api.SkillsPreviewFeature)
	}
	if len(features) > 0 {
		headers["Foundry-Features"] = strings.Join(features, ",")
	}
	return headers
}

func optimizationPromotionHeaders(reportOnly bool) map[string]string {
	if !reportOnly {
		return nil
	}
	return map[string]string{
		optimize_api.PromotionReportOnlyHeader: "true",
	}
}

func buildPromptDeployDefinition(
	currentDef map[string]any,
	candidateConfig json.RawMessage,
) (map[string]any, error) {
	updates, err := promptAgentCandidateValues(candidateConfig)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(currentDef)
	if err != nil {
		return nil, fmt.Errorf("failed to copy prompt agent definition: %w", err)
	}
	var newDef map[string]any
	if err := json.Unmarshal(data, &newDef); err != nil {
		return nil, fmt.Errorf("failed to copy prompt agent definition: %w", err)
	}

	newDef["model"] = updates.model
	newDef["instructions"] = updates.instructions
	if err := mergePromptAgentTools(newDef, updates.functionTools); err != nil {
		return nil, fmt.Errorf("updating prompt agent tools: %w", err)
	}
	return newDef, nil
}

// resolveProjectEndpointForDeploy resolves the Foundry project endpoint using
// the same resolution chain as other agent commands.
func resolveProjectEndpointForDeploy(ctx context.Context, connFlags *optimizeConnectionFlags, envName string) (string, error) {
	if connFlags.projectEndpoint != "" {
		return strings.TrimRight(connFlags.projectEndpoint, "/"), nil
	}

	// When an explicit envName is provided, try the named environment first.
	if envName != "" {
		if ep := endpointFromNamedEnv(ctx, envName); ep != "" {
			return strings.TrimRight(ep, "/"), nil
		}
	}

	projectEndpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		if ep := projectEndpointFromEnv(); ep != "" {
			return ep, nil
		}
		return "", fmt.Errorf("could not resolve project endpoint: %w\n\n"+
			"Provide --project-endpoint (-p), or run 'azd ai agent init'", err)
	}
	return projectEndpoint, nil
}

// isReservedEnvVarError checks if a version creation error is due to
// the platform rejecting reserved AGENT_* or FOUNDRY_* environment variables.
// TODO: Use azcore.ResponseError.StatusCode + stable API error code when available,
// instead of brittle substring matching on server error wording.
func isReservedEnvVarError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "reserved for platform use") ||
		strings.Contains(msg, "AGENT_* variables are reserved")
}

// --- Skill file download ---

// isSkillFile returns true if the manifest entry represents a skill file.
func isSkillFile(f optimize_api.CandidateFile) bool {
	return f.Type == "skill" || strings.HasPrefix(f.Path, "skills/")
}

// extractLatestDefinition gets the latest version's definition as a map for flexible field access.
func extractLatestDefinition(agent *agent_api.AgentObject) (map[string]any, error) {
	defBytes, err := json.Marshal(agent.Versions.Latest.Definition)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent definition: %w", err)
	}

	var defMap map[string]any
	if err := json.Unmarshal(defBytes, &defMap); err != nil {
		return nil, fmt.Errorf("failed to parse agent definition: %w", err)
	}
	return defMap, nil
}

// extractEnvVars extracts existing environment variables from a definition map.
func extractEnvVars(def map[string]any) map[string]string {
	result := make(map[string]string)
	if envRaw, ok := def["environment_variables"]; ok {
		if envMap, ok := envRaw.(map[string]any); ok {
			for k, v := range envMap {
				if s, ok := v.(string); ok {
					result[k] = s
				}
			}
		}
	}
	return result
}

// buildDeployDefinition creates the definition map for the new version,
// preserving all fields from the current version but overriding env vars.
func buildDeployDefinition(currentDef map[string]any, envVars map[string]string) map[string]any {
	newDef := make(map[string]any)
	for k, v := range currentDef {
		if k != "environment_variables" {
			newDef[k] = v
		}
	}
	newDef["environment_variables"] = envVars
	normalizeProtocolVersions(newDef)
	normalizeContainerImage(newDef)
	return newDef
}

// normalizeProtocolVersions ensures protocol_versions (or legacy container_protocol_versions)
// use the canonical "1.0.0" format instead of the legacy "v1" format that the
// platform no longer accepts for new versions.
func normalizeProtocolVersions(def map[string]any) {
	// Try new field name first, fall back to legacy
	raw, ok := def["protocol_versions"]
	if !ok {
		raw, ok = def["container_protocol_versions"]
		if !ok {
			return
		}
		// Migrate legacy field to new name
		def["protocol_versions"] = raw
		delete(def, "container_protocol_versions")
	}
	protocols, ok := raw.([]any)
	if !ok {
		return
	}
	for _, p := range protocols {
		pMap, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if ver, ok := pMap["version"].(string); ok && ver == "v1" {
			pMap["version"] = "1.0.0"
		}
	}
}

// normalizeContainerImage migrates the legacy top-level "image" field to
// "container_configuration.image" on raw definition maps. This is needed because
// service responses stored as map[string]any bypass HostedAgentDefinition.UnmarshalJSON.
func normalizeContainerImage(def map[string]any) {
	// Already using new schema
	if _, ok := def["container_configuration"]; ok {
		return
	}
	// Migrate legacy top-level image
	image, ok := def["image"]
	if !ok {
		return
	}
	imageStr, ok := image.(string)
	if !ok || imageStr == "" {
		return
	}
	def["container_configuration"] = map[string]any{"image": imageStr}
	delete(def, "image")
}

// pollVersionActive polls the agent version until its status is "active" or a timeout occurs.
func pollVersionActive(
	ctx context.Context,
	client *agent_api.AgentClient,
	agentName, versionNum string,
) error {
	timeout := 5 * time.Minute
	interval := 5 * time.Second
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for version %s to become active after %s", versionNum, timeout)
		}

		version, err := client.GetAgentVersion(ctx, agentName, versionNum, DefaultAgentAPIVersion, false)
		if err != nil {
			return fmt.Errorf("failed to poll version status: %w", err)
		}

		if version.Status == "active" {
			return nil
		}

		if version.Status == "failed" {
			return fmt.Errorf("version %s failed to activate", versionNum)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func pollPromptVersionActive(
	ctx context.Context,
	client *agent_api.AgentClient,
	agentName string,
	headers map[string]string,
) (*agent_api.AgentVersionObject, error) {
	timeout := 5 * time.Minute
	interval := 5 * time.Second
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for prompt agent %q to become active after %s", agentName, timeout)
		}

		agent, err := client.GetAgentWithHeaders(ctx, agentName, DefaultAgentAPIVersion, headers)
		if err != nil {
			return nil, fmt.Errorf("failed to poll prompt agent status: %w", err)
		}
		latest := agent.Versions.Latest
		switch latest.Status {
		case "active":
			return &latest, nil
		case "failed":
			if latest.Error != nil {
				return nil, fmt.Errorf(
					"prompt agent version %s failed to activate: [%s] %s",
					latest.Version,
					latest.Error.Code,
					latest.Error.Message,
				)
			}
			return nil, fmt.Errorf("prompt agent version %s failed to activate", latest.Version)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}
