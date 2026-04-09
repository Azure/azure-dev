// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// sessionFlags holds common flags shared by all session subcommands.
type sessionFlags struct {
	agentName string
	output    string
}

func newSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "sessions",
		Short:  "Manage sessions for a hosted agent endpoint.",
		Hidden: !isVNextEnabled(context.Background()),
		Long: `Manage sessions for a hosted agent endpoint.

Create, show, list, and delete hosted agent sessions.
Sessions provide persistent compute and filesystem state for 
hosted agent invocations.

Agent details are automatically resolved from the azd environment.
Use --agent-name to select a specific agent when the project has
multiple azure.ai.agent services.`,
	}

	// PersistentPreRunE is set outside the struct literal so the closure
	// captures the outer cmd variable. When a subcommand runs (e.g.
	// "sessions create"), Cobra passes the leaf command as the function
	// parameter. Using cmd.Parent() here reaches the root command;
	// using the parameter's Parent() would return this session command
	// itself, causing infinite recursion.
	cmd.PersistentPreRunE = func(childCmd *cobra.Command, args []string) error {
		if parent := cmd.Parent(); parent != nil &&
			parent.PersistentPreRunE != nil {
			if err := parent.PersistentPreRunE(childCmd, args); err != nil {
				return err
			}
		}

		ctx := azdext.WithAccessToken(childCmd.Context())
		if !isVNextEnabled(ctx) {
			return fmt.Errorf(
				"session commands require hosted agent vnext to be enabled\n\n" +
					"Set 'enableHostedAgentVNext' to 'true' in your azd " +
					"environment or as an OS environment variable.",
			)
		}
		return nil
	}

	cmd.AddCommand(newSessionCreateCommand())
	cmd.AddCommand(newSessionShowCommand())
	cmd.AddCommand(newSessionDeleteCommand())
	cmd.AddCommand(newSessionListCommand())

	return cmd
}

// addSessionFlags registers the common flags on a cobra command.
func addSessionFlags(cmd *cobra.Command, flags *sessionFlags) {
	cmd.Flags().StringVarP(
		&flags.agentName, "agent-name", "n", "",
		"Agent name (matches azure.yaml service name; "+
			"auto-detected when only one exists)",
	)
	cmd.Flags().StringVarP(
		&flags.output, "output", "o", "json",
		"Output format (json or table)",
	)
}

// sessionContext holds the resolved agent context for session operations.
type sessionContext struct {
	endpoint  string
	agentName string
	version   string // from AGENT_{SERVICE}_VERSION env var
}

// resolveSessionContext resolves the agent name, version, and project endpoint.
func resolveSessionContext(
	ctx context.Context, agentName string,
) (*sessionContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	name := agentName
	var version string

	if info, err := resolveAgentServiceFromProject(
		ctx, azdClient, name, rootFlags.NoPrompt,
	); err == nil {
		if name == "" && info.AgentName != "" {
			name = info.AgentName
		}
		if info.Version != "" {
			version = info.Version
		}
	}

	if name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"agent name is required but could not be resolved",
			"provide --agent-name or define an azure.ai.agent "+
				"service in azure.yaml and run 'azd up'",
		)
	}

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return nil, err
	}

	return &sessionContext{
		endpoint:  endpoint,
		agentName: name,
		version:   version,
	}, nil
}

// ---------------------------------------------------------------------------
// session create
// ---------------------------------------------------------------------------

type sessionCreateFlags struct {
	sessionFlags
	sessionID    string
	version      string
	isolationKey string
}

