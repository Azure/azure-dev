// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizationDir is the default folder that holds optimized candidate versions.
const optimizationDir = ".agent_optimization"

type optimizeApplyFlags struct {
	candidate string
	agent     string
	optimizeConnectionFlags
}

func newOptimizeApplyCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeApplyFlags{}
	action := &OptimizeApplyAction{flags: flags, noPrompt: extCtx.NoPrompt}

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply optimized candidate configuration locally to your azd project.",
		Long: `Download the optimized configuration and skill files from an optimization
candidate and write them into your local azd project under .agent_optimization/.

After applying, run 'azd deploy' to deploy the optimized agent version.`,
		Example: `  # Apply candidate config locally, then deploy
  azd ai agent optimize apply --candidate cand_abc123
  azd deploy`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())
			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVar(&flags.candidate, "candidate", "", "Candidate ID from optimization results (required)")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Agent service name (auto-detected from azure.yaml)")
	_ = cmd.MarkFlagRequired("candidate")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

// OptimizeApplyAction implements the optimize apply command.
type OptimizeApplyAction struct {
	flags    *optimizeApplyFlags
	noPrompt bool
}

func (a *OptimizeApplyAction) Run(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w\n\n"+
			"'optimize apply' requires an azd project. Use 'optimize deploy' for standalone API deployment", err)
	}
	defer azdClient.Close()

	svc, project, err := resolveAgentService(ctx, azdClient, a.flags.agent, a.noPrompt)
	if err != nil || project == nil || svc == nil {
		return fmt.Errorf("could not resolve agent service in azd project: %w\n\n"+
			"Run 'azd ai agent init' first, or use 'optimize deploy' for standalone API deployment", err)
	}

	return a.apply(ctx, azdClient, svc, project, out, bold)
}

func (a *OptimizeApplyAction) apply(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	project *azdext.ProjectConfig,
	out io.Writer,
	bold *color.Color,
) error {
	projectEndpoint, err := resolveProjectEndpointForDeploy(ctx, &a.flags.optimizeConnectionFlags)
	if err != nil {
		return err
	}

	serviceDir := filepath.Join(project.Path, svc.RelativePath)
	candidateDir := filepath.Join(serviceDir, optimizationDir, a.flags.candidate)

	bold.Fprintf(out, "Applying optimization candidate %s...\n\n", a.flags.candidate)

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}
	optClient := optimize_api.NewOptimizeClient(projectEndpoint, credential)

	// Step 1: Fetch candidate config and write to config.json.
	fmt.Fprintf(out, "  Fetching candidate config...\n")
	candidateConfig, err := optClient.GetCandidateConfig(ctx, a.flags.candidate)
	if err != nil {
		return fmt.Errorf("failed to fetch candidate config: %w", err)
	}

	if err := os.MkdirAll(candidateDir, 0750); err != nil {
		return fmt.Errorf("failed to create optimization directory: %w", err)
	}

	// Clean up other candidate directories, keeping only baseline and the current candidate.
	cleanOtherCandidates(filepath.Join(serviceDir, optimizationDir), a.flags.candidate, out)

	configJSON, err := json.MarshalIndent(candidateConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize candidate config: %w", err)
	}

	configPath := filepath.Join(candidateDir, "config.json")
	if err := os.WriteFile(configPath, configJSON, 0600); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
	}
	fmt.Fprintf(out, "  → %s\n", configPath)

	// Step 2: Download skill files into the candidate directory.
	if n, dlErr := downloadSkillFilesToDir(ctx, optClient, a.flags.candidate, candidateDir, out); dlErr != nil {
		fmt.Fprintf(out, "  warning: failed to download skill files: %s\n", dlErr)
	} else if n > 0 {
		fmt.Fprintf(out, "  Downloaded %d skill file(s)\n", n)
	}

	// Step 3: Write OPTIMIZATION_LOCAL_DIR and OPTIMIZATION_CANDIDATE_ID into agent.yaml
	// so the deploy pipeline knows which local optimization config to use.
	agentYamlPath := filepath.Join(serviceDir, "agent.yaml")
	fmt.Fprintf(out, "  Updating %s...\n", agentYamlPath)
	if err := upsertAgentYamlEnvVar(agentYamlPath, "OPTIMIZATION_LOCAL_DIR", optimizationDir); err != nil {
		return fmt.Errorf("failed to update agent.yaml: %w", err)
	}
	if err := upsertAgentYamlEnvVar(agentYamlPath, "OPTIMIZATION_CANDIDATE_ID", a.flags.candidate); err != nil {
		return fmt.Errorf("failed to update agent.yaml: %w", err)
	}

	// Step 4: Store candidate ID in the azd environment for tracking.
	serviceKey := toServiceKey(svc.Name)
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	candidateKey := fmt.Sprintf("AGENT_%s_OPTIMIZATION_CANDIDATE_ID", serviceKey)
	if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     candidateKey,
		Value:   a.flags.candidate,
	}); err != nil {
		return fmt.Errorf("failed to store candidate ID in azd environment: %w", err)
	}

	// Done — prompt the user to deploy.
	fmt.Fprintln(out)
	color.New(color.FgGreen, color.Bold).Fprintf(out,
		"  ✓ Candidate %s applied to %s\n\n",
		a.flags.candidate, filepath.Join(optimizationDir, a.flags.candidate))
	fmt.Fprintf(out, "  Run %s to deploy the optimized agent.\n",
		color.CyanString("azd deploy --service %s", svc.Name))

	// Show prompt diff (baseline → optimized).
	printPromptDiff(out, serviceDir, a.flags.candidate, candidateConfig)

	return nil
}

