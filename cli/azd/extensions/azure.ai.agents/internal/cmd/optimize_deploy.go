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
	"os"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type optimizeDeployFlags struct {
	candidate string
	agent     string
	optimizeConnectionFlags
}

func newOptimizeDeployCommand() *cobra.Command {
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

			if len(args) > 0 && flags.agent == "" {
				flags.agent = args[0]
			}

			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVar(&flags.candidate, "candidate", "", "Candidate ID from optimization results (required)")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Agent name to deploy to (auto-detected from agent.yaml)")
	_ = cmd.MarkFlagRequired("candidate")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

// OptimizeDeployAction implements the optimize deploy command.
type OptimizeDeployAction struct {
	flags *optimizeDeployFlags
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
	// Resolve agent name from flag or agent.yaml in current directory.
	resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, false)
	if err != nil {
		return err
	}
	agentName := resolved.agentName

	// Resolve project endpoint (for Foundry agent API).
	projectEndpoint, err := resolveProjectEndpointForDeploy(ctx, &a.flags.optimizeConnectionFlags)
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
	candidateConfig, err := optClient.GetCandidateConfig(ctx, a.flags.candidate)
	if err != nil {
		return fmt.Errorf("failed to fetch candidate config: %w", err)
	}

	// JSON-stringify the candidate config for the env var.
	configJSON, err := json.Marshal(candidateConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize candidate config: %w", err)
	}

	// Step 2: Fetch current agent from Foundry.
	fmt.Fprintf(out, "  Fetching current agent definition...\n")
	agentClient := agent_api.NewAgentClient(projectEndpoint, credential)

	agentObj, err := agentClient.GetAgent(ctx, agentName, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to get agent %q: %w", agentName, err)
	}

	// Extract definition from latest version using map[string]any for flexibility.
	latestDef, err := extractLatestDefinition(agentObj)
	if err != nil {
		return err
	}

	// Step 3: Merge env vars and create new version.
	// Use OPTIMIZATION_CONFIG (non-reserved) — the agent SDK reads both
	// AGENT_OPTIMIZATION_CONFIG (first-party service) and OPTIMIZATION_CONFIG (CLI).
	// TODO: if the SSL issue is resolved, change to resolved endpoint + candidate ID.
	envVars := extractEnvVars(latestDef)
	envVars["OPTIMIZATION_CONFIG"] = string(configJSON)

	newDef := buildDeployDefinition(latestDef, envVars)

	description := fmt.Sprintf("Optimized: candidate %s", a.flags.candidate)
	createReq := &agent_api.CreateAgentVersionRequest{
		Description: &description,
		Metadata:    map[string]string{"optimized_from": a.flags.candidate},
		Definition:  newDef,
	}

	fmt.Fprintf(out, "  Creating new agent version...\n")
	versionObj, err := agentClient.CreateAgentVersion(ctx, agentName, createReq, DefaultAgentAPIVersion)
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

	// Step 4: Poll until version is active.
	fmt.Fprintf(out, "  Waiting for version %s to become active...\n", versionObj.Version)
	if err := pollVersionActive(ctx, agentClient, agentName, versionObj.Version); err != nil {
		return err
	}

	// Step 5: Report the deployment to the optimization service (best-effort).
	if err := optClient.ReportDeployment(ctx, &optimize_api.DeploymentReport{
		CandidateID:  a.flags.candidate,
		AgentName:    agentName,
		AgentVersion: versionObj.Version,
	}); err != nil {
		// Non-fatal — deployment succeeded, just log the reporting failure.
		fmt.Fprintf(out, "  %s failed to report deployment to optimization service: %s\n",
			color.YellowString("warning:"), err)
	}

	// Step 6: Print success.
	fmt.Fprintln(out)
	_, _ = color.New(color.FgGreen, color.Bold).Fprintf(out,
		"  \u2713 Successfully deployed candidate %s as version %s\n", a.flags.candidate, versionObj.Version)
	fmt.Fprintf(out, "\n  Agent:   %s\n", agentName)
	fmt.Fprintf(out, "  Version: %s\n", versionObj.Version)

	return nil
}

// upsertAgentYamlEnvVar reads the agent.yaml file, adds or updates the specified
// environment variable in the environment_variables list, and writes back.
func upsertAgentYamlEnvVar(agentYamlPath, key, value string) error {
	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // G304: path from azd project
	if err != nil {
		return fmt.Errorf("reading agent.yaml: %w", err)
	}

	var agent agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &agent); err != nil {
		return fmt.Errorf("parsing agent.yaml: %w", err)
	}

	// Upsert the environment variable.
	if agent.EnvironmentVariables == nil {
		agent.EnvironmentVariables = &[]agent_yaml.EnvironmentVariable{}
	}

	found := false
	envVars := *agent.EnvironmentVariables
	for i := range envVars {
		if envVars[i].Name == key {
			envVars[i].Value = value
			found = true
			break
		}
	}
	if !found {
		envVars = append(envVars, agent_yaml.EnvironmentVariable{Name: key, Value: value})
	}
	agent.EnvironmentVariables = &envVars

	// Marshal back to YAML and write.
	out, err := yaml.Marshal(&agent)
	if err != nil {
		return fmt.Errorf("marshaling agent.yaml: %w", err)
	}

	//nolint:gosec // G306: agent.yaml should be readable by tooling
	if err := os.WriteFile(agentYamlPath, out, 0644); err != nil {
		return fmt.Errorf("writing agent.yaml: %w", err)
	}

	return nil
}

// resolveProjectEndpointForDeploy resolves the Foundry project endpoint using
// the same resolution chain as other agent commands.
func resolveProjectEndpointForDeploy(ctx context.Context, connFlags *optimizeConnectionFlags) (string, error) {
	if connFlags.projectEndpoint != "" {
		return strings.TrimRight(connFlags.projectEndpoint, "/"), nil
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
	return newDef
}

// normalizeProtocolVersions ensures container_protocol_versions use the
// canonical "1.0.0" format instead of the legacy "v1" format that the
// platform no longer accepts for new versions.
func normalizeProtocolVersions(def map[string]any) {
	raw, ok := def["container_protocol_versions"]
	if !ok {
		return
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

		version, err := client.GetAgentVersion(ctx, agentName, versionNum, DefaultAgentAPIVersion)
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
