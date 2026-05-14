// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

const (
	defaultEvalConfigName = "eval.yaml"
	defaultEvalName       = "smoke-core"
	defaultEvalModel      = "gpt-4o"
	defaultEvalSamples    = 100
)

type evalConfig = eval_api.EvalConfig
type evalAgentRef = opteval.AgentRef
type evalDatasetRef = opteval.DatasetRef

// evalState holds transient runtime state stored in the azd environment.
type evalState struct {
	InitStatus       string
	DatasetGenOpID   string
	DatasetGenStatus string
	EvalGenOpID      string
	EvalGenStatus    string
	EvalID           string
}

// Azd environment keys for eval state.
const (
	evalKeyInitStatus       = "LAST_EVAL_INIT_STATUS"
	evalKeyDatasetGenOpID   = "LAST_EVAL_DATASET_GEN_OP_ID"
	evalKeyDatasetGenStatus = "LAST_EVAL_DATASET_GEN_STATUS"
	evalKeyEvalGenOpID      = "LAST_EVAL_GEN_OP_ID"
	evalKeyEvalGenStatus    = "LAST_EVAL_GEN_STATUS"
	evalKeyEvalID           = "LAST_EVAL_ID"
)

type evalResolvedContext struct {
	azdClient             *azdext.AzdClient
	evalClient            *eval_api.EvalClient
	datasetClient         *dataset_api.DatasetClient
	projectRoot           string
	hasProject            bool
	agentProject          string
	agentProjectSource    string
	agentName             string
	agentNameSource       string
	version               string
	versionSource         string
	agentKind             agent_yaml.AgentKind
	agentKindSource       string
	serviceName           string
	projectEndpoint       string
	projectEndpointSource string
	envName               string
}

type evalContextOptions struct {
	agent           string
	projectEndpoint string
	requireAgent    bool
	noPrompt        bool
}

func newEvalCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval <command>",
		Short: "Create and run quick evals for an agent.",
		Long: `Create and run quick evals for an agent.

These commands are designed for quick agent eval onboarding under azd ai agent.
Use eval init to generate an eval config, then eval run to execute it.`,
	}

	cmd.AddCommand(newEvalInitCommand(extCtx))
	cmd.AddCommand(newEvalRunCommand())
	cmd.AddCommand(newEvalListCommand())
	cmd.AddCommand(newEvalShowCommand())

	return cmd
}

