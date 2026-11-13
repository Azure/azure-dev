// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"go.yaml.in/yaml/v3"
)

var githubEnvironmentFallbackPattern = regexp.MustCompile(
	`^\s*\$\{\{\s*(?:inputs|github\.event\.inputs)\.environment\s*\|\|\s*'((?:''|[^'])*)'\s*\}\}\s*$`,
)

type githubWorkflowJob struct {
	name        string
	environment string
	dynamic     bool
}

type githubWorkflowAnalysis struct {
	hasAutomaticTrigger      bool
	hasEnvironmentInput      bool
	deploymentJobs           []githubWorkflowJob
	missingEnvironmentJobs   []string
	unsafeEnvironmentJobs    []string
	ambiguousEnvironmentJobs []string
}

func (pm *PipelineManager) validateGitHubWorkflow(ctx context.Context, props projectProperties) error {
	workflowPath := findPipelineFile(ciProviderGitHubActions, props.RepoRoot)
	if workflowPath == "" {
		return fmt.Errorf(
			"GitHub environment %q was selected, but no azure-dev GitHub Actions workflow was found",
			props.GitHubEnvironment,
		)
	}

	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		return fmt.Errorf("reading GitHub Actions workflow %s: %w", workflowPath, err)
	}

	legacyProps := props
	legacyProps.GitHubEnvironment = ""
	legacyContents, err := renderPipelineDefinition(legacyProps)
	if err != nil {
		return fmt.Errorf("rendering legacy GitHub Actions workflow: %w", err)
	}
	if bytes.Equal(contents, legacyContents) {
		if pm.console.IsNoPromptMode() {
			return fmt.Errorf(
				"the azd-generated workflow %s must be updated to use GitHub environment %q",
				workflowPath,
				props.GitHubEnvironment,
			)
		}
		update, err := pm.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Update the azd-generated workflow to use GitHub environment '%s'?",
				props.GitHubEnvironment,
			),
			DefaultValue: true,
		})
		if err != nil {
			return fmt.Errorf("prompting to update GitHub Actions workflow: %w", err)
		}
		if !update {
			return fmt.Errorf(
				"the workflow %s must select GitHub environment %q before pipeline configuration can continue",
				workflowPath,
				props.GitHubEnvironment,
			)
		}
		if err := generatePipelineDefinition(workflowPath, props); err != nil {
			return fmt.Errorf("updating GitHub Actions workflow: %w", err)
		}
		contents, err = os.ReadFile(workflowPath)
		if err != nil {
			return fmt.Errorf("reading updated GitHub Actions workflow %s: %w", workflowPath, err)
		}
	}

	analysis, err := analyzeGitHubWorkflow(contents)
	if err != nil {
		return fmt.Errorf("analyzing GitHub Actions workflow %s: %w", workflowPath, err)
	}

	pm.displayGitHubWorkflowSummary(ctx, workflowPath, props.GitHubEnvironment, analysis)

	if len(analysis.deploymentJobs) == 0 {
		return pm.confirmAmbiguousGitHubWorkflow(
			ctx,
			fmt.Sprintf("azd could not find a job that runs azd up, provision, or deploy in %s", workflowPath),
		)
	}
	if len(analysis.missingEnvironmentJobs) > 0 {
		return fmt.Errorf(
			"GitHub Actions deployment job(s) %s do not select an environment; add environment: %q to each job",
			strings.Join(analysis.missingEnvironmentJobs, ", "),
			props.GitHubEnvironment,
		)
	}
	if len(analysis.unsafeEnvironmentJobs) > 0 {
		return fmt.Errorf(
			"GitHub Actions deployment job(s) %s use inputs.environment without a fallback for automatic triggers; "+
				"add a fallback such as environment: ${{ inputs.environment || '%s' }}",
			strings.Join(analysis.unsafeEnvironmentJobs, ", "),
			strings.ReplaceAll(props.GitHubEnvironment, "'", "''"),
		)
	}

	incompatibleJobs := githubWorkflowIncompatibleJobs(analysis, props.GitHubEnvironment)
	if len(incompatibleJobs) > 0 {
		return fmt.Errorf(
			"GitHub Actions workflow %s does not deploy every deployment job to GitHub environment %q; "+
				"incompatible job(s): %s",
			workflowPath,
			props.GitHubEnvironment,
			strings.Join(incompatibleJobs, ", "),
		)
	}
	if len(analysis.ambiguousEnvironmentJobs) > 0 {
		return pm.confirmAmbiguousGitHubWorkflow(
			ctx,
			fmt.Sprintf(
				"azd could not determine the GitHub environment used by deployment job(s) %s in %s",
				strings.Join(analysis.ambiguousEnvironmentJobs, ", "),
				workflowPath,
			),
		)
	}

	return nil
}