// baselineConfig is the JSON structure saved as the agent's pre-optimization baseline.
type baselineConfig struct {
	Instructions string `json:"instructions,omitempty"`
	Model        string `json:"model,omitempty"`
	Name         string `json:"name"`
	SkillDir     string `json:"skill_dir,omitempty"`
}

// saveBaselineConfig writes the agent's current configuration to
// <agentProject>/.agent_optimization/baseline/config.json before optimization begins.
func saveBaselineConfig(agentProject, skillDir string, req *optimize_api.OptimizeRequest) error {
	baseDir := filepath.Join(agentProject, optimizationDir, "baseline")
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return fmt.Errorf("creating baseline directory: %w", err)
	}

	cfg := baselineConfig{
		Instructions: req.Agent.SystemPrompt,
		Model:        req.Agent.Model,
		Name:         req.Agent.AgentName,
		SkillDir:     skillDir,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing baseline config: %w", err)
	}

	configPath := filepath.Join(baseDir, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("writing baseline config: %w", err)
	}

	return nil
}

// loadBaselineConfig reads the baseline config from
// <agentProject>/.agent_optimization/baseline/config.json.
func loadBaselineConfig(agentProject string) (*baselineConfig, error) {
	configPath := filepath.Join(agentProject, optimizationDir, "baseline", "config.json")
	data, err := os.ReadFile(configPath) //nolint:gosec // path derived from project directory
	if err != nil {
		return nil, err
	}

	var cfg baselineConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing baseline config: %w", err)
	}
	return &cfg, nil
}

