// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"bytes"
	"context"
	"errors"
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
	`^\s*\$\{\{\s*(inputs|github\.event\.inputs)\.environment\s*\|\|\s*'((?:''|[^'])*)'\s*\}\}\s*$`,
)
var githubAzdDeploymentCommandPattern = regexp.MustCompile(
	`(?:^|[\s;&|])azd\s+(?:up|provision|deploy)(?:\s|$)`,
)

const (
	githubWorkflowInputNone     = ""
	githubWorkflowInputUnified  = "inputs"
	githubWorkflowInputDispatch = "github.event.inputs"
)

type githubWorkflowJob struct {
	name        string
	environment string
	dynamic     bool
}

type githubWorkflowAnalysis struct {
	hasAutomaticTrigger      bool
	hasEnvironmentInput      bool
	hasWorkflowDispatch      bool
	hasWorkflowCall          bool
	dispatchInputGuaranteed  bool
	callInputGuaranteed      bool
	deploymentJobs           []githubWorkflowJob
	missingEnvironmentJobs   []string
	unsafeEnvironmentJobs    []string
	ambiguousEnvironmentJobs []string
}

func (pm *PipelineManager) validateGitHubWorkflow(ctx context.Context, props projectProperties) error {
	workflowPath := findPipelineFile(ciProviderGitHubActions, props.RepoRoot)
	if workflowPath == "" {
		workflows, err := findGitHubWorkflowFiles(props.RepoRoot)
		if err != nil {
			return fmt.Errorf("finding GitHub Actions workflows: %w", err)
		}
		if len(workflows) == 0 && props.GitHubEnvironment == "" {
			return nil
		}
		scope := fmt.Sprintf("GitHub environment %q", props.GitHubEnvironment)
		if props.GitHubEnvironment == "" {
			scope = "repository-scoped variables and secrets"
		}
		return pm.confirmAmbiguousGitHubWorkflow(
			ctx,
			fmt.Sprintf(
				"azd could not locate azure-dev.yml or azure-dev.yaml to verify that workflows in %s use %s",
				props.RepoRoot,
				scope,
			),
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
	if props.GitHubEnvironment != "" &&
		bytes.Equal(normalizeWorkflowLineEndings(contents), normalizeWorkflowLineEndings(legacyContents)) {
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

	if props.GitHubEnvironment == "" {
		var environmentJobs []string
		for _, job := range analysis.deploymentJobs {
			if job.environment != "" {
				environmentJobs = append(environmentJobs, job.name)
			}
		}
		if len(environmentJobs) > 0 {
			return fmt.Errorf(
				"GitHub Actions deployment job(s) %s select a GitHub environment, but azd is configured to use "+
					"repository-scoped variables and secrets",
				strings.Join(environmentJobs, ", "),
			)
		}
		return nil
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
			"GitHub Actions deployment job(s) %s can evaluate to an empty environment; require the input, give it "+
				"a nonempty default, or add a fallback such as environment: ${{ inputs.environment || '%s' }}",
			strings.Join(analysis.unsafeEnvironmentJobs, ", "),
			strings.ReplaceAll(props.GitHubEnvironment, "'", "''"),
		)
	}

	ambiguousConfirmed := false
	if len(analysis.ambiguousEnvironmentJobs) > 0 {
		if err := pm.confirmAmbiguousGitHubWorkflow(
			ctx,
			fmt.Sprintf(
				"azd could not determine the GitHub environment used by deployment job(s) %s in %s",
				strings.Join(analysis.ambiguousEnvironmentJobs, ", "),
				workflowPath,
			),
		); err != nil {
			return err
		}
		ambiguousConfirmed = true
	}

	if !ambiguousConfirmed && !githubWorkflowCanReachEnvironment(analysis, props.GitHubEnvironment) {
		return fmt.Errorf(
			"GitHub Actions workflow %s cannot select GitHub environment %q for any deployment job",
			workflowPath,
			props.GitHubEnvironment,
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

func findGitHubWorkflowFiles(repoRoot string) ([]string, error) {
	workflowDirectory := filepath.Join(repoRoot, gitHubRoot, gitHubWorkflows)
	entries, err := os.ReadDir(workflowDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var workflows []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension == ".yml" || extension == ".yaml" {
			workflows = append(workflows, filepath.Join(workflowDirectory, entry.Name()))
		}
	}
	return workflows, nil
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
	dispatchInput := githubWorkflowEnvironmentInput(onNode, "workflow_dispatch")
	callInput := githubWorkflowEnvironmentInput(onNode, "workflow_call")
	analysis.hasWorkflowDispatch = yamlMappingValue(onNode, "workflow_dispatch") != nil
	analysis.hasWorkflowCall = yamlMappingValue(onNode, "workflow_call") != nil
	analysis.hasEnvironmentInput = dispatchInput != nil || callInput != nil
	analysis.dispatchInputGuaranteed = githubWorkflowInputGuaranteed(dispatchInput)
	analysis.callInputGuaranteed = githubWorkflowInputGuaranteed(callInput)

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

		inputKind := githubWorkflowInputKind(environmentName)
		switch {
		case environmentName == "":
			analysis.missingEnvironmentJobs = append(analysis.missingEnvironmentJobs, jobName)
		case workflowJob.dynamic && inputKind != githubWorkflowInputNone &&
			!githubWorkflowInputIsSafe(analysis, inputKind):
			analysis.unsafeEnvironmentJobs = append(analysis.unsafeEnvironmentJobs, jobName)
		case workflowJob.dynamic:
			_, hasFallback := githubWorkflowEnvironmentFallback(environmentName)
			if !hasFallback && inputKind == githubWorkflowInputNone {
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
		return onNode.Value != "workflow_dispatch" && onNode.Value != "workflow_call"
	case yaml.SequenceNode:
		for _, event := range onNode.Content {
			if event.Value != "workflow_dispatch" && event.Value != "workflow_call" {
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

func githubWorkflowEnvironmentInput(onNode *yaml.Node, trigger string) *yaml.Node {
	triggerNode := yamlMappingValue(onNode, trigger)
	inputs := yamlMappingValue(triggerNode, "inputs")
	return yamlMappingValue(inputs, "environment")
}

func githubWorkflowInputGuaranteed(inputNode *yaml.Node) bool {
	if inputNode == nil {
		return false
	}
	required := yamlMappingValue(inputNode, "required")
	if required != nil && required.Value == "true" {
		return true
	}
	defaultValue := yamlMappingValue(inputNode, "default")
	return defaultValue != nil && strings.TrimSpace(defaultValue.Value) != ""
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
		if githubAzdDeploymentCommandPattern.MatchString(run.Value) {
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
	for environmentNode.Kind == yaml.AliasNode {
		environmentNode = environmentNode.Alias
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
	if len(matches) != 3 {
		return "", false
	}
	return strings.ReplaceAll(matches[2], "''", "'"), true
}

func githubWorkflowInputKind(expression string) string {
	normalized := strings.Join(strings.Fields(expression), "")
	switch normalized {
	case "${{inputs.environment}}":
		return githubWorkflowInputUnified
	case "${{github.event.inputs.environment}}":
		return githubWorkflowInputDispatch
	default:
		return githubWorkflowInputNone
	}
}

func githubWorkflowInputIsSafe(analysis *githubWorkflowAnalysis, inputKind string) bool {
	if analysis.hasAutomaticTrigger {
		return false
	}
	switch inputKind {
	case githubWorkflowInputUnified:
		return (!analysis.hasWorkflowDispatch || analysis.dispatchInputGuaranteed) &&
			(!analysis.hasWorkflowCall || analysis.callInputGuaranteed)
	case githubWorkflowInputDispatch:
		return analysis.hasWorkflowDispatch &&
			!analysis.hasWorkflowCall &&
			analysis.dispatchInputGuaranteed
	default:
		return false
	}
}

func githubWorkflowCanReachEnvironment(
	analysis *githubWorkflowAnalysis,
	environmentName string,
) bool {
	for _, job := range analysis.deploymentJobs {
		if job.environment == environmentName {
			return true
		}
		if job.dynamic {
			fallback, hasFallback := githubWorkflowEnvironmentFallback(job.environment)
			if hasFallback {
				if fallback == environmentName {
					return true
				}
				matches := githubEnvironmentFallbackPattern.FindStringSubmatch(job.environment)
				if len(matches) == 3 && githubWorkflowHasInputKind(analysis, matches[1]) {
					return true
				}
			}
			inputKind := githubWorkflowInputKind(job.environment)
			if inputKind != githubWorkflowInputNone && githubWorkflowHasInputKind(analysis, inputKind) {
				return true
			}
		}
	}
	return false
}

func githubWorkflowHasInputKind(analysis *githubWorkflowAnalysis, inputKind string) bool {
	switch inputKind {
	case githubWorkflowInputUnified:
		return analysis.hasEnvironmentInput
	case githubWorkflowInputDispatch:
		return analysis.hasWorkflowDispatch
	default:
		return false
	}
}

func normalizeWorkflowLineEndings(contents []byte) []byte {
	return bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
}