func resolveEvalContext(ctx context.Context, options evalContextOptions) (*evalResolvedContext, error) {
	fmt.Println(output.WithGrayFormat("Resolving eval context..."))

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}

	fmt.Println(output.WithGrayFormat("  Reading project configuration..."))
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})

	// If no azd workspace is found, fall back to prompt-based resolution.
	if err != nil || projectResponse.Project == nil {
		return resolveEvalContextWithoutProject(ctx, azdClient, options)
	}
	project := projectResponse.Project

	fmt.Println(output.WithGrayFormat("  Detecting agent service..."))

	// Read the current azd environment once — used for agent info, endpoint, and env name.
	var envName string
	envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if envErr == nil && envResp.Environment != nil {
		envName = envResp.Environment.Name
	}

	getEnvValue := func(key string) string {
		if envName == "" {
			return ""
		}
		v, e := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName, Key: key,
		})
		if e != nil || v.Value == "" {
			return ""
		}
		return v.Value
	}

	var svc *azdext.ServiceConfig
	var info *AgentServiceInfo
	svc, _, err = resolveAgentService(ctx, azdClient, options.agent, options.noPrompt)
	if err == nil {
		// Resolve deployed agent name/version from azd environment.
		info = &AgentServiceInfo{ServiceName: svc.Name}
		serviceKey := toServiceKey(svc.Name)
		if v := getEnvValue(fmt.Sprintf("AGENT_%s_NAME", serviceKey)); v != "" {
			info.AgentName = v
		}
		if v := getEnvValue(fmt.Sprintf("AGENT_%s_VERSION", serviceKey)); v != "" {
			info.Version = v
		}
	} else if options.agent == "" && options.requireAgent {
		azdClient.Close()
		return nil, evalAgentContextError(err)
	}

	fmt.Println(output.WithGrayFormat("  Resolving Foundry project endpoint..."))
	projectEndpoint := options.projectEndpoint
	projectEndpointSource := "--project-endpoint"
	if projectEndpoint == "" {
		if v := getEnvValue("AZURE_AI_PROJECT_ENDPOINT"); v != "" {
			projectEndpoint = v
			projectEndpointSource = "AZURE_AI_PROJECT_ENDPOINT"
		}
	}
	if projectEndpoint == "" {
		if v := getEnvValue("AZURE_AI_PROJECT_ID"); v != "" {
			ep, epErr := endpointFromProjectID(v)
			if epErr != nil {
				azdClient.Close()
				return nil, epErr
			}
			projectEndpoint = ep
			projectEndpointSource = "AZURE_AI_PROJECT_ID"
		}
	}
	if projectEndpoint == "" {
		azdClient.Close()
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"Foundry project context could not be resolved",
			"run 'azd ai agent init' to configure your project, or pass --project-endpoint directly",
		)
	}

	agentName := options.agent
	agentNameSource := "--agent"
	agentVersion := ""
	agentVersionSource := "unresolved"
	agentKind := agent_yaml.AgentKind("")
	agentKindSource := "unresolved"
	serviceName := ""
	agentProject := project.Path
	agentProjectSource := "workspace root"
	if agentName == "" {
		agentNameSource = "unresolved"
	}
	if svc != nil {
		serviceName = svc.Name
		agentProject = filepath.Join(project.Path, svc.RelativePath)
		agentProjectSource = fmt.Sprintf("azure.yaml service %q project path", svc.Name)
		serviceKey := toServiceKey(svc.Name)
		if info != nil && info.AgentName != "" {
			agentName = info.AgentName
			agentNameSource = fmt.Sprintf("AGENT_%s_NAME", serviceKey)
		}
		if info != nil && info.Version != "" {
			agentVersion = info.Version
			agentVersionSource = fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
		}
		if detectedKind, manifestPath := detectEvalAgentKind(agentProject); detectedKind != "" {
			agentKind = detectedKind
			agentKindSource = relPathForYaml(project.Path, manifestPath)
		}
	}
	if agentKind == "" {
		agentKind = agent_yaml.AgentKindHosted
		agentKindSource = "default"
	}
	if !agent_yaml.IsValidAgentKind(agentKind) {
		azdClient.Close()
		return nil, fmt.Errorf("unsupported agent kind %q", agentKind)
	}

	if options.requireAgent && agentName == "" {
		azdClient.Close()
		return nil, evalAgentContextError(nil)
	}

	credential, err := newAgentCredential()
	if err != nil {
		azdClient.Close()
		return nil, err
	}
	evalClient := eval_api.NewEvalClient(projectEndpoint, credential)
	datasetClient := dataset_api.NewDatasetClient(projectEndpoint, credential)

	return &evalResolvedContext{
		azdClient:             azdClient,
		evalClient:            evalClient,
		datasetClient:         datasetClient,
		projectRoot:           project.Path,
		hasProject:            true,
		agentProject:          agentProject,
		agentProjectSource:    agentProjectSource,
		agentName:             agentName,
		agentNameSource:       agentNameSource,
		version:               agentVersion,
		versionSource:         agentVersionSource,
		agentKind:             agentKind,
		agentKindSource:       agentKindSource,
		serviceName:           serviceName,
		projectEndpoint:       projectEndpoint,
		projectEndpointSource: projectEndpointSource,
		envName:               envName,
	}, nil
}