// downloadSkillFilesToDir fetches the candidate manifest, downloads all skill
// files, and writes them into the given directory. Returns the number of files written.
func downloadSkillFilesToDir(
	ctx context.Context,
	client *optimize_api.OptimizeClient,
	candidateID string,
	destDir string,
	out io.Writer,
) (int, error) {
	manifest, err := client.GetCandidate(ctx, candidateID)
	if err != nil {
		return 0, fmt.Errorf("fetching candidate manifest: %w", err)
	}

	var skillFiles []optimize_api.CandidateFile
	for _, f := range manifest.Files {
		if isSkillFile(f) {
			skillFiles = append(skillFiles, f)
		}
	}
	if len(skillFiles) == 0 {
		return 0, nil
	}

	count := 0
	for _, f := range skillFiles {
		if f.Path == "" {
			continue
		}

		content, err := client.GetCandidateFile(ctx, candidateID, f.Path)
		if err != nil {
			fmt.Fprintf(out, "  warning: failed to download skill file %s: %s\n", f.Path, err)
			continue
		}

		outPath := filepath.Join(destDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(outPath), 0750); err != nil {
			return count, fmt.Errorf("creating directory for %s: %w", f.Path, err)
		}

		if err := os.WriteFile(outPath, []byte(content), 0600); err != nil {
			return count, fmt.Errorf("writing skill file %s: %w", f.Path, err)
		}

		fmt.Fprintf(out, "  → %s (%d bytes)\n", outPath, len(content))
		count++
	}

	return count, nil
}

// cleanOtherCandidates removes all subdirectories in the optimization folder
// except "baseline" and the candidate being applied.
func cleanOtherCandidates(optimizeDir, currentCandidate string, out io.Writer) {
	entries, err := os.ReadDir(optimizeDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "baseline" || name == currentCandidate {
			continue
		}
		dir := filepath.Join(optimizeDir, name)
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(out, "  warning: failed to remove old candidate %s: %s\n", name, err)
		} else {
			fmt.Fprintf(out, "  Removed old candidate: %s\n", name)
		}
	}
}

// maxDiffPreviewLines is the max lines shown per section in the prompt diff preview.
const maxDiffPreviewLines = 4

// printPromptDiff displays an abbreviated prompt diff (baseline → optimized)
// with a short preview and a suggested command for the full diff.
func printPromptDiff(out io.Writer, serviceDir, candidateID string, candidateConfig any) {
	optimized := extractInstructions(candidateConfig)
	if optimized == "" {
		return
	}

	baseline, err := loadBaselineConfig(serviceDir)
	if err != nil || baseline.Instructions == "" {
		return
	}

	baselineText := baseline.Instructions
	baselineLines := strings.Split(baselineText, "\n")
	optimizedLines := strings.Split(optimized, "\n")

	fmt.Fprintf(out, "\n  Prompt diff (baseline → optimized):\n\n")

	// Baseline preview (removed).
	removed := color.New(color.FgRed)
	removed.Fprintf(out, "    — Baseline (%d lines, %d chars):\n",
		len(baselineLines), len(baselineText))
	printPreviewLines(out, baselineLines, "- ", removed)

	fmt.Fprintln(out)

	// Optimized preview (added).
	added := color.New(color.FgGreen)
	added.Fprintf(out, "    — Optimized (%d lines, %d chars):\n",
		len(optimizedLines), len(optimized))
	printPreviewLines(out, optimizedLines, "+ ", added)

	// Suggest command to see the full diff.
	baselinePath := filepath.Join(optimizationDir, "baseline", "config.json")
	candidatePath := filepath.Join(optimizationDir, candidateID, "config.json")
	fmt.Fprintf(out, "\n  To see the full diff:\n")
	fmt.Fprintf(out, "    %s\n",
		color.CyanString("diff %s %s", baselinePath, candidatePath))
}

// printPreviewLines prints up to maxDiffPreviewLines with a prefix, then "..." if truncated.
func printPreviewLines(out io.Writer, lines []string, prefix string, c *color.Color) {
	limit := min(len(lines), maxDiffPreviewLines)
	for _, line := range lines[:limit] {
		c.Fprintf(out, "    %s%s\n", prefix, line)
	}
	if len(lines) > maxDiffPreviewLines {
		c.Fprintf(out, "    %s... (%d more lines)\n", prefix, len(lines)-maxDiffPreviewLines)
	}
}

// extractInstructions retrieves the system prompt string from a candidate config
// returned by the optimization service.
func extractInstructions(config any) string {
	m, ok := config.(map[string]any)
	if !ok {
		return ""
	}
	if v, exists := m["systemPrompt"]; exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	if v, exists := m["instructions"]; exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
