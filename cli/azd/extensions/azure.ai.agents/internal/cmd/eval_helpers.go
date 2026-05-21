// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_helpers.go provides shared utility functions used by both eval and
// optimize commands, including portal URL construction and path display
// helpers.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// resolvePortalPrefix reads AZURE_AI_PROJECT_ID from the azd environment and
// returns a PortalPrefix for building Foundry portal URLs.
// Returns nil on any failure.
func resolvePortalPrefix(ctx context.Context, azdClient *azdext.AzdClient, envName string) *eval_api.PortalPrefix {
	if azdClient == nil || envName == "" {
		return nil
	}
	v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil || v.Value == "" {
		log.Printf("[debug] could not read AZURE_AI_PROJECT_ID: %v", err)
		return nil
	}
	prefix, err := eval_api.NewPortalPrefix(v.Value)
	if err != nil {
		log.Printf("[debug] failed to build portal prefix: %v", err)
		return nil
	}
	return prefix
}

// buildEvalReportURL constructs the Foundry portal URL for an eval run report.
// Returns empty string on any failure.
func buildEvalReportURL(ctx context.Context, azdClient *azdext.AzdClient, envName, evalID, runID string) string {
	if evalID == "" || runID == "" {
		return ""
	}
	prefix := resolvePortalPrefix(ctx, azdClient, envName)
	if prefix == nil {
		return ""
	}
	return prefix.EvalRunURL(evalID, runID)
}

// printPortalLink resolves the portal prefix and prints a portal URL.
// The buildURL callback receives the resolved prefix and returns the full URL.
// Best-effort — silently skips on any failure.
func printPortalLink(ctx context.Context, out io.Writer, azdClient *azdext.AzdClient, envName string, buildURL func(*eval_api.PortalPrefix) string) {
	prefix := resolvePortalPrefix(ctx, azdClient, envName)
	if prefix == nil {
		return
	}
	fmt.Fprintf(out, "  Portal: %s\n", color.CyanString(buildURL(prefix)))
}

// relativeDisplay returns a project-relative path for display purposes.
// Used by both eval and optimize config confirmation prompts.
// Returns empty string for empty input.
func relativeDisplay(absPath, projectDir string) string {
	if absPath == "" || projectDir == "" {
		return absPath
	}
	if rel, err := filepath.Rel(projectDir, absPath); err == nil {
		return rel
	}
	return absPath
}

// reconcileConfigAgentName reconciles the agent name in a config with the
// environment-resolved name. Environment takes precedence. Returns true if
// the config was changed. Used by both eval run and optimize.
func reconcileConfigAgentName(agent *opteval.AgentRef, envName, configSource string) bool {
	if envName == "" || agent.Name == "" || agent.Name == envName {
		if envName != "" && agent.Name == "" {
			agent.Name = envName
		}
		return false
	}
	fmt.Printf("  %s agent name in %s (%q) differs from environment (%q) — using environment value\n",
		color.YellowString("warning:"), configSource, agent.Name, envName)
	agent.Name = envName
	return true
}

// resolveAgentConfig resolves agent configuration from config metadata
// using a priority chain:
//
//  1. existingConfig's agent.config path — if the config references a
//     metadata.yaml, resolve all fields from it.
//  2. Default baseline path — try .agent_configs/baseline/metadata.yaml.
//  3. Nothing found — returns nil; the caller should prompt the user
//     for an instruction and then call writeBaselineIfNeeded.
//
// The returned AgentConfig contains resolved instruction file path, model,
// skill_dir, and tools_file. Eval init uses only instruction fields;
// optimize also uses skill_dir and tools_file.
func resolveAgentConfig(
	existingConfig *opteval.Config,
	projectDir string,
) *opteval.AgentConfig {
	// Step 1: existing config has a config pointer — resolve from it.
	if existingConfig != nil && existingConfig.Agent.ConfigFile != "" {
		ref := opteval.AgentRef{ConfigFile: existingConfig.Agent.ConfigFile}
		return ref.ResolveConfig(projectDir)
	}

	// Step 2: try the default baseline path.
	if projectDir != "" {
		relPath := opteval.BaselineConfigRelPath()
		if fileExists(filepath.Join(projectDir, relPath)) {
			ref := opteval.AgentRef{ConfigFile: relPath}
			return ref.ResolveConfig(projectDir)
		}
	}

	// Step 3: nothing found — caller should prompt and write baseline.
	return nil
}

