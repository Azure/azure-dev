// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type standaloneAgentDeployer func(
	context.Context,
	project.DirectDeployOptions,
) (*project.DirectDeployResult, error)

func newAgentDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &agentDeployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy an agent directly from agent.yaml.",
		Long: `Deploy an agent definition to the configured Foundry project.

The path defaults to ./agent.yaml. A hosted agent uploads source code from the
definition directory unless --code specifies another path. If toolbox.yaml is
present next to agent.yaml, it is deployed first through the toolbox extension.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "agent.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			return runAgentDeploy(
				cmd.Context(), path, *flags, extCtx.OutputFormat,
				azdDependencyCommandRunner{}, project.DeployStandaloneHostedAgent,
			)
		},
	}
	cmd.Flags().StringVarP(
		&flags.projectEndpoint, "project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env and project config).",
	)
	cmd.Flags().StringVar(&flags.codePath, "code", "", "Path to the hosted-agent source directory.")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func runAgentDeploy(
	ctx context.Context,
	definitionPath string,
	flags agentDeployFlags,
	output string,
	runner dependencyCommandRunner,
	deployer standaloneAgentDeployer,
) error {
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{FlagValue: flags.projectEndpoint})
	if err != nil {
		return err
	}
	toolbox, err := loadAgentToolboxReference(definitionPath)
	if err != nil {
		return err
	}
	environment, err := deployAgentToolboxDependency(
		ctx, runner, resolved.Endpoint, definitionPath, toolbox,
	)
	if err != nil {
		return err
	}

	result, err := deployer(ctx, project.DirectDeployOptions{
		DefinitionPath:  definitionPath,
		CodePath:        flags.codePath,
		ProjectEndpoint: resolved.Endpoint,
		Environment:     environment,
		Progress: func(message string) {
			if output != "json" {
				fmt.Fprintln(os.Stderr, message)
			}
		},
	})
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
			strings.TrimRight(projectEndpoint, "/"), name, version,
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
