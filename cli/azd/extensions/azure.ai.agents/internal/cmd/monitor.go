// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type monitorFlags struct {
	userIdentityFlags
	name      string
	sessionID string
	follow    bool
	tail      int
	logType   string
	utc       bool
	raw       bool
	direct    directAgentTargetFlags
	traces    monitorTraceFlags
}

// MonitorAction handles the execution of the monitor command.
type MonitorAction struct {
	*AgentContext
	flags *monitorFlags
}

const featureSessionLogs = "session logs"

func newMonitorCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &monitorFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "monitor [name]",
		Short: "Monitor logs and traces from a deployed agent.",
		Long: `Monitor logs and traces from a deployed agent.

With --type console (default) or --type system, streams console output
(stdout/stderr) or system events from a hosted agent session. A session ID is
required — it is auto-resolved from the last invocation, or you can specify one
explicitly with --session-id. Use --follow to stream logs in real-time, or omit
it to fetch recent logs and exit.

With --type traces, lists the agent's recorded OpenTelemetry traces from the
Application Insights resource connected to the Foundry project, newest first.
Use --since to set the window, --status failed to find failed interactions, and
--tail to limit how many traces are listed. Use --trace-id to export one trace
with every recorded span, including span and parent IDs, timestamps, status,
and attributes. A trace must contain a GenAI operation span with a non-empty
gen_ai.operation.name and matching gen_ai.agent.name or gen_ai.agent.id.
Infrastructure spans with inherited agent identity alone do not qualify.
Export retains all spans of a qualifying trace, including infrastructure spans.
The list shows the root span name, or the earliest matching GenAI span name
when the root is missing. Export reports whether the trace is complete and why
not (missing spans, truncation, or partial query errors) and notes sampling.
Parents in Foundry services that do not export to the project's Application
Insights are reported as upstream parents and do not make a trace incomplete.
Credential-bearing attributes are redacted; prompts and responses recorded in
spans are kept. Reading traces requires read access to the project connections
and to the Application Insights logs.

When run from an azd project, the agent name and version are resolved from the
azure.yaml service configuration and current azd environment. Optionally specify
the service name as a positional argument when multiple agent services exist.

Outside an azd project, the positional value is treated as the deployed agent name.
The Foundry project endpoint is resolved from the global project context configured
by azd ai project set, or from FOUNDRY_PROJECT_ENDPOINT.

To target a deployed agent explicitly, supply --project-endpoint with the deployed
Foundry agent name as the positional argument, or --agent-endpoint with a full
agent protocol URL. Direct targets never read azure.yaml or azd environment
values, and log streaming then requires --session-id.

For agents configured with header-based isolation, pass --user-identity
when streaming session logs.`,
		Example: `  # Monitor session logs (auto-resolves session from last invocation)
  azd ai agent monitor

  # Monitor logs for a specific agent service
  azd ai agent monitor my-agent

  # Monitor a hosted agent outside an azd project
  azd ai agent monitor my-agent --session-id <session-id>

  # Stream logs for a specific session
  azd ai agent monitor --session-id <session-id>

  # Stream session logs in real-time
  azd ai agent monitor --session-id <session-id> --follow

  # Fetch system event logs
  azd ai agent monitor --type system

  # List failed traces from the last 24 hours for a deployed agent
  azd ai agent monitor support --type traces --since 24h --status failed \
    --project-endpoint https://<account>.services.ai.azure.com/api/projects/<project>

  # Export one trace with all of its spans to a file
  azd ai agent monitor support --type traces --trace-id <trace-id> --out-file ./trace.json \
    --project-endpoint https://<account>.services.ai.azure.com/api/projects/<project>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nameSupplied := len(args) > 0
			if nameSupplied {
				flags.name = args[0]
			}

			if err := validateMonitorFlags(flags); err != nil {
				return err
			}
			traceOpts, err := validateMonitorModeFlags(cmd, flags, extCtx.OutputFormat)
			if err != nil {
				return err
			}

			target, err := resolveDirectAgentTarget(
				cmd, &flags.direct, flags.name, nameSupplied, "the [name] argument")
			if err != nil {
				return err
			}
			if target != nil && traceOpts == nil && flags.sessionID != "" {
				if err := validateDirectSessionID(
					flags.sessionID, directSessionsListCommand(&flags.direct, target)); err != nil {
					return err
				}
			}

			ctx := azdext.WithAccessToken(cmd.Context())

			if target != nil {
				if traceOpts != nil {
					return runMonitorTracesForTarget(ctx, target, traceOpts)
				}
				return runDirectMonitorLogs(ctx, flags, target)
			}

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			info, err := resolveMonitorAgentInfo(ctx, azdClient, flags.name, extCtx.NoPrompt)
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
				return monitorEndpointError(err)
			}

			if traceOpts != nil {
				return runMonitorTracesForTarget(ctx, agentContext, traceOpts)
			}

			// Resolve session ID for session-based logstream.
			if flags.sessionID == "" {
				sessionID := resolveMonitorSession(
					ctx,
					azdClient,
					monitorAgentKey(info, agentContext.ProjectEndpoint),
				)
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
	cmd.Flags().IntVarP(&flags.tail, "tail", "l", 50,
		"Number of trailing log lines to fetch, or with --type traces, the most traces to list (1-300)")
	cmd.Flags().StringVarP(&flags.logType, "type", "t", monitorTypeConsole,
		"Type of data: 'console' (stdout/stderr), 'system' (container events), "+
			"or 'traces' (recorded OpenTelemetry traces)")
	cmd.Flags().BoolVar(&flags.utc, "utc", false, "Display timestamps in UTC instead of local time")
	cmd.Flags().BoolVar(&flags.raw, "raw", false, "Print the raw SSE stream without formatting")
	addUserIdentityFlag(cmd, &flags.userIdentityFlags)
	addDirectAgentTargetFlags(cmd, &flags.direct, "the [name] argument")
	addMonitorTraceFlags(cmd, &flags.traces)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:  "output",
		Usage: "The output format for --type traces: table (default) or json; logs are always printed as text",
	})

	return cmd
}

// runMonitorTracesForTarget lists or exports traces for the deployed agent named by target.
func runMonitorTracesForTarget(ctx context.Context, target *AgentContext, opts *traceOptions) error {
	traceTarget := &AgentContext{ProjectEndpoint: target.ProjectEndpoint, Name: target.Name}
	deps, err := newTraceDeps(traceTarget)
	if err != nil {
		return err
	}
	return runMonitorTraces(ctx, traceTarget, opts, deps, os.Stdout, os.Stderr)
}

// runDirectMonitorLogs streams session logs for an explicitly targeted agent. Direct
// targets never read stored session selections, so --session-id is required.
func runDirectMonitorLogs(ctx context.Context, flags *monitorFlags, target *AgentContext) error {
	client, err := target.NewClient()
	if err != nil {
		return err
	}
	if err := requireDirectSessionSupport(ctx, client, target, featureSessionLogs); err != nil {
		return err
	}
	if err := validateDirectSessionID(
		flags.sessionID, directSessionsListCommand(&flags.direct, target)); err != nil {
		return err
	}

	action := &MonitorAction{AgentContext: target, flags: flags}
	return action.Run(ctx)
}

func resolveMonitorAgentInfo(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) (*AgentServiceInfo, error) {
	info, err := resolveAgentServiceFromProject(ctx, azdClient, name, noPrompt)
	if err == nil {
		return info, nil
	}
	if isMissingAzdProjectError(err) {
		if name != "" {
			return &AgentServiceInfo{AgentName: name}, nil
		}
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"agent name is required outside an azd project",
			"pass <agent-name> as the positional argument, or run from an azd project",
		)
	}
	return nil, err
}

func isMissingAzdProjectError(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		st, ok := status.FromError(current)
		if !ok {
			continue
		}
		message := strings.ToLower(st.Message())
		missingProject := strings.Contains(message, "no project exists") ||
			strings.Contains(message, "project not found")
		if missingProject && (st.Code() == codes.NotFound || st.Code() == codes.Unknown) {
			return true
		}
	}
	return false
}

func monitorAgentKey(info *AgentServiceInfo, projectEndpoint string) string {
	if info.AgentEndpoint != "" {
		return buildRemoteAgentKeyFromEndpoint(info.AgentEndpoint)
	}
	return buildAgentKey(projectEndpoint, info.AgentName, info.Version, false)
}

func monitorEndpointError(err error) error {
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != exterrors.CodeMissingProjectEndpoint {
		return err
	}

	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint is configured",
		"set a default with `azd ai project set <project-endpoint>` to monitor outside an azd project, "+
			"or run from a directory containing azure.yaml with FOUNDRY_PROJECT_ENDPOINT set in the active azd environment",
	)
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
		DefaultAgentAPIVersion,
		a.flags.logType,
		a.flags.tail,
		a.flags.follow,
		a.flags.sessionRequestOptions(),
	)
	if err != nil {
		// Suppress context deadline/cancellation errors (expected in non-follow timeout and Ctrl+C)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to get agent logs: %w", err)
	}
	defer body.Close()

	formatter := &logFormatter{
		writer: os.Stdout,
		utc:    a.flags.utc,
		raw:    a.flags.raw,
	}
	if err := formatter.formatStream(ctx, body); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	return nil
}

func validateMonitorFlags(flags *monitorFlags) error {
	if flags.tail < 1 || flags.tail > 300 {
		return fmt.Errorf("--tail must be between 1 and 300, got %d", flags.tail)
	}

	switch flags.logType {
	case monitorTypeConsole, monitorTypeSystem, monitorTypeTraces:
	default:
		return fmt.Errorf("--type must be 'console', 'system', or 'traces', got '%s'", flags.logType)
	}

	return nil
}

// resolveMonitorSession resolves the session ID from extension-owned state.
// Returns the session ID if available, or empty string if not found.
func resolveMonitorSession(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
) string {
	if azdClient == nil {
		return ""
	}

	val, err := getAgentSpecificContextValue(ctx, azdClient, "sessions", agentKey)
	if err != nil || val == "" {
		return ""
	}

	return val
}