// writeBaselineIfNeeded creates a baseline config when no config was resolved
// but an instruction is available. Returns the config file relative path
// (empty if nothing was written).
func writeBaselineIfNeeded(
	projectDir, instruction string,
) string {
	if projectDir == "" || instruction == "" {
		return ""
	}
	defaultConfigFile := opteval.BaselineConfigRelPath()
	absConfigFile := filepath.Join(projectDir, defaultConfigFile)
	// Don't overwrite an existing baseline.
	if fileExists(absConfigFile) {
		return ""
	}
	if err := writeBaselineConfig(projectDir, baselineParams{
		Instruction: instruction,
	}); err != nil {
		fmt.Printf("   warning: failed to write baseline config: %s\n", err)
		return ""
	}
	fmt.Printf("   Baseline:   %s\n", absConfigFile)
	return defaultConfigFile
}

// baselineParams holds optional inputs for writing a baseline agent config.
type baselineParams struct {
	Model       string // agent model (optional)
	Instruction string // system prompt text (optional)
	SkillDir    string // absolute skill dir path (empty = auto-detect)
	ToolsFile   string // absolute tools file path (optional)
}

// writeBaselineConfig writes a baseline agent config to .agent_configs/baseline/.
// It creates metadata.yaml with file pointers and writes instructions.md.
// When skillDir is empty, it auto-detects a "skills" or "skill" directory.
// Used by both eval init and optimize.
func writeBaselineConfig(agentProject string, p baselineParams) error {
	baseDir := filepath.Join(agentProject, opteval.AgentConfigsDir, opteval.BaselineDir)
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return fmt.Errorf("creating baseline directory: %w", err)
	}

	meta := struct {
		Model           string `yaml:"model,omitempty"`
		InstructionFile string `yaml:"instruction_file,omitempty"`
		SkillDir        string `yaml:"skill_dir,omitempty"`
		ToolsFile       string `yaml:"tools_file,omitempty"`
	}{
		Model: p.Model,
	}

	if p.Instruction != "" {
		instructionPath := filepath.Join(baseDir, opteval.InstructionFile)
		if err := os.WriteFile(instructionPath, []byte(p.Instruction), 0600); err != nil {
			return fmt.Errorf("writing baseline instructions: %w", err)
		}
		meta.InstructionFile = opteval.InstructionFile
	}

	// Resolve skill_dir: use explicit path, or auto-detect from project.
	skillDir := p.SkillDir
	if skillDir == "" {
		for _, candidate := range []string{"skills", "skill"} {
			dir := filepath.Join(agentProject, candidate)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				skillDir = dir
				break
			}
		}
	}
	if skillDir != "" {
		if rel, err := filepath.Rel(baseDir, skillDir); err == nil {
			meta.SkillDir = filepath.ToSlash(rel)
		} else {
			meta.SkillDir = skillDir
		}
	}

	if p.ToolsFile != "" {
		if rel, err := filepath.Rel(baseDir, p.ToolsFile); err == nil {
			meta.ToolsFile = filepath.ToSlash(rel)
		} else {
			meta.ToolsFile = p.ToolsFile
		}
	}

	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("serializing baseline metadata: %w", err)
	}

	metaPath := filepath.Join(baseDir, opteval.MetadataFile)
	if err := os.WriteFile(metaPath, data, 0600); err != nil {
		return fmt.Errorf("writing baseline metadata: %w", err)
	}

	return nil
}

// loadJSONLFile reads a JSONL file and unmarshals each non-empty line into T.
// Returns an error if the file cannot be read, a line fails to parse, or no items are found.
func loadJSONLFile[T any](path string) ([]T, error) {
	f, err := os.Open(path) //nolint:gosec // path is provided by user for local dataset
	if err != nil {
		return nil, fmt.Errorf("failed to open dataset file %s: %w", path, err)
	}
	defer f.Close()

	var items []T
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("failed to parse dataset line %d: %w", lineNum, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading dataset file %s: %w", path, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("dataset file %s contains no items", path)
	}
	return items, nil
}

// statusLabelAndColor maps a raw status to a display label and color function.
func statusLabelAndColor(status string) (string, func(string, ...any) string) {
	switch status {
	case "completed":
		return "Completed", color.GreenString
	case "succeeded":
		return "Succeeded", color.GreenString
	case "failed":
		return "Failed", color.RedString
	case "cancelled", "canceled":
		return "Cancelled", color.YellowString
	case "running", "in_progress":
		return "Running", color.CyanString
	case "partial":
		return "Partial", color.YellowString
	case "":
		return "No runs", color.HiBlackString
	default:
		return status, fmt.Sprintf
	}
}

// colorizeStatus returns a colorized status string for display.
func colorizeStatus(status string) string {
	label, colorFn := statusLabelAndColor(status)
	return colorFn(label)
}

// padColorizedStatus returns a fixed-width colored status string so that
// tabwriter aligns columns correctly despite ANSI escape sequences.
func padColorizedStatus(status string) string {
	const statusWidth = 10 // wide enough for "Completed", "Cancelled", etc.
	label, colorFn := statusLabelAndColor(status)
	padded := fmt.Sprintf("%-*s", statusWidth, label)
	return colorFn(padded)
}