// resolveEvalContextWithoutProject prompts the user for essential inputs when
// there is no azd workspace (no azure.yaml). In --no-prompt mode it requires
// --project-endpoint and --agent to be passed explicitly.
func resolveEvalContextWithoutProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	options evalContextOptions,
) (*evalResolvedContext, error) {
	fmt.Println(output.WithGrayFormat("  No azd project found. Prompting for inputs..."))

	projectEndpoint := options.projectEndpoint
	agentName := options.agent

	if options.noPrompt {
		if projectEndpoint == "" {
			azdClient.Close()
			return nil, exterrors.Dependency(
				exterrors.CodeMissingAiProjectEndpoint,
				"--project-endpoint is required when running outside an azd project with --no-prompt",
				"pass --project-endpoint (-p) with your Foundry project endpoint URL",
			)
		}
		if agentName == "" && options.requireAgent {
			azdClient.Close()
			return nil, evalAgentContextError(nil)
		}
	} else {
		prompt := azdClient.Prompt()

		if projectEndpoint == "" {
			resp, err := prompt.Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Foundry project endpoint URL",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				azdClient.Close()
				return nil, fmt.Errorf("prompting for project endpoint: %w", err)
			}
			projectEndpoint = strings.TrimSpace(resp.Value)
			if projectEndpoint == "" {
				azdClient.Close()
				return nil, fmt.Errorf("project endpoint is required")
			}
		}

		if agentName == "" && options.requireAgent {
			resp, err := prompt.Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Agent name",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				azdClient.Close()
				return nil, fmt.Errorf("prompting for agent name: %w", err)
			}
			agentName = strings.TrimSpace(resp.Value)
			if agentName == "" {
				azdClient.Close()
				return nil, fmt.Errorf("agent name is required")
			}
		}
	}

	credential, err := newAgentCredential()
	if err != nil {
		azdClient.Close()
		return nil, err
	}

	cwd, _ := os.Getwd()
	evalClient := eval_api.NewEvalClient(projectEndpoint, credential)
	datasetClient := dataset_api.NewDatasetClient(projectEndpoint, credential)

	return &evalResolvedContext{
		azdClient:             azdClient,
		evalClient:            evalClient,
		datasetClient:         datasetClient,
		projectRoot:           cwd,
		agentProject:          cwd,
		agentProjectSource:    "current directory",
		agentName:             agentName,
		agentNameSource:       "user input",
		version:               "",
		versionSource:         "unresolved",
		agentKind:             agent_yaml.AgentKindHosted,
		agentKindSource:       "default",
		serviceName:           "",
		projectEndpoint:       projectEndpoint,
		projectEndpointSource: "user input",
		envName:               "",
	}, nil
}

func printEvalDetectedContext(resolved *evalResolvedContext, configPath string) {
	fmt.Println()
	fmt.Println(color.CyanString("Detected eval target:"))
	if resolved.serviceName != "" {
		printEvalField("Service", resolved.serviceName, "azure.yaml")
	}
	printEvalField("Agent", resolved.agentName, resolved.agentNameSource)
	printEvalField("Version", resolved.version, resolved.versionSource)
	printEvalField("Kind", string(resolved.agentKind), resolved.agentKindSource)
	printEvalField("Endpoint", resolved.projectEndpoint, resolved.projectEndpointSource)
	printEvalField("Project", resolved.agentProject, resolved.agentProjectSource)
	fmt.Printf("  Eval config:      %s\n", output.WithHighLightFormat(configPath))
	fmt.Println()
}

func printEvalField(label, value, source string) {
	padded := fmt.Sprintf("%-16s", label+":")
	if value == "" || source == "unresolved" {
		fmt.Printf("  %s%s\n", padded, output.WithGrayFormat("%s (%s)", value, source))
	} else {
		fmt.Printf("  %s %s %s\n",
			color.GreenString("(✓)"),
			padded+output.WithHighLightFormat(value),
			output.WithGrayFormat("(%s)", source),
		)
	}
}

func detectEvalAgentKind(agentProject string) (agent_yaml.AgentKind, string) {
	for _, fileName := range []string{"agent.yaml", "agent.yml"} {
		path := filepath.Join(agentProject, fileName)
		data, err := os.ReadFile(path) //nolint:gosec // local agent manifest path is derived from azure.yaml service project
		if err != nil {
			continue
		}

		var manifest struct {
			Kind agent_yaml.AgentKind `yaml:"kind"`
		}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			continue
		}
		if agent_yaml.IsValidAgentKind(manifest.Kind) {
			return manifest.Kind, path
		}
	}

	return "", ""
}

func evalAgentContextError(cause error) error {
	message := "agent context could not be resolved"
	if cause != nil {
		message = fmt.Sprintf("%s: %s", message, cause)
	}
	return exterrors.Dependency(
		exterrors.CodeMissingAgentEnvVars,
		message,
		"run 'azd ai agent init' to configure your agent, or pass --agent and --project-endpoint directly",
	)
}

func endpointFromProjectID(projectID string) (string, error) {
	project, err := extractProjectDetails(projectID)
	if err != nil {
		return "", err
	}
	return buildAgentEndpoint(project.AccountName, project.ProjectName), nil
}

func pollEvalOperation(
	ctx context.Context,
	label string,
	operationID string,
	get eval_api.GetJobFunc,
	apiVersion string,
) (*eval_api.GenerationJob, error) {
	return pollEvalOperationWithSpinner(ctx, label, operationID, get, apiVersion, true)
}

