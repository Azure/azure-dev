// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"azureaiagent/internal/exterrors"

	"github.com/spf13/cobra"
)

// Flags that only apply to historical log queries (--since with --type console).
const (
	monitorLevelFlag  = "level"
	monitorSearchFlag = "search"
)

const (
	maxLogSearchLength = 256
	// logQueryDataSource identifies where historical log records are read from.
	logQueryDataSource = "application_insights"
	// Table session IDs are shortened so rows stay readable; JSON keeps full IDs.
	logTableSessionIDLength  = 12
	telemetryAttributionNote = "only records explicitly attributed to both the selected project and agent are returned; " +
		"unattributed or conflicting records are excluded, so results may not include all telemetry"
)

// Application Insights severity levels.
var logSeverityNames = []string{"verbose", "information", "warning", "error", "critical"}

var logLevelAliases = map[string]int{
	"verbose": 0, "debug": 0,
	"information": 1, "info": 1,
	"warning": 2, "warn": 2,
	"error":    3,
	"critical": 4,
}

// logQueryCoverageNotes describe which retained records a historical query can return.
var logQueryCoverageNotes = []string{
	"records are application log records and exceptions that the agent exported through OpenTelemetry " +
		"to the connected Application Insights resource (traces and exceptions tables)",
	"output written directly to stdout or stderr, and platform system events, are included only if the agent " +
		"exported them as log records; use monitor without --since to read a session's live console stream",
	telemetryAttributionNote,
	"exceptions without a severity level are treated as error",
}

type monitorLogFlags struct {
	level  string
	search string
}

func addMonitorLogFlags(cmd *cobra.Command, flags *monitorLogFlags) {
	cmd.Flags().StringVar(&flags.level, monitorLevelFlag, "",
		"With --type console and --since, minimum severity of retained log records: "+
			"'verbose', 'information', 'warning', 'error', or 'critical'")
	cmd.Flags().StringVar(&flags.search, monitorSearchFlag, "",
		"With --type console and --since, return only retained log records whose message contains this text "+
			"(case-insensitive)")
}

// logQueryOptions are validated historical log query settings.
type logQueryOptions struct {
	since       time.Duration
	sinceLabel  string
	level       string
	minSeverity int
	search      string
	sessionID   string
	limit       int
	output      string
	utc         bool
}

// validateMonitorLogQueryFlags validates `monitor --since` for console or system logs.
// All checks are local so bad input fails before authentication.
func validateMonitorLogQueryFlags(cmd *cobra.Command, flags *monitorFlags, output string) (*logQueryOptions, error) {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	if flags.logType == monitorTypeSystem {
		return nil, exterrors.Compatibility(
			exterrors.CodeUnsupportedLogSource,
			"--since is not supported with --type system: hosted-agent system events are not retained "+
				"in the project's Application Insights resource",
			"omit --since to read recent system events from a session, or use --type console --since "+
				"to search the agent's retained application logs",
		)
	}

	for _, name := range []string{"follow", "raw", "user-identity", monitorTraceIDFlag, monitorStatusFlag, monitorOutFileFlag} {
		if changed(name) {
			return nil, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--%s cannot be used with --since for console logs", name),
				"--since searches retained logs in Application Insights instead of a session log stream; "+
					"remove --"+name+", or omit --since to read the live session stream",
			)
		}
	}

	opts := &logQueryOptions{
		sessionID: flags.sessionID,
		limit:     flags.tail,
		utc:       flags.utc,
	}

	switch output {
	case "", "default", "table":
		opts.output = "table"
	case "json":
		opts.output = "json"
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--output %q is not supported with --since", output),
			"use --output table or --output json",
		)
	}

	since, err := parseTraceWindow(flags.traces.since)
	if err != nil {
		return nil, err
	}
	opts.since = since
	opts.sinceLabel = strings.TrimSpace(flags.traces.since)

	if changed(monitorLevelFlag) {
		level := strings.ToLower(strings.TrimSpace(flags.logs.level))
		severity, ok := logLevelAliases[level]
		if !ok {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("--level %q is invalid", flags.logs.level),
				"use one of: "+strings.Join(logSeverityNames, ", "),
			)
		}
		opts.minSeverity = severity
		opts.level = logSeverityNames[severity]
	}

	if changed(monitorSearchFlag) {
		opts.search = strings.TrimSpace(flags.logs.search)
		if opts.search == "" || len(opts.search) > maxLogSearchLength {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("--search must contain 1-%d characters", maxLogSearchLength),
				"supply the text to find in log messages, such as --search timeout",
			)
		}
	}

	if changed("session-id") {
		if err := validateLogQuerySessionID(opts.sessionID); err != nil {
			return nil, err
		}
	}

	return opts, nil
}

