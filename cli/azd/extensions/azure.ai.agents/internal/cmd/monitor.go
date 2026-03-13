// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type monitorFlags struct {
	accountName string
	projectName string
	name        string
	version     string
	sessionID   string
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
		Short: "Monitor logs from a hosted agent.",
		Long: `Monitor logs from a hosted agent.

Streams console output (stdout/stderr) or system events from an agent session or container.
Use --session to stream logs for a specific session, or omit it to use the container logstream.
Use --follow to stream logs in real-time, or omit it to fetch recent logs and exit.
This is useful for troubleshooting agent startup issues or monitoring agent behavior.`,
		Example: `  # Stream session logs
  azd ai agent monitor --name my-agent --version 1 --session <session-id>

  # Stream session logs in real-time
  azd ai agent monitor --name my-agent --version 1 --session <session-id> --follow

  # Fetch container console logs (legacy)
  azd ai agent monitor --name my-agent --version 1

  # Fetch system event logs from container (legacy)
  azd ai agent monitor --name my-agent --version 1 --type system`,
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

			// When vnext is enabled, resolve session ID for session-based logstream.
			if flags.sessionID == "" {
				sessionID, vnext := resolveMonitorSession(ctx, flags.name)
				if vnext {
					flags.sessionID = sessionID
				}
			}

			action := &MonitorAction{
				AgentContext: agentContext,
				flags:        flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.accountName, "account-name", "a", "", "Cognitive Services account name")
	cmd.Flags().StringVarP(&flags.projectName, "project-name", "p", "", "AI Foundry project name")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Name of the hosted agent (required)")
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "Version of the hosted agent (required)")
	cmd.Flags().StringVarP(&flags.sessionID, "session", "s", "", "Session ID to stream logs for")
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

	var body io.ReadCloser
	if a.flags.sessionID != "" {
		fmt.Fprintf(os.Stderr, "Streaming session logs for %s (session: %s)...\n", a.Name, a.flags.sessionID)
		body, err = agentClient.GetAgentSessionLogStream(
			ctx,
			a.Name,
			a.Version,
			a.flags.sessionID,
			DefaultVNextAgentAPIVersion,
			a.flags.logType,
			a.flags.tail,
			a.flags.follow,
		)
	} else {
		body, err = agentClient.GetAgentContainerLogStream(
			ctx,
			a.Name,
			a.Version,
			DefaultAgentAPIVersion,
			a.flags.logType,
			a.flags.tail,
			a.flags.follow,
		)
	}
	if err != nil {
		// Suppress context deadline/cancellation errors (expected in non-follow timeout and Ctrl+C)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to get agent logs: %w", err)
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		// Suppress context deadline/cancellation errors:
		// - DeadlineExceeded: expected in non-follow mode (internal timeout fires after available data is read)
		// - Canceled: expected when user presses Ctrl+C in follow mode
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
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

// resolveMonitorSession checks if vnext is enabled and resolves the session ID
// from the .foundry-agent.json file. Returns the session ID and whether vnext is enabled.
// If vnext is not enabled or the session cannot be resolved, the returned string will be empty.
func resolveMonitorSession(ctx context.Context, agentName string) (string, bool) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", false
	}
	defer azdClient.Close()

	// Check if vnext is enabled
	vnextValue := ""
	azdEnv, err := loadAzdEnvironment(ctx, azdClient)
	if err == nil {
		vnextValue = azdEnv["enableHostedAgentVNext"]
	}
	if vnextValue == "" {
		vnextValue = os.Getenv("enableHostedAgentVNext")
	}
	enabled, err := strconv.ParseBool(vnextValue)
	if err != nil || !enabled {
		return "", false
	}

	// Resolve session ID from .foundry-agent.json
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return "", true
	}
	agentCtx := loadLocalContext(configPath)
	if agentCtx.Sessions != nil {
		if sid, ok := agentCtx.Sessions[agentName]; ok {
			return sid, true
		}
	}

	return "", true
}