func pollEvalOperationWithSpinner(
	ctx context.Context,
	label string,
	operationID string,
	get eval_api.GetJobFunc,
	apiVersion string,
	showSpinner bool,
) (*eval_api.GenerationJob, error) {
	if operationID == "" {
		return nil, fmt.Errorf("%s did not return an operation ID", strings.ToLower(label))
	}

	start := time.Now()
	if showSpinner {
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        label + "...",
			ClearOnStop: true,
		})
		if err := spinner.Start(ctx); err != nil {
			fmt.Printf("%s: running\n", label)
		}
		defer func() { _ = spinner.Stop(ctx) }()
	}

	poller := eval_api.NewPoller(operationID, apiVersion, get)
	job, err := poller.Poll(ctx)

	elapsed := time.Since(start).Round(time.Second)

	if err != nil {
		if _, ok := errors.AsType[*eval_api.PollerTimeoutError](err); ok {
			fmt.Printf("  %s  %s  (%s)\n",
				color.YellowString("(!) Timed out"), label, elapsed)
			return nil, err
		}
		if jfe, ok := errors.AsType[*eval_api.JobFailedError](err); ok {
			if body, marshalErr := json.MarshalIndent(jfe.Job, "", "  "); marshalErr == nil {
				log.Printf("[debug] %s: failed response:\n%s", label, body)
			}
			fmt.Printf("  %s  %s  (%s)\n", color.RedString("(x) Failed"), label, elapsed)
			return nil, fmt.Errorf("%s failed with status %q", strings.ToLower(label), jfe.Status)
		}
		fmt.Printf("  %s  %s\n", color.RedString("(x) Failed"), label)
		return nil, err
	}

	log.Printf("[debug] %s: completed successfully", label)
	fmt.Printf("  %s  %s  (%s)\n", color.GreenString("(✓) Done"), label, elapsed)
	return job, nil
}

func readEvalConfig(path string) (*evalConfig, error) {
	return eval_api.LoadEvalConfig(path)
}

func writeEvalConfig(path string, cfg *evalConfig) error {
	return eval_api.WriteEvalConfig(path, cfg)
}

// formatTimestamp formats a timestamp value for display in eval output.
func formatTimestamp(ts any) string {
	return eval_api.FormatTimestamp(ts)
}

// loadEvalState reads eval runtime state from the azd environment.
// Returns an empty state if no values are set.
func loadEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string) *evalState {
	get := func(key string) string {
		v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName, Key: key,
		})
		if err != nil || v.Value == "" {
			return ""
		}
		return v.Value
	}
	return &evalState{
		InitStatus:       get(evalKeyInitStatus),
		DatasetGenOpID:   get(evalKeyDatasetGenOpID),
		DatasetGenStatus: get(evalKeyDatasetGenStatus),
		EvalGenOpID:      get(evalKeyEvalGenOpID),
		EvalGenStatus:    get(evalKeyEvalGenStatus),
		EvalID:           get(evalKeyEvalID),
	}
}

// saveEvalState persists eval runtime state to the azd environment.
func saveEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string, state *evalState) error {
	pairs := []struct {
		key, val string
	}{
		{evalKeyInitStatus, state.InitStatus},
		{evalKeyDatasetGenOpID, state.DatasetGenOpID},
		{evalKeyDatasetGenStatus, state.DatasetGenStatus},
		{evalKeyEvalGenOpID, state.EvalGenOpID},
		{evalKeyEvalGenStatus, state.EvalGenStatus},
		{evalKeyEvalID, state.EvalID},
	}
	for _, p := range pairs {
		if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: p.key, Value: p.val,
		}); err != nil {
			return fmt.Errorf("setting %s in azd env: %w", p.key, err)
		}
	}
	return nil
}

// clearEvalState removes eval state keys from the azd environment.
func clearEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string) {
	for _, key := range []string{
		evalKeyInitStatus, evalKeyDatasetGenOpID, evalKeyDatasetGenStatus,
		evalKeyEvalGenOpID, evalKeyEvalGenStatus, evalKeyEvalID,
	} {
		_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: key, Value: "",
		})
	}
}

func relPathForYaml(baseDir string, target string) string {
	if rel, err := filepath.Rel(baseDir, target); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(target)
}
