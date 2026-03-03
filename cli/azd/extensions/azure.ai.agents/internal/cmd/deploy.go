// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

type deployFlags struct {
	service string
}

func newDeployCommand() *cobra.Command {
	flags := &deployFlags{}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy your agent to Azure AI Foundry.",
		Long: `Deploy your agent to Azure AI Foundry.

This command runs 'azd deploy' scoped to your agent service. It
automatically detects the azure.ai.agent service from azure.yaml and
passes --service to deploy only that service (not the full project).

The deployment lifecycle (build → push → deploy) runs through the azd
extension hooks, using the service configuration from azure.yaml and the
agent definition from agent.yaml.`,
		Example: `  # Deploy the agent service (auto-detected from azure.yaml)
  azd ai agent deploy

  # Deploy a specific agent service by name
  azd ai agent deploy --service my-agent`,
		RunE: func(cmd *cobra.Command, args []string) error {
			setupDebugLogging(cmd.Flags())

			return runDeploy(cmd.Context(), flags)
		},
	}

	cmd.Flags().StringVar(&flags.service, "service", "", "Name of the agent service to deploy (from azure.yaml)")

	return cmd
}

func runDeploy(ctx context.Context, flags *deployFlags) error {
	serviceName := flags.service

	// Auto-detect the agent service name from azure.yaml if not provided
	if serviceName == "" {
		info, err := resolveAgentServiceFromProject(ctx, "")
		if err != nil {
			return fmt.Errorf(
				"could not detect agent service from azure.yaml: %w\n\n"+
					"Use --service to specify the service name explicitly", err)
		}
		serviceName = info.ServiceName
		fmt.Fprintf(os.Stderr, "Detected agent service: %s\n", serviceName)
	}

	args := []string{"deploy", "--service", serviceName}

	fmt.Fprintf(os.Stderr, "Running: azd %s\n\n", strings.Join(args, " "))

	azdCmd := exec.Command("azd", args...)
	azdCmd.Stdout = os.Stdout
	azdCmd.Stderr = os.Stderr
	azdCmd.Stdin = os.Stdin

	if err := azdCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("azd deploy failed with exit code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("failed to run azd deploy: %w", err)
	}

	return nil
}