func validateLogQuerySessionID(sessionID string) error {
	if sessionID == "" || strings.TrimSpace(sessionID) != sessionID || strings.ContainsAny(sessionID, "/\\?#'\"") {
		return exterrors.Validation(
			exterrors.CodeInvalidSessionId,
			fmt.Sprintf("session ID %q is invalid", sessionID),
			"supply the session ID exactly as returned by `azd ai agent sessions list`",
		)
	}
	return nil
}

// logQueryFilters echoes the filters applied to a historical log query.
type logQueryFilters struct {
	Type      string `json:"type"`
	Level     string `json:"level,omitempty"`
	Search    string `json:"search,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Limit     int    `json:"limit"`
}

// logQueryCoverage describes the retained data a historical log query reads.
type logQueryCoverage struct {
	Source  string   `json:"source"`
	Tables  []string `json:"tables"`
	Sampled bool     `json:"sampled"`
	Notes   []string `json:"notes"`
}

// logRecord is one retained log record.
type logRecord struct {
	Timestamp     string         `json:"timestamp"`
	RecordType    string         `json:"record_type"`
	Severity      string         `json:"severity"`
	SeverityLevel int64          `json:"severity_level"`
	Message       string         `json:"message"`
	ExceptionType string         `json:"exception_type,omitempty"`
	AgentVersion  string         `json:"agent_version,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	Logger        string         `json:"logger,omitempty"`
	RoleInstance  string         `json:"role_instance,omitempty"`
	ItemCount     int64          `json:"item_count"`
	Attributes    map[string]any `json:"attributes"`

	time time.Time
}

// logQueryResult is the `monitor --since` output for console logs.
type logQueryResult struct {
	ProjectEndpoint string               `json:"project_endpoint"`
	AgentName       string               `json:"agent_name"`
	Telemetry       traceTelemetrySource `json:"telemetry"`
	Window          traceWindow          `json:"window"`
	Filters         logQueryFilters      `json:"filters"`
	Coverage        logQueryCoverage     `json:"coverage"`
	Truncated       bool                 `json:"truncated"`
	PartialError    string               `json:"partial_error,omitempty"`
	Data            []logRecord          `json:"data"`
}

// runMonitorLogQueryForTarget searches retained logs for the deployed agent named by target.
func runMonitorLogQueryForTarget(ctx context.Context, target *AgentContext, opts *logQueryOptions) error {
	queryTarget := &AgentContext{ProjectEndpoint: target.ProjectEndpoint, Name: target.Name}
	deps, err := newTraceDeps(queryTarget)
	if err != nil {
		return err
	}
	return runMonitorLogQuery(ctx, queryTarget, opts, deps, os.Stdout, os.Stderr)
}