func newSessionCreateCommand() *cobra.Command {
	flags := &sessionCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create [agent-name] [version] [isolation-key]",
		Short: "Create a new session for a hosted agent.",
		Long: `Create a new session for a hosted agent endpoint.

Provisions a session with a persistent filesystem. The session
is ready for invocations once the command completes.

The agent name is auto-detected when only one azure.ai.agent service exists
in azure.yaml. The version defaults to the deployed agent version from the
azd environment (AGENT_{SERVICE}_VERSION) when omitted.
The isolation key is derived from the Entra token by default.

Positional arguments can be used instead of flags:
  azd ai agent sessions create [agent-name] [version] [isolation-key]`,
		Example: `  # Create a session (auto-detect agent, latest version)
  azd ai agent sessions create

  # Create a session for a specific agent
  azd ai agent sessions create my-agent

  # Create a session backed by agent version 3
  azd ai agent sessions create my-agent 3

  # Create with flags
  azd ai agent sessions create --agent-name my-agent --version 3

  # Create with a specific session ID
  azd ai agent sessions create --session-id my-session`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			// Positional args fill in missing flags: [agent-name] [version] [isolation-key]
			switch len(args) {
			case 3:
				if flags.isolationKey == "" {
					flags.isolationKey = args[2]
				}
				fallthrough
			case 2:
				if flags.version == "" {
					flags.version = args[1]
				}
				fallthrough
			case 1:
				if flags.agentName == "" {
					flags.agentName = args[0]
				}
			}

			sc, err := resolveSessionContext(ctx, flags.agentName)
			if err != nil {
				return err
			}

			// Resolve version: flag > env var > error
			version := flags.version
			if version == "" {
				version = sc.version
			}
			if version == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentVersion,
					"agent version is required to create a session "+
						"but could not be resolved",
					"provide --version, pass the version as a "+
						"positional argument, or deploy the agent "+
						"with 'azd up' to set it automatically",
				)
			}

			credential, err := newAgentCredential()
			if err != nil {
				return err
			}

			client := agent_api.NewAgentClient(
				sc.endpoint, credential,
			)

			request := &agent_api.CreateAgentSessionRequest{
				VersionIndicator: &agent_api.VersionIndicator{
					Type:         "version_ref",
					AgentVersion: version,
				},
			}
			if flags.sessionID != "" {
				request.AgentSessionID = &flags.sessionID
			}

			session, err := client.CreateSession(
				ctx,
				sc.agentName,
				flags.isolationKey,
				request,
				DefaultVNextAgentAPIVersion,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(
					err, exterrors.OpCreateSession,
				)
			}

			// Persist session ID for reuse by invoke
			persistSessionID(ctx, sc.agentName, session.AgentSessionID)

			return printSession(session, flags.output)
		},
	}

	addSessionFlags(cmd, &flags.sessionFlags)
	cmd.Flags().StringVar(
		&flags.sessionID, "session-id", "",
		"Optional caller-provided session ID "+
			"(auto-generated if omitted)",
	)
	cmd.Flags().StringVar(
		&flags.version, "version", "",
		"Agent version to back the session "+
			"(auto-resolved from azd environment if omitted)",
	)
	cmd.Flags().StringVar(
		&flags.isolationKey, "isolation-key", "",
		"Isolation key for session ownership "+
			"(derived from Entra token by default)",
	)

	return cmd
}

// ---------------------------------------------------------------------------
// session show
// ---------------------------------------------------------------------------

type sessionShowFlags struct {
	sessionFlags
}

func newSessionShowCommand() *cobra.Command {
	flags := &sessionShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show details of a session.",
		Long: `Show details of a hosted agent session.

Retrieves the current status, version indicator, and timestamps for the
specified session.`,
		Example: `  # Show session details
  azd ai agent sessions show my-session

  # Show in table format
  azd ai agent sessions show my-session --output table`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			sessionID := args[0]

			sc, err := resolveSessionContext(ctx, flags.agentName)
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return err
			}

			client := agent_api.NewAgentClient(
				sc.endpoint, credential,
			)

			session, err := client.GetSession(
				ctx,
				sc.agentName,
				sessionID,
				DefaultVNextAgentAPIVersion,
			)
			if err != nil {
				if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
					respErr.StatusCode == http.StatusNotFound {
					return exterrors.Validation(
						exterrors.CodeSessionNotFound,
						fmt.Sprintf(
							"session %q not found or has been deleted",
							sessionID,
						),
						"use 'azd ai agent sessions list' to see "+
							"available sessions",
					)
				}
				return exterrors.ServiceFromAzure(
					err, exterrors.OpGetSession,
				)
			}

			return printSession(session, flags.output)
		},
	}

	addSessionFlags(cmd, &flags.sessionFlags)

	return cmd
}

// ---------------------------------------------------------------------------
// session delete
// ---------------------------------------------------------------------------

type sessionDeleteFlags struct {
	sessionFlags
	isolationKey string
}

