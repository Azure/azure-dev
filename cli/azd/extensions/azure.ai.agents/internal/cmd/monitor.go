// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type monitorFlags struct {
	accountName string
	projectName string
	name        string
	version     string
	follow      bool
	tail        int
	logType     string
}

// MonitorAction handles the execution of the monitor command.
type MonitorAction struct {
	*AgentContext
	flags *monitorFlags
}

func newMonitorCommand() *cobra.Command {
	flags := &monitorFlags{}

	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Monitor logs from a hosted agent container.",
		Long: `Monitor logs from a hosted agent container.

Streams console output (stdout/stderr) or system events from an agent container.
Use --follow to stream logs in real-time, or omit it to fetch recent logs and exit.
This is useful for troubleshooting agent startup issues or monitoring agent behavior.`,
		Example: `  # Fetch the last 50 lines of console logs
  azd ai agent monitor --name my-agent --version 1

  # Stream console logs in real-time
  azd ai agent monitor --name my-agent --version 1 --follow

  # Fetch system event logs
  azd ai agent monitor --name my-agent --version 1 --type system

  # Fetch last 100 lines with explicit account
  azd ai agent monitor --name my-agent --version 1 --tail 100 --account-name myAccount --project-name myProject`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMonitorFlags(flags); err != nil {
				return err
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			agentContext, err := newAgentContext(ctx, flags.accountName, flags.projectName, flags.name, flags.version)
			if err != nil {
				return err
			}

			action := &MonitorAction{
				AgentContext: agentContext,
				flags:       flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.accountName, "account-name", "a", "", "Cognitive Services account name")
	cmd.Flags().StringVarP(&flags.projectName, "project-name", "p", "", "AI Foundry project name")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Name of the hosted agent (required)")
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "Version of the hosted agent (required)")
	cmd.Flags().BoolVarP(&flags.follow, "follow", "f", false, "Stream logs in real-time")
	cmd.Flags().IntVarP(&flags.tail, "tail", "l", 50, "Number of trailing log lines to fetch (1-300)")
	cmd.Flags().StringVarP(&flags.logType, "type", "t", "console", "Type of logs: 'console' (stdout/stderr) or 'system' (container events)")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("version")

	return cmd
}

// Run executes the monitor command logic.
func (a *MonitorAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	body, err := agentClient.GetAgentContainerLogStream(
		ctx,
		a.Name,
		a.Version,
		DefaultAgentAPIVersion,
		a.flags.logType,
		a.flags.tail,
	)
	if err != nil {
		return fmt.Errorf("failed to get agent logs: %w", err)
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading log stream: %w", err)
	}

	return nil
}

func validateMonitorFlags(flags *monitorFlags) error {
	if flags.tail < 1 || flags.tail > 300 {
		return fmt.Errorf("--tail must be between 1 and 300, got %d", flags.tail)
	}

	if flags.logType != "console" && flags.logType != "system" {
		return fmt.Errorf("--type must be 'console' or 'system', got '%s'", flags.logType)
	}

	return nil
}
