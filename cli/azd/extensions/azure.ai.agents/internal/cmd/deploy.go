// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type agentDeployFlags struct {
	projectEndpoint string
	codePath        string
	service         string
	dryRun          bool
}

type dependencyCommandRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type azdDependencyCommandRunner struct{}

func (azdDependencyCommandRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	// #nosec G702 -- the executable is fixed and callers construct the azd argument list without a shell.
	command := exec.CommandContext(ctx, "azd", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("azd %s failed: %s", strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}

type standaloneAgentPreparer func(
	context.Context,
	project.DirectDeployOptions,
) (*project.PreparedStandaloneHostedAgent, error)

type preparedStandaloneAgentDeployer func(
	context.Context,
	*project.PreparedStandaloneHostedAgent,
	map[string]string,
) (*project.DirectDeployResult, error)

func newAgentDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &agentDeployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy an agent or preview a hosted-agent deployment.",
		Long: `Deploy an agent definition to the configured Foundry project.

The path defaults to ./agent.yaml. A hosted agent uploads source code from the
definition directory unless --code specifies another path. If toolbox.yaml is
present next to agent.yaml, it is deployed first through the toolbox extension.

With --dry-run and no path, the hosted-agent service is read from azure.yaml.
Legacy projects that keep agent.yaml in the service directory are also supported.
The dry run validates and packages local inputs, compares them with the latest
deployed version when possible, and performs no remote mutations.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flags.dryRun && cmd.Flags().Changed("service") {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"--service is only supported with --dry-run",
					"add --dry-run or remove --service",
				)
			}
			if flags.dryRun {
				if len(args) > 0 && cmd.Flags().Changed("service") {
					return exterrors.Validation(
						exterrors.CodeConflictingArguments,
						"--service cannot be used with an explicit agent definition path",
						"use either --service with azure.yaml or pass an agent.yaml path",
					)
				}
				var definitionPath string
				if len(args) == 1 {
					definitionPath = args[0]
				}
				return runAgentDeployDryRun(
					cmd.Context(),
					definitionPath,
					*flags,
					extCtx.OutputFormat,
					cmd.OutOrStdout(),
				)
			}
			path := "agent.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			return runAgentDeploy(
				cmd.Context(), path, *flags, extCtx.OutputFormat,
				azdDependencyCommandRunner{},
				project.PrepareStandaloneHostedAgent,
				project.DeployPreparedStandaloneHostedAgent,
			)
		},
	}
	cmd.Flags().StringVarP(
		&flags.projectEndpoint, "project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env and project config).",
	)
	cmd.Flags().StringVar(&flags.codePath, "code", "", "Path to the hosted-agent source directory.")
	cmd.Flags().StringVar(&flags.service, "service", "", "Agent service name from azure.yaml.")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Preview the hosted-agent deployment without making changes.")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func runAgentDeployDryRun(
	ctx context.Context,
	definitionPath string,
	flags agentDeployFlags,
	output string,
	writer io.Writer,
) error {
	options := project.AgentDeployPlanOptions{
		DefinitionPath: definitionPath,
		CodePath:       flags.codePath,
	}

	var azdClient *azdext.AzdClient
	if definitionPath == "" {
		var err error
		azdClient, err = azdext.NewAzdClient()
		if err != nil {
			return exterrors.Dependency(
				exterrors.CodeProjectNotFound,
				fmt.Sprintf("failed to connect to azd while reading azure.yaml: %s", err),
				"run the command from an azd project or pass an explicit agent.yaml path",
			)
		}
		defer azdClient.Close()

		serviceConfig, projectConfig, err := resolveAgentService(
			ctx,
			azdClient,
			flags.service,
			true,
		)
		if err != nil {
			legacyDefinition := legacyAgentDefinitionInCurrentDirectory()
			if projectFileExistsInCurrentDirectory() || legacyDefinition == "" {
				return err
			}
			definitionPath = legacyDefinition
			options.DefinitionPath = legacyDefinition
		} else {
			options.ServiceConfig = serviceConfig
			options.ProjectRoot = projectConfig.GetPath()

			if environment, envErr := loadAzdEnvironment(ctx, azdClient); envErr == nil {
				options.Environment = environment
				options.ProjectEndpoint = environment["FOUNDRY_PROJECT_ENDPOINT"]
			}
			if options.ProjectEndpoint == "" {
				if reference := brownfieldInlineAgentReference(serviceConfig, projectConfig); reference != nil {
					options.ProjectEndpoint = reference.projectEndpoint
				}
			}
		}
	}

	if strings.TrimSpace(flags.projectEndpoint) != "" {
		endpoint, _, err := validateProjectEndpoint(flags.projectEndpoint)
		if err != nil {
			return err
		}
		options.ProjectEndpoint = endpoint
	} else if options.ProjectEndpoint == "" && definitionPath != "" {
		resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
		if err == nil {
			options.ProjectEndpoint = resolved.Endpoint
		} else if localError, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localError.Code != exterrors.CodeMissingProjectEndpoint {
			return err
		}
	}

	plan, err := project.PlanAgentDeploy(ctx, options)
	if err != nil {
		return err
	}
	if output == "json" {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		_, err = fmt.Fprintln(writer, string(data))
		return err
	}
	return writeAgentDeployPlan(writer, plan)
}

func projectFileExistsInCurrentDirectory() bool {
	for _, name := range []string{"azure.yaml", "azure.yml"} {
		if _, err := os.Stat(name); err == nil {
			return true
		}
	}
	return false
}

func legacyAgentDefinitionInCurrentDirectory() string {
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return ""
}

func writeAgentDeployPlan(writer io.Writer, plan *project.AgentDeployPlan) error {
	if _, err := fmt.Fprintf(
		writer,
		"Agent             %s\nAction            %s\nRemote comparison %s\nSource            %s\n",
		plan.Agent,
		plan.Action,
		plan.RemoteComparison,
		plan.Source,
	); err != nil {
		return err
	}
	if len(plan.Changes) == 0 {
		if _, err := fmt.Fprintln(writer, "\nNo configuration changes."); err != nil {
			return err
		}
		if plan.Action == "createVersion" {
			if _, err := fmt.Fprintln(writer, "Deploy would still create a new immutable agent version."); err != nil {
				return err
			}
		}
	} else {
		if _, err := fmt.Fprintln(writer, "\nGroup            Field                              Change"); err != nil {
			return err
		}
		for _, change := range plan.Changes {
			if _, err := fmt.Fprintf(writer, "%-16s %-34s %s\n", change.Group, change.Field, change.Change); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(writer, "\nDry run complete. No changes were made.")
	return err
}

func runAgentDeploy(
	ctx context.Context,
	definitionPath string,
	flags agentDeployFlags,
	output string,
	runner dependencyCommandRunner,
	preparer standaloneAgentPreparer,
	deployer preparedStandaloneAgentDeployer,
) error {
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{FlagValue: flags.projectEndpoint})
	if err != nil {
		return err
	}
	toolbox, err := loadAgentToolboxReference(definitionPath)
	if err != nil {
		return err
	}
	prepared, err := preparer(ctx, project.DirectDeployOptions{
		DefinitionPath:  definitionPath,
		CodePath:        flags.codePath,
		ProjectEndpoint: resolved.Endpoint,
		Progress: func(message string) {
			if output != "json" {
				fmt.Fprintln(os.Stderr, message)
			}
		},
	})
	if err != nil {
		return err
	}
	environment, err := deployAgentToolboxDependency(
		ctx, runner, resolved.Endpoint, definitionPath, toolbox,
	)
	if err != nil {
		return err
	}

	result, err := deployer(ctx, prepared, environment)
	if err != nil {
		return err
	}
	if output == "json" {
		return emitJSON(result)
	}
	fmt.Printf("Name     %s\n", result.Name)
	fmt.Printf("Version  %s\n", result.Version)
	fmt.Printf("State    %s\n", result.State)
	if result.Endpoint != "" {
		fmt.Printf("Endpoint %s\n", result.Endpoint)
	}
	return nil
}

type toolboxDeployOutput struct {
	Toolbox  string `json:"toolbox"`
	Version  string `json:"version"`
	Endpoint string `json:"endpoint"`
}

func deployAgentToolboxDependency(
	ctx context.Context,
	runner dependencyCommandRunner,
	projectEndpoint string,
	agentDefinitionPath string,
	reference *agent_yaml.ToolboxReference,
) (map[string]string, error) {
	if reference == nil {
		return nil, nil
	}
	name := strings.TrimSpace(reference.Name)
	if name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			"agent toolbox reference requires a name",
			"set toolbox.name in agent.yaml",
		)
	}
	environment := map[string]string{"TOOLBOX_NAME": name}
	if version := strings.TrimSpace(reference.Version); version != "" {
		environment["TOOLBOX_VERSION"] = version
		environment["TOOLBOX_ENDPOINT"] = fmt.Sprintf(
			"%s/toolboxes/%s/versions/%s/mcp?api-version=v1",
			strings.TrimRight(projectEndpoint, "/"),
			url.PathEscape(name),
			url.PathEscape(version),
		)
		return environment, nil
	}

	toolboxPath := filepath.Join(filepath.Dir(agentDefinitionPath), "toolbox.yaml")
	if _, err := os.Stat(toolboxPath); err != nil {
		if os.IsNotExist(err) {
			return environment, nil
		}
		return nil, exterrors.Dependency(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("failed to inspect toolbox definition %q: %s", toolboxPath, err),
			"verify the toolbox definition permissions and retry",
		)
	}
	toolboxName, err := toolboxDefinitionName(toolboxPath)
	if err != nil {
		return nil, err
	}
	if toolboxName != name {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox definition %q declares %q but agent.yaml references %q", toolboxPath, toolboxName, name),
			"make toolbox.name match the agent toolbox reference",
		)
	}

	stdout, err := runner.Run(
		ctx,
		"ai", "toolbox", "deploy", toolboxPath,
		"--project-endpoint", projectEndpoint,
		"--output", "json",
		"--no-prompt",
	)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("failed to deploy toolbox %q: %s", name, err),
			"fix the toolbox definition or its connection dependencies, then retry agent deploy",
		)
	}
	var deployed toolboxDeployOutput
	if err := json.Unmarshal(stdout, &deployed); err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox deploy returned invalid JSON: %s", err),
		)
	}
	if deployed.Toolbox != name || strings.TrimSpace(deployed.Endpoint) == "" {
		return nil, exterrors.Internal(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox deploy returned an incomplete result for %q", name),
		)
	}
	environment["TOOLBOX_VERSION"] = deployed.Version
	environment["TOOLBOX_ENDPOINT"] = deployed.Endpoint
	return environment, nil
}

func toolboxDefinitionName(path string) (string, error) {
	// #nosec G304 -- reading a sibling toolbox definition is intentional.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var value struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &value); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox definition %q is invalid: %s", path, err),
			"fix toolbox.yaml and retry",
		)
	}
	if strings.TrimSpace(value.Name) == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox definition %q does not declare a name", path),
			"set name in toolbox.yaml and retry",
		)
	}
	return strings.TrimSpace(value.Name), nil
}