// runMonitorLogQuery searches retained console logs for a resolved agent target.
func runMonitorLogQuery(
	ctx context.Context,
	target *AgentContext,
	opts *logQueryOptions,
	deps *traceDeps,
	out, errOut io.Writer,
) error {
	agent, err := getDirectAgent(ctx, deps.agents, target)
	if err != nil {
		return err
	}
	if err := requireSessionSupport(agent, "historical logs"); err != nil {
		return err
	}

	source, err := resolveTracingSource(ctx, deps.connections, target.ProjectEndpoint)
	if err != nil {
		return err
	}

	end := deps.now().UTC()
	start := end.Add(-opts.since)

	fmt.Fprintf(errOut, "Querying logs for agent %s from Application Insights connection %s...\n",
		target.Name, source.ConnectionName)

	query := buildLogQuery(target.Name, source.ProjectID, opts, start, end)
	response, err := deps.logs.QueryResource(ctx, source.ResourceID, query, start, end)
	if err != nil {
		return classifyTelemetryQueryError(err, source, exterrors.OpQueryLogs)
	}

	result := &logQueryResult{
		ProjectEndpoint: target.ProjectEndpoint,
		AgentName:       target.Name,
		Telemetry:       *source,
		Window:          newTraceWindow(start, end),
		Filters: logQueryFilters{
			Type:      monitorTypeConsole,
			Level:     opts.level,
			Search:    opts.search,
			SessionID: opts.sessionID,
			Limit:     opts.limit,
		},
		Coverage: logQueryCoverage{
			Source: logQueryDataSource,
			Tables: []string{"traces", "exceptions"},
			Notes:  slices.Clone(logQueryCoverageNotes),
		},
		PartialError: partialQueryError(response),
		Data:         []logRecord{},
	}

	for _, row := range response.PrimaryTable().Records() {
		record, err := logRecordFromRow(row)
		if err != nil {
			return err
		}
		if record.ItemCount > 1 {
			result.Coverage.Sampled = true
		}
		result.Data = append(result.Data, record)
	}
	// The query returns newest first; keep the newest records and print them oldest first.
	if len(result.Data) > opts.limit {
		result.Data = result.Data[:opts.limit]
		result.Truncated = true
	}
	slices.Reverse(result.Data)
	if result.Coverage.Sampled {
		result.Coverage.Notes = append(result.Coverage.Notes,
			"telemetry is sampled; records with item_count > 1 each stand for several similar records")
	}

	return writeLogQueryResult(result, opts, out, errOut)
}

// buildLogQuery returns retained log records attributed to the agent, newest first.
// limit+1 rows detect truncation.
func buildLogQuery(agentName, projectID string, opts *logQueryOptions, start, end time.Time) string {
	var filters strings.Builder
	if opts.sessionID != "" {
		fmt.Fprintf(&filters, "| where session_id == %s\n", kqlString(opts.sessionID))
	}
	if opts.minSeverity > 0 {
		fmt.Fprintf(&filters, "| where severity_level >= %d\n", opts.minSeverity)
	}
	if opts.search != "" {
		fmt.Fprintf(&filters, "| where message contains %s or exception_type contains %s\n",
			kqlString(opts.search), kqlString(opts.search))
	}

	return fmt.Sprintf(`let agentName = %s;
let projectId = %s;
union isfuzzy=true
    (traces | extend record_type = 'log', message = tostring(message),
        severity_level = toint(severityLevel), exception_type = ''),
    (exceptions | extend record_type = 'exception',
        message = coalesce(tostring(column_ifexists('outerMessage', '')), tostring(column_ifexists('innermostMessage', ''))),
        severity_level = toint(coalesce(toint(severityLevel), 3)),
        exception_type = tostring(column_ifexists('type', '')))
| where timestamp between (%s .. %s)
| %s
| %s
| extend session_id = tostring(customDimensions['microsoft.session.id']),
    agent_version = coalesce(tostring(customDimensions['gen_ai.agent.version']),
        iff(agent_id_attr startswith strcat(agentName, ':'), substring(agent_id_attr, strlen(agentName) + 1), ''))
%s| order by timestamp desc
| take %d
| project timestamp, record_type, severity_level, message, exception_type, agent_version, session_id,
    trace_id = operation_Id, span_id = operation_ParentId,
    logger = tostring(customDimensions['logger_name']), role_instance = cloud_RoleInstance,
    item_count = itemCount, attributes = customDimensions`,
		kqlString(agentName), kqlString(projectID), kqlDatetime(start), kqlDatetime(end),
		projectTelemetryFilter, agentTelemetryFilter, filters.String(), opts.limit+1)
}

