// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type monitorFlags struct {
	name      string
	sessionID string
	follow    bool
	tail      int
	logType   string
}

// MonitorAction handles the execution of the monitor command.
type MonitorAction struct {
	*AgentContext
	flags *monitorFlags
}

func newMonitorCommand() *cobra.Command {
	flags := &monitorFlags{}

	cmd := &cobra.Command{
		Use:   "monitor [name]",
		Short: "Monitor logs from a hosted agent.",
		Long: `Monitor logs from a hosted agent.

Streams console output (stdout/stderr) or system events from an agent session.
A session ID is required — it is auto-resolved from the last invocation, or you can
specify one explicitly with --session-id.
Use --follow to stream logs in real-time, or omit it to fetch recent logs and exit.
This is useful for troubleshooting agent issues or monitoring agent behavior.

The agent name and version are resolved automatically from the azure.yaml service
configuration and the current azd environment. Optionally specify the service name
(from azure.yaml) as a positional argument when multiple agent services exist.`,
		Example: `  # Monitor session logs (auto-resolves session from last invocation)
  azd ai agent monitor

  # Monitor logs for a specific agent service
  azd ai agent monitor my-agent

  # Stream logs for a specific session
  azd ai agent monitor --session-id <session-id>

  # Stream session logs in real-time
  azd ai agent monitor --session-id <session-id> --follow

  # Fetch system event logs
  azd ai agent monitor --type system`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}

			if err := validateMonitorFlags(flags); err != nil {
				return err
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.name, rootFlags.NoPrompt)
			if err != nil {
				return err
			}

			if info.AgentName == "" {
				return fmt.Errorf(
					"agent name could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or check your azd environment values",
					info.ServiceName,
				)
			}

			agentContext, err := newAgentContext(ctx, "", "", info.AgentName, info.Version)
			if err != nil {
				return err
			}

			// Resolve session ID for session-based logstream.
			if flags.sessionID == "" {
				sessionID := resolveMonitorSession(ctx, info.AgentName)
				if sessionID == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidSessionId,
						"A session ID is required for log streaming.",
						"Specify the session ID using --session-id, or run `azd ai agent invoke` first to create one",
					)
				}
				flags.sessionID = sessionID
			}

			action := &MonitorAction{
				AgentContext: agentContext,
				flags:        flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.sessionID, "session-id", "s", "", "Session ID to stream logs for")
	cmd.Flags().BoolVarP(&flags.follow, "follow", "f", false, "Stream logs in real-time")
	cmd.Flags().IntVarP(&flags.tail, "tail", "l", 50, "Number of trailing log lines to fetch (1-300)")
	cmd.Flags().StringVarP(&flags.logType, "type", "t", "console",
		"Type of logs: 'console' (stdout/stderr) or 'system' (container events)")

	return cmd
}

// Run executes the monitor command logic.
func (a *MonitorAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Streaming session logs for %s (session: %s)...\n", a.Name, a.flags.sessionID)
	body, err := agentClient.GetAgentSessionLogStream(
		ctx,
		a.Name,
		a.flags.sessionID,
		DefaultVNextAgentAPIVersion,
		a.flags.logType,
		a.flags.tail,
		a.flags.follow,
	)
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

// resolveMonitorSession resolves the session ID from the .foundry-agent.json file.
// Returns the session ID if available, or empty string if not found.
func resolveMonitorSession(ctx context.Context, agentName string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	// Resolve session ID from .foundry-agent.json
	configPath, err := resolveConfigPath(ctx, azdClient)
	if err != nil {
		return ""
	}
	agentCtx := loadLocalContext(configPath)
	if agentCtx.Sessions != nil {
		if sid, ok := agentCtx.Sessions[agentName]; ok {
			return sid
		}
	}

	return ""
}
