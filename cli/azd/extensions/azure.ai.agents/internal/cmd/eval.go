// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval.go implements the top-level "eval" command group and shared context
// resolution logic used by all eval subcommands (init, run, update, list, show).
//
// The evalResolvedContext struct holds the resolved agent, project, and
// endpoint information. It is built from azd project state, environment
// variables, or interactive prompts, and threaded through all subcommands.

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

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// Default values for eval configuration.
const (
	defaultEvalConfigName = "eval.yaml"
	defaultEvalName       = "smoke-core"
	defaultEvalSamples    = 15
	defaultEvalModel      = "gpt-4o"
)

// Type aliases to avoid repeating full package paths throughout the eval code.
type evalConfig = eval_api.EvalConfig
type evalAgentRef = opt_eval.AgentRef
type evalDatasetRef = opt_eval.DatasetRef

// evalResolvedContext holds the fully-resolved context for an eval operation,
// including the azd client, API clients, project paths, and agent metadata.
// Built by resolveEvalContext from azd project state, environment variables,
// or interactive prompts.
type evalResolvedContext struct {
	azdClient             *azdext.AzdClient
	evalClient            *eval_api.EvalClient
	datasetClient         *dataset_api.DatasetClient
	projectRoot           string               // azd project root directory
	hasProject            bool                 // true if running within an azd project
	agentProject          string               // agent service directory
	agentProjectSource    string               // how agentProject was resolved
	agentName             string               // deployed agent name
	agentNameSource       string               // how agentName was resolved
	version               string               // agent version
	versionSource         string               // how version was resolved
	agentKind             agent_yaml.AgentKind // hosted or prompt
	agentKindSource       string               // how agentKind was resolved
	serviceName           string               // azure.yaml service name
	projectEndpoint       string               // Foundry project endpoint URL
	projectEndpointSource string               // how projectEndpoint was resolved
	envName               string               // azd environment name
}

// evalContextOptions configures the behavior of resolveEvalContext.
type evalContextOptions struct {
	envName         string // explicit environment name (from -e flag)
	agent           string // explicit agent name (from --agent flag)
	projectEndpoint string // explicit project endpoint (from --project-endpoint flag)
	requireAgent    bool   // fail if agent name cannot be resolved
	noPrompt        bool   // skip interactive prompts
}

func newEvalCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval <command>",
		Short: "Create and run quick evals for an agent.",
		Long: `Create and run quick evals for an agent.

Subcommands:
  generate  Generate an eval config and dataset from a hosted agent
  run       Execute an evaluation run from eval.yaml
  update    Update an existing eval configuration
  list      List evaluations for the current project
  show      Show details of an evaluation run`,
	}

	cmd.AddCommand(newEvalGenerateCommand(extCtx))
	cmd.AddCommand(newDeprecatedEvalInitCommand())
	cmd.AddCommand(newEvalRunCommand(extCtx))
	cmd.AddCommand(newEvalUpdateCommand(extCtx))
	cmd.AddCommand(newEvalListCommand(extCtx))
	cmd.AddCommand(newEvalShowCommand(extCtx))

	return cmd
}

// newDeprecatedEvalInitCommand returns a hidden "init" command that tells users
// to use "eval generate" instead. This preserves discoverability during the
// deprecation period without silently accepting the old name.
func newDeprecatedEvalInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:        "init",
		Short:      "(deprecated) Use 'eval generate' instead.",
		Hidden:     true,
		Deprecated: "use 'azd ai agent eval generate' instead",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf(
				"'eval init' has been renamed to 'eval generate'.\n\n" +
					"Please run: azd ai agent eval generate")
		},
	}
}

// resolveEvalContext resolves the context for an eval operation by reading azd project state,
// environment variables, and optionally prompting the user. It returns an evalResolvedContext
// with API clients and metadata needed to run eval commands.
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
	if env := getExistingEnvironment(ctx, options.envName, azdClient); env != nil {
		envName = env.Name
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
		if v := getEnvValue("FOUNDRY_PROJECT_ENDPOINT"); v != "" {
			projectEndpoint = v
			projectEndpointSource = "FOUNDRY_PROJECT_ENDPOINT"
		}
	}
	if projectEndpoint == "" {
		if v := getEnvValue("AZURE_AI_PROJECT_ENDPOINT"); v != "" { // deprecated fallback
			projectEndpoint = v
			projectEndpointSource = "AZURE_AI_PROJECT_ENDPOINT (deprecated)"
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

// pollEvalOperationWithSpinner polls a long-running eval operation with a spinner, updating the provided evalProgress with status. It returns the completed job or an error if the operation failed or timed out.
func pollEvalOperationWithSpinner(
	ctx context.Context,
	label string,
	operationID string,
	get eval_api.GetJobFunc,
	apiVersion string,
	progress *evalProgress,
) (*eval_api.GenerationJob, error) {
	if operationID == "" {
		return nil, fmt.Errorf("%s did not return an operation ID", strings.ToLower(label))
	}

	progress.setRunning(label, operationID)
	poller := eval_api.NewPoller(operationID, apiVersion, get)
	job, err := poller.Poll(ctx)

	if err != nil {
		if _, ok := errors.AsType[*eval_api.PollerTimeoutError](err); ok {
			progress.setTimedOut(label)
			return nil, err
		}
		if jfe, ok := errors.AsType[*eval_api.JobFailedError](err); ok {
			if body, marshalErr := json.MarshalIndent(jfe.Job, "", "  "); marshalErr == nil {
				log.Printf("[debug] %s: failed response:\n%s", label, body)
			}
			progress.setFailed(label)
			errMsg := fmt.Sprintf("%s failed with status %q", strings.ToLower(label), jfe.Status)
			if jfe.Job != nil && jfe.Job.Error != nil && jfe.Job.Error.Message != "" {
				errMsg += ": " + jfe.Job.Error.Message
			}
			return nil, fmt.Errorf("%s", errMsg)
		}
		progress.setFailed(label)
		return nil, err
	}

	progress.setDone(label)
	return job, nil
}

func relPathForYaml(baseDir string, target string) string {
	if rel, err := filepath.Rel(baseDir, target); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(target)
}