func newSessionDeleteCommand() *cobra.Command {
	flags := &sessionDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete a session.",
		Long: `Delete a hosted agent session synchronously.

Terminates the hosted agent session and deletes the persistent filesystem
volume. Returns once cleanup is complete.

The isolation key is derived from the Entra token by default.`,
		Example: `  # Delete a session
  azd ai agent sessions delete my-session

  # Delete with an explicit isolation key
  azd ai agent sessions delete my-session --isolation-key sk-abc123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			sessionID := args[0]

			sc, err := resolveSessionContext(ctx, flags.agentName)
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return err
			}

			client := agent_api.NewAgentClient(
				sc.endpoint, credential,
			)

			err = client.DeleteSession(
				ctx,
				sc.agentName,
				sessionID,
				flags.isolationKey,
				DefaultVNextAgentAPIVersion,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(
					err, exterrors.OpDeleteSession,
				)
			}

			fmt.Printf(
				"Session %q deleted from agent %q.\n",
				sessionID, sc.agentName,
			)
			return nil
		},
	}

	addSessionFlags(cmd, &flags.sessionFlags)
	cmd.Flags().StringVar(
		&flags.isolationKey, "isolation-key", "",
		"Isolation key for session ownership "+
			"(derived from Entra token by default)",
	)

	return cmd
}

// ---------------------------------------------------------------------------
// session list
// ---------------------------------------------------------------------------

type sessionListFlags struct {
	sessionFlags
	limit           int32
	paginationToken string
}

func newSessionListCommand() *cobra.Command {
	flags := &sessionListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions for a hosted agent.",
		Long: `List sessions for a hosted agent endpoint.

Returns a paged list of sessions with their status, version, and timestamps.`,
		Example: `  # List all sessions
  azd ai agent sessions list

  # List with a page size limit
  azd ai agent sessions list --limit 10

  # List in table format
  azd ai agent sessions list --output table`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			sc, err := resolveSessionContext(ctx, flags.agentName)
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return err
			}

			client := agent_api.NewAgentClient(
				sc.endpoint, credential,
			)

			var limit *int32
			if cmd.Flags().Changed("limit") {
				limit = &flags.limit
			}

			var token *string
			if flags.paginationToken != "" {
				token = &flags.paginationToken
			}

			result, err := client.ListSessions(
				ctx,
				sc.agentName,
				limit,
				token,
				DefaultVNextAgentAPIVersion,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(
					err, exterrors.OpListSessions,
				)
			}

			return printSessionList(result, flags.output)
		},
	}

	addSessionFlags(cmd, &flags.sessionFlags)
	cmd.Flags().Int32Var(
		&flags.limit, "limit", 0,
		"Maximum number of sessions to return",
	)
	cmd.Flags().StringVar(
		&flags.paginationToken, "pagination-token", "",
		"Continuation token from a previous list response",
	)

	return cmd
}

// ---------------------------------------------------------------------------
// Output formatting
// ---------------------------------------------------------------------------

func printSession(
	session *agent_api.AgentSessionResource, format string,
) error {
	switch format {
	case "table":
		return printSessionTable(session)
	default:
		return printSessionJSON(session)
	}
}

func printSessionJSON(session *agent_api.AgentSessionResource) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session to JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printSessionTable(session *agent_api.AgentSessionResource) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	fmt.Fprintf(w, "Session ID\t%s\n", session.AgentSessionID)
	fmt.Fprintf(w, "Status\t%s\n", session.Status)
	fmt.Fprintf(
		w, "Version\t%s (type: %s)\n",
		session.VersionIndicator.AgentVersion,
		session.VersionIndicator.Type,
	)
	fmt.Fprintf(
		w, "Created At\t%s\n", formatUnixTimestamp(session.CreatedAt),
	)
	fmt.Fprintf(
		w, "Last Accessed\t%s\n",
		formatUnixTimestamp(session.LastAccessedAt),
	)
	fmt.Fprintf(
		w, "Expires At\t%s\n", formatUnixTimestamp(session.ExpiresAt),
	)

	return w.Flush()
}

func printSessionList(
	result *agent_api.SessionListResult, format string,
) error {
	switch format {
	case "table":
		return printSessionListTable(result)
	default:
		return printSessionListJSON(result)
	}
}

func printSessionListJSON(result *agent_api.SessionListResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"failed to marshal session list to JSON: %w", err,
		)
	}
	fmt.Println(string(data))
	return nil
}

func printSessionListTable(result *agent_api.SessionListResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(
		w,
		"SESSION ID\tSTATUS\tVERSION\tCREATED\tLAST ACCESSED\tEXPIRES",
	)
	fmt.Fprintln(
		w,
		"----------\t------\t-------\t-------\t-------------\t-------",
	)

	for _, s := range result.Data {
		fmt.Fprintf(
			w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.AgentSessionID,
			s.Status,
			s.VersionIndicator.AgentVersion,
			formatUnixTimestamp(s.CreatedAt),
			formatUnixTimestamp(s.LastAccessedAt),
			formatUnixTimestamp(s.ExpiresAt),
		)
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if result.PaginationToken != nil && *result.PaginationToken != "" {
		fmt.Printf(
			"\nMore results available. "+
				"Use --pagination-token %q to fetch the next page.\n",
			*result.PaginationToken,
		)
	}

	return nil
}

// formatUnixTimestamp converts a Unix epoch timestamp (seconds) to a
// human-readable UTC string. Returns "-" for zero values.
func formatUnixTimestamp(epoch int64) string {
	if epoch == 0 {
		return "-"
	}
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

// persistSessionID saves the session ID to .foundry-agent.json for reuse.
func persistSessionID(ctx context.Context, agentName, sessionID string) {
	if sessionID == "" {
		return
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	saveContextValue(ctx, azdClient, agentName, sessionID, "sessions")
}