func (pm *PipelineManager) confirmAmbiguousGitHubWorkflow(ctx context.Context, message string) error {
	if pm.console.IsNoPromptMode() {
		return fmt.Errorf("%s; verify the workflow environment configuration before running with --no-prompt", message)
	}
	pm.console.MessageUxItem(ctx, &ux.WarningMessage{Description: message})
	confirmed, err := pm.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Continue after confirming the workflow selects the configured GitHub environment?",
		DefaultValue: false,
	})
	if err != nil {
		return fmt.Errorf("confirming GitHub Actions workflow configuration: %w", err)
	}
	if !confirmed {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func (pm *PipelineManager) displayGitHubWorkflowSummary(
	ctx context.Context,
	workflowPath string,
	environmentName string,
	analysis *githubWorkflowAnalysis,
) {
	pm.console.Message(ctx, "")
	pm.console.Message(ctx, output.WithBold("GitHub pipeline configuration"))
	pm.console.Message(ctx, fmt.Sprintf("  azd environment:       %s", pm.env.Name()))
	pm.console.Message(ctx, fmt.Sprintf("  GitHub environment:    %s", environmentName))
	pm.console.Message(ctx, fmt.Sprintf("  Workflow:              %s", workflowPath))
	if analysis.hasAutomaticTrigger {
		pm.console.Message(ctx, "  Automatic triggers:    configured")
	} else {
		pm.console.Message(ctx, "  Automatic triggers:    none detected")
	}
	if analysis.hasEnvironmentInput {
		pm.console.Message(ctx, "  Environment input:     configured")
	} else {
		pm.console.Message(ctx, "  Environment input:     not configured")
	}
	pm.console.Message(ctx, "")
}

func findPipelineFile(provider ciProviderType, repoRoot string) string {
	for _, path := range pipelineProviderFiles[provider].Files {
		fullPath := filepath.Join(repoRoot, path)
		if osutil.FileExists(fullPath) {
			return fullPath
		}
	}
	return ""
}

func analyzeGitHubWorkflow(contents []byte) (*githubWorkflowAnalysis, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, fmt.Errorf("workflow is empty")
	}

	root := document.Content[0]
	analysis := &githubWorkflowAnalysis{}
	onNode := yamlMappingValue(root, "on")
	analysis.hasAutomaticTrigger = githubWorkflowHasAutomaticTrigger(onNode)
	analysis.hasEnvironmentInput = githubWorkflowHasEnvironmentInput(onNode)

	jobs := yamlMappingValue(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return analysis, nil
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		jobName := jobs.Content[i].Value
		job := jobs.Content[i+1]
		if !githubWorkflowJobRunsAzd(job) {
			continue
		}

		environmentName := githubWorkflowJobEnvironment(job)
		workflowJob := githubWorkflowJob{
			name:        jobName,
			environment: environmentName,
			dynamic:     strings.Contains(environmentName, "${{"),
		}
		analysis.deploymentJobs = append(analysis.deploymentJobs, workflowJob)

		switch {
		case environmentName == "":
			analysis.missingEnvironmentJobs = append(analysis.missingEnvironmentJobs, jobName)
		case workflowJob.dynamic &&
			analysis.hasAutomaticTrigger &&
			githubWorkflowUsesOnlyEnvironmentInput(environmentName):
			analysis.unsafeEnvironmentJobs = append(analysis.unsafeEnvironmentJobs, jobName)
		case workflowJob.dynamic:
			_, hasFallback := githubWorkflowEnvironmentFallback(environmentName)
			usesOnlyEnvironmentInput := githubWorkflowUsesOnlyEnvironmentInput(environmentName)
			if !hasFallback && (!usesOnlyEnvironmentInput || !analysis.hasEnvironmentInput) {
				analysis.ambiguousEnvironmentJobs = append(analysis.ambiguousEnvironmentJobs, jobName)
			}
		}
	}
	return analysis, nil
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func githubWorkflowHasAutomaticTrigger(onNode *yaml.Node) bool {
	if onNode == nil {
		return false
	}
	switch onNode.Kind {
	case yaml.ScalarNode:
		return onNode.Value != "workflow_dispatch"
	case yaml.SequenceNode:
		for _, event := range onNode.Content {
			if event.Value != "workflow_dispatch" {
				return true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(onNode.Content); i += 2 {
			if onNode.Content[i].Value != "workflow_dispatch" &&
				onNode.Content[i].Value != "workflow_call" {
				return true
			}
		}
	}
	return false
}

func githubWorkflowHasEnvironmentInput(onNode *yaml.Node) bool {
	for _, trigger := range []string{"workflow_dispatch", "workflow_call"} {
		triggerNode := yamlMappingValue(onNode, trigger)
		inputs := yamlMappingValue(triggerNode, "inputs")
		if yamlMappingValue(inputs, "environment") != nil {
			return true
		}
	}
	return false
}

func githubWorkflowJobRunsAzd(job *yaml.Node) bool {
	steps := yamlMappingValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return false
	}
	for _, step := range steps.Content {
		run := yamlMappingValue(step, "run")
		if run == nil {
			continue
		}
		command := run.Value
		if strings.Contains(command, "azd up") ||
			strings.Contains(command, "azd provision") ||
			strings.Contains(command, "azd deploy") {
			return true
		}
	}
	return false
}

func githubWorkflowJobEnvironment(job *yaml.Node) string {
	environmentNode := yamlMappingValue(job, "environment")
	if environmentNode == nil {
		return ""
	}
	if environmentNode.Kind == yaml.ScalarNode {
		return strings.TrimSpace(environmentNode.Value)
	}
	nameNode := yamlMappingValue(environmentNode, "name")
	if nameNode == nil {
		return ""
	}
	return strings.TrimSpace(nameNode.Value)
}

func githubWorkflowEnvironmentFallback(expression string) (string, bool) {
	matches := githubEnvironmentFallbackPattern.FindStringSubmatch(expression)
	if len(matches) != 2 {
		return "", false
	}
	return strings.ReplaceAll(matches[1], "''", "'"), true
}

func githubWorkflowUsesOnlyEnvironmentInput(expression string) bool {
	normalized := strings.Join(strings.Fields(expression), "")
	return normalized == "${{inputs.environment}}" ||
		normalized == "${{github.event.inputs.environment}}"
}

func githubWorkflowIncompatibleJobs(
	analysis *githubWorkflowAnalysis,
	environmentName string,
) []string {
	var incompatibleJobs []string
	for _, job := range analysis.deploymentJobs {
		if job.environment == environmentName {
			continue
		}
		if job.dynamic {
			fallback, hasFallback := githubWorkflowEnvironmentFallback(job.environment)
			if hasFallback {
				if fallback != environmentName {
					incompatibleJobs = append(
						incompatibleJobs,
						fmt.Sprintf("%s (fallback %q)", job.name, fallback),
					)
				}
				continue
			}
			if githubWorkflowUsesOnlyEnvironmentInput(job.environment) &&
				!analysis.hasAutomaticTrigger &&
				analysis.hasEnvironmentInput {
				continue
			}
			continue
		}
		if job.environment != "" {
			incompatibleJobs = append(
				incompatibleJobs,
				fmt.Sprintf("%s (%q)", job.name, job.environment),
			)
		}
	}
	return incompatibleJobs
}