func logRecordFromRow(row map[string]any) (logRecord, error) {
	timestamp, ok := recordTime(row, "timestamp")
	if !ok {
		return logRecord{}, exterrors.Internal(exterrors.CodeInvalidTelemetryQueryResult,
			"Application Insights returned a log record without a timestamp")
	}
	level := recordInt(row, "severity_level")
	return logRecord{
		Timestamp:     timestamp.UTC().Format(time.RFC3339Nano),
		RecordType:    recordString(row, "record_type"),
		Severity:      logSeverityName(level),
		SeverityLevel: level,
		Message:       recordString(row, "message"),
		ExceptionType: recordString(row, "exception_type"),
		AgentVersion:  recordString(row, "agent_version"),
		SessionID:     recordString(row, "session_id"),
		TraceID:       nonZeroTelemetryID(recordString(row, "trace_id")),
		SpanID:        nonZeroTelemetryID(recordString(row, "span_id")),
		Logger:        recordString(row, "logger"),
		RoleInstance:  recordString(row, "role_instance"),
		ItemCount:     recordInt(row, "item_count"),
		Attributes:    redactTraceAttributes(recordDynamic(row, "attributes")),
		time:          timestamp,
	}, nil
}

func logSeverityName(level int64) string {
	if level >= 0 && int(level) < len(logSeverityNames) {
		return logSeverityNames[level]
	}
	return "unknown"
}

// nonZeroTelemetryID drops the all-zero IDs recorded for logs emitted outside a trace.
func nonZeroTelemetryID(id string) string {
	if strings.Trim(id, "0") == "" {
		return ""
	}
	return id
}

func writeLogQueryResult(result *logQueryResult, opts *logQueryOptions, out, errOut io.Writer) error {
	if result.Truncated {
		fmt.Fprintf(errOut, "WARNING: more log records matched; showing the newest %d. Narrow --since or raise --tail.\n",
			opts.limit)
	}
	if result.PartialError != "" {
		fmt.Fprintf(errOut, "WARNING: results may be incomplete; the query returned a partial error: %s\n",
			result.PartialError)
	}

	emptyNotice := fmt.Sprintf("No log records found for agent %q in the last %s%s.",
		result.AgentName, opts.sinceLabel, describeLogFilters(result.Filters))
	emptyHint := "Retained logs include only records the agent exported to the connected Application Insights " +
		"resource; ingestion can take several minutes, and data outside retention is not returned."

	if opts.output == "json" {
		if len(result.Data) == 0 {
			fmt.Fprintln(errOut, emptyNotice+" "+emptyHint)
		}
		return writeIndentedJSON(out, result)
	}

	fmt.Fprintf(out, "Agent: %s\nProject: %s\nTelemetry: %s (%s)\nWindow: %s to %s\nCoverage: %s\n\n",
		result.AgentName, result.ProjectEndpoint, result.Telemetry.ConnectionName, result.Telemetry.ResourceID,
		result.Window.Start, result.Window.End, telemetryAttributionNote)
	if len(result.Data) == 0 {
		_, err := fmt.Fprintf(out, "%s\n%s\n", emptyNotice, emptyHint)
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tLEVEL\tVERSION\tSESSION\tMESSAGE")
	fmt.Fprintln(w, "----\t-----\t-------\t-------\t-------")
	for _, record := range result.Data {
		message := record.Message
		if record.ExceptionType != "" {
			message = record.ExceptionType + ": " + message
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			formatTraceTime(record.time, opts.utc), record.Severity, record.AgentVersion,
			shortenID(record.SessionID, logTableSessionIDLength), singleLine(message))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "\nUse --output json for full session IDs, trace IDs, and attributes.")
	return err
}

func describeLogFilters(filters logQueryFilters) string {
	var parts []string
	if filters.Level != "" {
		parts = append(parts, "at level "+filters.Level+" or above")
	}
	if filters.Search != "" {
		parts = append(parts, fmt.Sprintf("containing %q", filters.Search))
	}
	if filters.SessionID != "" {
		parts = append(parts, fmt.Sprintf("in session %q", filters.SessionID))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ")
}

func shortenID(id string, length int) string {
	if len(id) <= length {
		return id
	}
	return id[:length]
}

func singleLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			quoted := strconv.QuoteRune(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
