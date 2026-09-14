// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type agentDeployFlags struct {
	projectEndpoint string
	codePath        string
	service         string
	dryRun          bool
}

func newAgentDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &agentDeployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy [agent.yaml]",
		Short: "Preview a hosted-agent deployment.",
		Long: `Preview the hosted-agent deployment that azd would perform.

This command requires --dry-run and never deploys an agent. With no path, it
reads an azure.ai.agent service from azure.yaml. Legacy projects may pass an
agent.yaml path or run from a directory that contains agent.yaml but no
azure.yaml.

Use 'azd deploy <service>' to perform the real deployment.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flags.dryRun {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"azd ai agent deploy only supports deployment previews",
					"add --dry-run, or use 'azd deploy <service>' to deploy the agent",
				)
			}
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
