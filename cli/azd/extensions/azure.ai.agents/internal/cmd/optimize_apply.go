// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_apply.go implements the "optimize apply" command, which downloads
// an optimization candidate and applies it locally to the azd project.
//
// It writes the candidate's instruction, skills, and tool definitions
// into .agent_configs/<candidate-id>/, updates agent.yaml environment
// variables, and shows a diff summary (prompt and skills) against the
// baseline.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/opteval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// agentConfigsDir aliases the shared constant for local use.
const agentConfigsDir = opteval.AgentConfigsDir

// optimizeApplyFlags holds CLI flags for the optimize apply command.
type optimizeApplyFlags struct {
	candidate string // candidate ID from optimization results
	agent     string // agent service name
	optimizeConnectionFlags
}

func newOptimizeApplyCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeApplyFlags{}
	action := &OptimizeApplyAction{flags: flags, noPrompt: extCtx.NoPrompt}

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply optimized candidate configuration locally to your azd project.",
		Long: `Download the optimized configuration and skill files from an optimization
candidate and write them into your local azd project under .agent_configs/.

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

// apply downloads and writes the candidate config, updates agent.yaml,
// stores state, and prints a diff summary.
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
	candidateDir := filepath.Join(serviceDir, agentConfigsDir, a.flags.candidate)

	bold.Fprintf(out, "Applying optimization candidate %s...\n\n", a.flags.candidate)

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}
	optClient := optimize_api.NewOptimizeClient(projectEndpoint, credential)

	// Step 1: Fetch candidate config from the optimization service.
	fmt.Fprintf(out, "  Fetching candidate config...\n")
	candidateConfig, err := optClient.GetCandidateConfig(ctx, a.flags.candidate)
	if err != nil {
		return fmt.Errorf("failed to fetch candidate config: %w", err)
	}

	if err := os.MkdirAll(candidateDir, 0750); err != nil {
		return fmt.Errorf("failed to create optimization directory: %w", err)
	}

	// Clean up other candidate directories, keeping only baseline and the current candidate.
	cleanOtherCandidates(filepath.Join(serviceDir, agentConfigsDir), a.flags.candidate, out)

	// Step 2: Download skill files into the candidate directory (before metadata.yaml
	// so the skills/ dir exists when writeAgentConfigFromCandidate checks for it).
	if n, dlErr := downloadSkillFilesToDir(ctx, optClient, a.flags.candidate, candidateDir, out); dlErr != nil {
		fmt.Fprintf(out, "  warning: failed to download skill files: %s\n", dlErr)
	} else if n > 0 {
		fmt.Fprintf(out, "  Downloaded %d skill file(s)\n", n)
	}

	// Write metadata.yaml, instructions.md, skills, and tool definitions for the candidate.
	if err := writeAgentConfigFromCandidate(candidateDir, candidateConfig); err != nil {
		return fmt.Errorf("failed to write candidate config: %w", err)
	}
	fmt.Fprintf(out, "  → %s\n", filepath.Join(candidateDir, opteval.MetadataFile))

	// Step 3: Write OPTIMIZATION_LOCAL_DIR and OPTIMIZATION_CANDIDATE_ID into agent.yaml
	// so the deploy pipeline knows which local optimization config to use.
	agentYamlPath := filepath.Join(serviceDir, "agent.yaml")
	fmt.Fprintf(out, "  Updating %s...\n", agentYamlPath)
	if err := upsertAgentYamlEnvVar(agentYamlPath, "OPTIMIZATION_LOCAL_DIR", agentConfigsDir); err != nil {
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
		a.flags.candidate, filepath.Join(agentConfigsDir, a.flags.candidate))
	fmt.Fprintf(out, "  Run %s to deploy the optimized agent.\n",
		color.CyanString("azd deploy --service %s", svc.Name))

	// Point the user to the config folders for comparison.
	baselinePath := filepath.Join(serviceDir, agentConfigsDir, opteval.BaselineDir)
	candidatePath := filepath.Join(serviceDir, agentConfigsDir, a.flags.candidate)
	fmt.Fprintf(out, "\n  To see the full diff, compare the files in:\n")
	fmt.Fprintf(out, "    Baseline:  %s\n", color.CyanString(baselinePath))
	fmt.Fprintf(out, "    Optimized: %s\n", color.CyanString(candidatePath))

	return nil
}

// agentConfigMetadata is the YAML structure written as metadata.yaml in each
// agent config version directory (baseline or candidate).
//
// It uses file pointers instead of embedding large content inline:
//   - instruction_file → points to instructions.md in the same directory
//   - skill_dir        → points to the skills/ subdirectory
//   - tools_file       → points to a tools definition file (optional)
type agentConfigMetadata struct {
	Name            string `yaml:"name"`
	Model           string `yaml:"model,omitempty"`
	InstructionFile string `yaml:"instruction_file,omitempty"`
	SkillDir        string `yaml:"skill_dir,omitempty"`
	ToolsFile       string `yaml:"tools_file,omitempty"`
}

// loadBaselineConfig reads the baseline metadata.yaml from
// <agentProject>/.agent_configs/baseline/metadata.yaml and resolves
// file pointers to absolute paths.
func loadBaselineConfig(agentProject string) (*agentConfigMetadata, error) {
	baseDir := filepath.Join(agentProject, agentConfigsDir, opteval.BaselineDir)
	metaPath := filepath.Join(baseDir, opteval.MetadataFile)
	data, err := os.ReadFile(metaPath) //nolint:gosec // path derived from project directory
	if err != nil {
		return nil, err
	}

	var meta agentConfigMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing baseline metadata: %w", err)
	}
	return &meta, nil
}

// resolveInstructions reads the instruction content from the metadata's
// instruction_file, resolved relative to configDir.
func (m *agentConfigMetadata) resolveInstructions(configDir string) string {
	if m.InstructionFile == "" {
		return ""
	}
	path := m.InstructionFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(configDir, path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from project directory
	if err != nil {
		return ""
	}
	return string(data)
}

// resolveSkillDir returns the absolute path to the skill directory,
// resolved relative to configDir. Returns empty if not set.
func (m *agentConfigMetadata) resolveSkillDir(configDir string) string {
	if m.SkillDir == "" {
		return ""
	}
	if filepath.IsAbs(m.SkillDir) {
		return m.SkillDir
	}
	return filepath.Join(configDir, m.SkillDir)
}

// writeAgentConfigFromCandidate writes metadata.yaml, instructions.md, skill
// files, and tool definitions for an optimization candidate into the given
// directory. No config.json is written — all content is decomposed into
// individual files with pointers in metadata.yaml.
func writeAgentConfigFromCandidate(candidateDir string, candidateConfig any) error {
	meta := agentConfigMetadata{}

	// Extract fields from the candidate config map.
	m, _ := candidateConfig.(map[string]any)
	if m != nil {
		if v, exists := m["name"]; exists {
			if s, ok := v.(string); ok {
				meta.Name = s
			}
		}
		if v, exists := m["agentName"]; exists {
			if s, ok := v.(string); ok {
				meta.Name = s
			}
		}
		if v, exists := m["model"]; exists {
			if s, ok := v.(string); ok {
				meta.Model = s
			}
		}
	}

	// Write instructions.md from the candidate's system prompt.
	instructions := extractInstructions(candidateConfig)
	if instructions != "" {
		instructionPath := filepath.Join(candidateDir, opteval.InstructionFile)
		if err := os.WriteFile(instructionPath, []byte(instructions), 0600); err != nil {
			return fmt.Errorf("writing candidate instructions: %w", err)
		}
		meta.InstructionFile = opteval.InstructionFile
	}

	// Write inline skills from the candidate config as individual files.
	if m != nil {
		if err := writeInlineSkills(candidateDir, m); err != nil {
			return fmt.Errorf("writing candidate skills: %w", err)
		}
	}

	// Set skill_dir pointer if the skills/ dir exists (from inline or downloaded skills).
	skillDir := filepath.Join(candidateDir, opteval.SkillsDir)
	if info, err := os.Stat(skillDir); err == nil && info.IsDir() {
		meta.SkillDir = opteval.SkillsDir
	}

	// Write tool_definitions as a JSON file.
	if m != nil {
		if err := writeToolDefinitions(candidateDir, m); err != nil {
			return fmt.Errorf("writing candidate tool definitions: %w", err)
		}
		if _, err := os.Stat(filepath.Join(candidateDir, opteval.ToolsFile)); err == nil {
			meta.ToolsFile = opteval.ToolsFile
		}
	}

	// Write metadata.yaml.
	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("serializing candidate metadata: %w", err)
	}
	metaPath := filepath.Join(candidateDir, opteval.MetadataFile)
	if err := os.WriteFile(metaPath, data, 0600); err != nil {
		return fmt.Errorf("writing candidate metadata: %w", err)
	}

	return nil
}

// writeInlineSkills extracts the "skills" array from a candidate config and
// writes each skill as skills/<name>/SKILL.md. Each file contains a YAML
// front-matter header with the skill name and description, followed by the
// skill body.
func writeInlineSkills(candidateDir string, config map[string]any) error {
	skillsRaw, exists := config["skills"]
	if !exists {
		return nil
	}
	skills, ok := skillsRaw.([]any)
	if !ok || len(skills) == 0 {
		return nil
	}

	for _, s := range skills {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := sm["name"].(string)
		if name == "" {
			continue
		}
		body, _ := sm["body"].(string)
		description, _ := sm["description"].(string)

		skillSubDir := filepath.Join(candidateDir, opteval.SkillsDir, name)
		if err := os.MkdirAll(skillSubDir, 0750); err != nil {
			return fmt.Errorf("creating skill directory %s: %w", name, err)
		}

		// Build the skill file content with YAML front-matter.
		var content strings.Builder
		content.WriteString("---\n")
		content.WriteString(fmt.Sprintf("name: %s\n", name))
		if description != "" {
			content.WriteString(fmt.Sprintf("description: %s\n", description))
		}
		content.WriteString("---\n")
		if body != "" {
			content.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				content.WriteString("\n")
			}
		}

		filePath := filepath.Join(skillSubDir, "SKILL.md")
		if err := os.WriteFile(filePath, []byte(content.String()), 0600); err != nil {
			return fmt.Errorf("writing skill %s: %w", name, err)
		}
	}
	return nil
}

// writeToolDefinitions extracts the "tool_definitions" field from a candidate
// config and writes it as tools.json.
func writeToolDefinitions(candidateDir string, config map[string]any) error {
	toolsRaw, exists := config["tool_definitions"]
	if !exists {
		return nil
	}

	data, err := json.MarshalIndent(toolsRaw, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing tool definitions: %w", err)
	}

	return os.WriteFile(filepath.Join(candidateDir, opteval.ToolsFile), data, 0600)
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
// except the baseline and the candidate being applied.
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
		if name == opteval.BaselineDir || name == currentCandidate {
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
