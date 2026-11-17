// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/spf13/cobra"
)

const (
	monitorTypeConsole = "console"
	monitorTypeSystem  = "system"
	monitorTypeTraces  = "traces"

	traceStatusAll       = "all"
	traceStatusFailed    = "failed"
	traceStatusSucceeded = "succeeded"

	defaultTraceListWindow   = time.Hour
	defaultTraceExportWindow = 30 * 24 * time.Hour
	maxTraceWindow           = 730 * 24 * time.Hour
	maxTraceExportSpans      = 5000

	// traceExportSchemaVersion versions the JSON document written by trace export.
	traceExportSchemaVersion = "1"

	tracingSetupURL = "https://learn.microsoft.com/azure/foundry/observability/quickstarts/quickstart-tracing-hosted-agent"

	redactedValue = "[REDACTED]"
)

// Flags that only apply to --type traces.
const (
	monitorSinceFlag   = "since"
	monitorTraceIDFlag = "trace-id"
	monitorStatusFlag  = "status"
	monitorOutFileFlag = "out-file"
)

var traceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// monitorTraceFlags holds the flags for `monitor --type traces`.
type monitorTraceFlags struct {
	since   string
	traceID string
	status  string
	outFile string
}

// traceOptions are validated trace settings.
type traceOptions struct {
	since      time.Duration
	sinceLabel string
	traceID    string
	status     string
	limit      int
	output     string
	outFile    string
	utc        bool
}

func addMonitorTraceFlags(cmd *cobra.Command, flags *monitorTraceFlags) {
	cmd.Flags().StringVar(&flags.since, monitorSinceFlag, "",
		"With --type traces, how far back to search, such as 30m, 6h, or 7d "+
			"(default 1h when listing, 30d with --trace-id)")
	cmd.Flags().StringVar(&flags.traceID, monitorTraceIDFlag, "",
		"With --type traces, export one trace (operation ID) with all of its recorded spans")
	cmd.Flags().StringVar(&flags.status, monitorStatusFlag, traceStatusAll,
		"With --type traces, filter listed traces by outcome: 'all', 'failed', or 'succeeded'")
	cmd.Flags().StringVar(&flags.outFile, monitorOutFileFlag, "",
		"With --type traces, write the JSON result to this file instead of stdout")
}

// validateMonitorModeFlags rejects flags that do not apply to the selected --type.
// All checks are local so bad input fails before authentication.
func validateMonitorModeFlags(cmd *cobra.Command, flags *monitorFlags, output string) (*traceOptions, error) {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	if flags.logType != monitorTypeTraces {
		for _, name := range []string{monitorSinceFlag, monitorTraceIDFlag, monitorStatusFlag, monitorOutFileFlag} {
			if changed(name) {
				return nil, exterrors.Validation(
					exterrors.CodeConflictingArguments,
					fmt.Sprintf("--%s is only valid with --type traces", name),
					"add --type traces to list or export agent traces",
				)
			}
		}
		return nil, nil
	}

	for _, name := range []string{"follow", "raw", "session-id", "user-identity"} {
		if changed(name) {
			return nil, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--%s cannot be used with --type traces", name),
				"traces are read from the project's Application Insights resource, not from a session log stream; "+
					"remove --"+name,
			)
		}
	}

	opts := &traceOptions{
		traceID: flags.traces.traceID,
		status:  flags.traces.status,
		limit:   flags.tail,
		outFile: strings.TrimSpace(flags.traces.outFile),
		utc:     flags.utc,
	}

	switch output {
	case "", "default", "table":
		opts.output = "table"
	case "json":
		opts.output = "json"
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--output %q is not supported with --type traces", output),
			"use --output table or --output json",
		)
	}

	if changed(monitorTraceIDFlag) {
		if !traceIDPattern.MatchString(opts.traceID) {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("trace ID %q is invalid", opts.traceID),
				"supply the trace ID (operation ID) exactly as listed by `azd ai agent monitor <name> --type traces`",
			)
		}
		if changed(monitorStatusFlag) || changed("tail") {
			return nil, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--status and --tail filter the trace list and cannot be used with --trace-id",
				"remove --status and --tail to export one trace",
			)
		}
	}

	switch opts.status {
	case traceStatusAll, traceStatusFailed, traceStatusSucceeded:
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--status must be 'all', 'failed', or 'succeeded', got '%s'", opts.status),
			"",
		)
	}

	if changed(monitorOutFileFlag) {
		if opts.outFile == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--out-file requires a file path",
				"supply a destination such as --out-file ./trace.json",
			)
		}
		if info, err := os.Stat(opts.outFile); err == nil && info.IsDir() {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("--out-file %q is a directory", opts.outFile),
				"supply a file path, such as --out-file ./trace.json",
			)
		}
	}

	opts.since = defaultTraceListWindow
	opts.sinceLabel = "1h"
	if opts.traceID != "" {
		opts.since = defaultTraceExportWindow
		opts.sinceLabel = "30d"
	}
	if changed(monitorSinceFlag) {
		since, err := parseTraceWindow(flags.traces.since)
		if err != nil {
			return nil, err
		}
		opts.since = since
		opts.sinceLabel = strings.TrimSpace(flags.traces.since)
	}

	return opts, nil
}

// parseTraceWindow parses a lookback window. It accepts Go durations (30m, 6h) and
// whole days (7d).
func parseTraceWindow(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	var window time.Duration
	var err error
	if days, ok := strings.CutSuffix(value, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		window = time.Duration(n) * 24 * time.Hour
	} else {
		window, err = time.ParseDuration(value)
	}
	if err != nil || window <= 0 || window > maxTraceWindow {
		return 0, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--since %q is invalid", raw),
			"supply a positive window up to 730d, such as 30m, 6h, or 7d",
		)
	}
	return window, nil
}

// tracingConnectionLister reads the connections of a Foundry project.
type tracingConnectionLister interface {
	GetAllConnections(ctx context.Context) ([]azure.Connection, error)
}

// traceQuerier runs KQL against an Application Insights resource.
type traceQuerier interface {
	QueryResource(ctx context.Context, resourceID, query string, start, end time.Time) (*azure.LogsQueryResult, error)
}

type traceDeps struct {
	agents      remoteAgentReader
	connections tracingConnectionLister
	logs        traceQuerier
	now         func() time.Time
}

func newTraceDeps(target *AgentContext) (*traceDeps, error) {
	agentClient, err := target.NewClient()
	if err != nil {
		return nil, err
	}
	credential, err := newAgentCredential()
	if err != nil {
		return nil, err
	}
	projectsClient, err := azure.NewFoundryProjectsClient(target.ProjectEndpoint, credential)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid Foundry project endpoint %q: %v", target.ProjectEndpoint, err), "")
	}
	return &traceDeps{
		agents:      agentClient,
		connections: projectsClient,
		logs:        azure.NewLogsQueryClient(credential),
		now:         time.Now,
	}, nil
}

// traceTelemetrySource identifies the Application Insights resource that traces are read from.
type traceTelemetrySource struct {
	ConnectionName string `json:"connection_name"`
	ResourceID     string `json:"resource_id"`
}

type traceWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func newTraceWindow(start, end time.Time) traceWindow {
	return traceWindow{Start: start.UTC().Format(time.RFC3339), End: end.UTC().Format(time.RFC3339)}
}

// resolveTracingSource finds the project's Application Insights connection. It reads
// only connection metadata, never connection credentials.
func resolveTracingSource(
	ctx context.Context, lister tracingConnectionLister, projectEndpoint string,
) (*traceTelemetrySource, error) {
	connections, err := lister.GetAllConnections(ctx)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			(respErr.StatusCode == http.StatusUnauthorized || respErr.StatusCode == http.StatusForbidden) {
			return nil, withServiceSuggestion(exterrors.ServiceFromAzure(err, exterrors.OpListConnections),
				"confirm your account can read connections in this Foundry project (for example, the Azure AI User role)")
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpListConnections)
	}

	var candidates []azure.Connection
	for _, conn := range connections {
		if strings.EqualFold(string(conn.Type), string(azure.ConnectionTypeAppInsights)) {
			candidates = append(candidates, conn)
		}
	}
	if len(candidates) == 0 {
		return nil, exterrors.Dependency(
			exterrors.CodeTracingNotConfigured,
			fmt.Sprintf("Foundry project %s has no Application Insights connection, so no agent traces are recorded",
				projectEndpoint),
			"connect an Application Insights resource to the project (Foundry portal > Tracing) and "+
				"enable tracing for the agent; see "+tracingSetupURL,
		)
	}

	sources := make([]traceTelemetrySource, 0, len(candidates))
	var defaults []traceTelemetrySource
	for _, conn := range candidates {
		resourceID := appInsightsConnectionResourceID(conn)
		if resourceID == "" {
			continue
		}
		source := traceTelemetrySource{ConnectionName: conn.Name, ResourceID: resourceID}
		sources = append(sources, source)
		if conn.IsDefault {
			defaults = append(defaults, source)
		}
	}
	if len(sources) == 0 {
		return nil, exterrors.Dependency(
			exterrors.CodeTracingNotConfigured,
			fmt.Sprintf("the Application Insights connection %q in project %s does not identify an "+
				"Application Insights resource", candidates[0].Name, projectEndpoint),
			"reconnect Application Insights to the project in the Foundry portal; see "+tracingSetupURL,
		)
	}

	if source, ok := singleTelemetryResource(sources); ok {
		return &source, nil
	}
	if source, ok := singleTelemetryResource(defaults); ok {
		return &source, nil
	}
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.ConnectionName)
	}
	slices.Sort(names)
	return nil, exterrors.Dependency(
		exterrors.CodeAmbiguousTracingConnection,
		fmt.Sprintf("project %s has several Application Insights connections for different resources: %s",
			projectEndpoint, strings.Join(names, ", ")),
		"mark one connection as the project default in the Foundry portal, or remove the unused connections",
	)
}

// singleTelemetryResource returns the source when every entry targets the same resource.
func singleTelemetryResource(sources []traceTelemetrySource) (traceTelemetrySource, bool) {
	if len(sources) == 0 {
		return traceTelemetrySource{}, false
	}
	for _, source := range sources[1:] {
		if !strings.EqualFold(source.ResourceID, sources[0].ResourceID) {
			return traceTelemetrySource{}, false
		}
	}
	return sources[0], true
}

func appInsightsConnectionResourceID(conn azure.Connection) string {
	for key, value := range conn.Metadata {
		if strings.EqualFold(key, "ResourceId") && azure.IsAppInsightsResourceID(value) {
			return value
		}
	}
	if azure.IsAppInsightsResourceID(conn.Target) {
		return conn.Target
	}
	return ""
}

func classifyTraceQueryError(err error, source *traceTelemetrySource) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			return exterrors.Dependency(
				exterrors.CodeTelemetryResourceNotFound,
				fmt.Sprintf("Application Insights resource %s from project connection %q was not found",
					source.ResourceID, source.ConnectionName),
				"reconnect an existing Application Insights resource to the project in the Foundry portal",
			)
		case http.StatusUnauthorized, http.StatusForbidden:
			return withServiceSuggestion(exterrors.ServiceFromAzure(err, exterrors.OpQueryTraces),
				fmt.Sprintf("confirm your account can read logs for Application Insights resource %s "+
					"(for example, the Log Analytics Reader or Monitoring Reader role)", source.ResourceID))
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpQueryTraces)
}

// kqlString quotes a value as a KQL string literal.
func kqlString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}

func kqlDatetime(t time.Time) string {
	return "datetime(" + t.UTC().Format(time.RFC3339Nano) + ")"
}

// traceSpansSource unions the span tables of a classic Application Insights schema.
const traceSpansSource = `union isfuzzy=true
    (requests | extend span_kind = 'request'),
    (dependencies | extend span_kind = 'dependency')`

// Agent identity can be inherited by infrastructure spans, so require a GenAI
// operation on the same span. Agent IDs can be UUIDs or <agent-name>:<version>.
const agentSpanFilter = `extend agent_name_attr = tostring(customDimensions['gen_ai.agent.name']),
    agent_id_attr = tostring(customDimensions['gen_ai.agent.id'])
| where gettype(customDimensions['gen_ai.operation.name']) == 'string'
| where isnotempty(tostring(customDimensions['gen_ai.operation.name']))
| where agent_name_attr =~ agentName or agent_id_attr =~ agentName
    or (agent_id_attr startswith strcat(agentName, ':') and strlen(agent_id_attr) > strlen(agentName) + 1)`

// buildTraceListQuery returns the newest traces (operation IDs) that contain at least
// one GenAI span attributed to the agent. limit+1 rows detect truncation.
func buildTraceListQuery(agentName, status string, start, end time.Time, limit int) string {
	statusFilter := ""
	switch status {
	case traceStatusFailed:
		statusFilter = "| where failed_span_count > 0\n"
	case traceStatusSucceeded:
		statusFilter = "| where failed_span_count == 0\n"
	}

	return fmt.Sprintf(`let agentName = %s;
let spans = %s
| where timestamp between (%s .. %s);
let agentTraces = spans
| %s
| extend agent_version_attr = coalesce(tostring(customDimensions['gen_ai.agent.version']),
    iff(agent_id_attr startswith strcat(agentName, ':'), substring(agent_id_attr, strlen(agentName) + 1), ''))
| extend conversation_attr = tostring(customDimensions['gen_ai.conversation.id'])
| extend agent_span_name = coalesce(name, tostring(customDimensions['gen_ai.operation.name']))
| summarize agent_version = take_anyif(agent_version_attr, isnotempty(agent_version_attr)),
    conversation_id = take_anyif(conversation_attr, isnotempty(conversation_attr)),
    arg_min(timestamp, agent_span_name) by operation_Id;
spans
| where operation_Id in ((agentTraces | project operation_Id))
| summarize start_time = min(timestamp), end_time = max(timestamp + duration * 1ms), span_count = count(),
    failed_span_count = countif(success == false), sampled = max(toint(itemCount > 1)),
    root_span_name = take_anyif(name, isempty(operation_ParentId) or operation_ParentId == operation_Id)
    by operation_Id
| join kind=inner agentTraces on operation_Id
| extend root_span_name = coalesce(root_span_name, agent_span_name)
%s| order by start_time desc
| take %d
| project trace_id = operation_Id, start_time, end_time, span_count, failed_span_count, sampled,
    root_span_name, agent_version, conversation_id`,
		kqlString(agentName), traceSpansSource, kqlDatetime(start), kqlDatetime(end),
		agentSpanFilter, statusFilter, limit+1)
}

// buildTraceExportQuery returns every span and exception recorded for one trace.
// maxSpans+1 rows are requested to detect truncation.
func buildTraceExportQuery(traceID string, start, end time.Time, maxSpans int) string {
	return fmt.Sprintf(`let traceId = %s;
union isfuzzy=true
    (requests | extend span_kind = 'request'),
    (dependencies | extend span_kind = 'dependency'),
    (exceptions | extend span_kind = 'exception')
| where timestamp between (%s .. %s)
| where operation_Id == traceId
| order by timestamp asc
| take %d
| project timestamp, span_kind,
    span_id = tostring(column_ifexists('id', '')),
    parent_id = operation_ParentId,
    name = tostring(column_ifexists('name', '')),
    duration_ms = todouble(column_ifexists('duration', real(null))),
    success = column_ifexists('success', bool(null)),
    result_code = tostring(column_ifexists('resultCode', '')),
    type = tostring(column_ifexists('type', '')),
    target = tostring(column_ifexists('target', '')),
    role = cloud_RoleName,
    item_count = itemCount,
    message = tostring(column_ifexists('outerMessage', '')),
    problem_id = tostring(column_ifexists('problemId', '')),
    attributes = customDimensions`,
		kqlString(traceID), kqlDatetime(start), kqlDatetime(end), maxSpans+1)
}

// traceSummary is one listed trace.
type traceSummary struct {
	TraceID         string `json:"trace_id"`
	StartTime       string `json:"start_time"`
	EndTime         string `json:"end_time,omitempty"`
	DurationMs      int64  `json:"duration_ms"`
	SpanCount       int64  `json:"span_count"`
	FailedSpanCount int64  `json:"failed_span_count"`
	Status          string `json:"status"`
	Sampled         bool   `json:"sampled"`
	RootSpanName    string `json:"root_span_name,omitempty"`
	AgentVersion    string `json:"agent_version,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`

	start time.Time
}

// traceListResult is the `monitor --type traces` list output.
type traceListResult struct {
	ProjectEndpoint string               `json:"project_endpoint"`
	AgentName       string               `json:"agent_name"`
	Telemetry       traceTelemetrySource `json:"telemetry"`
	Window          traceWindow          `json:"window"`
	StatusFilter    string               `json:"status_filter"`
	Truncated       bool                 `json:"truncated"`
	PartialError    string               `json:"partial_error,omitempty"`
	Data            []traceSummary       `json:"data"`
}

// traceSpan is one exported span.
type traceSpan struct {
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Type         string         `json:"type,omitempty"`
	Target       string         `json:"target,omitempty"`
	Role         string         `json:"role,omitempty"`
	StartTime    string         `json:"start_time"`
	EndTime      string         `json:"end_time"`
	DurationMs   float64        `json:"duration_ms"`
	Status       string         `json:"status"`
	ResultCode   string         `json:"result_code,omitempty"`
	ItemCount    int64          `json:"item_count"`
	Attributes   map[string]any `json:"attributes"`

	start time.Time
}

// traceException is one exception recorded within the exported trace.
type traceException struct {
	Timestamp    string         `json:"timestamp"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Type         string         `json:"type,omitempty"`
	Message      string         `json:"message,omitempty"`
	ProblemID    string         `json:"problem_id,omitempty"`
	Attributes   map[string]any `json:"attributes"`
}

// traceCompleteness explains whether all spans of the trace were read.
type traceCompleteness struct {
	Complete              bool     `json:"complete"`
	RootSpanFound         bool     `json:"root_span_found"`
	UpstreamParentSpanIDs []string `json:"upstream_parent_span_ids"`
	MissingParentSpanIDs  []string `json:"missing_parent_span_ids"`
	Truncated             bool     `json:"truncated"`
	Sampled               bool     `json:"sampled"`
	PartialError          string   `json:"partial_error,omitempty"`
	Notes                 []string `json:"notes"`
}

// traceExport is the documented export format for one trace.
type traceExport struct {
	SchemaVersion   string               `json:"schema_version"`
	TraceID         string               `json:"trace_id"`
	ProjectEndpoint string               `json:"project_endpoint"`
	AgentName       string               `json:"agent_name"`
	Telemetry       traceTelemetrySource `json:"telemetry"`
	Window          traceWindow          `json:"window"`
	Completeness    traceCompleteness    `json:"completeness"`
	Spans           []traceSpan          `json:"spans"`
	Exceptions      []traceException     `json:"exceptions"`
}

// runMonitorTraces lists or exports traces for a resolved agent target.
func runMonitorTraces(
	ctx context.Context,
	target *AgentContext,
	opts *traceOptions,
	deps *traceDeps,
	out, errOut io.Writer,
) error {
	if _, err := getDirectAgent(ctx, deps.agents, target); err != nil {
		return err
	}

	source, err := resolveTracingSource(ctx, deps.connections, target.ProjectEndpoint)
	if err != nil {
		return err
	}

	end := deps.now().UTC()
	start := end.Add(-opts.since)

	if opts.traceID != "" {
		export, err := exportTrace(ctx, deps.logs, target, source, opts, start, end)
		if err != nil {
			return err
		}
		return writeTraceExport(export, opts, out, errOut)
	}

	fmt.Fprintf(errOut, "Querying traces for agent %s from Application Insights connection %s...\n",
		target.Name, source.ConnectionName)
	result, err := listTraces(ctx, deps.logs, target, source, opts, start, end)
	if err != nil {
		return err
	}
	return writeTraceList(result, opts, out, errOut)
}

func listTraces(
	ctx context.Context,
	logs traceQuerier,
	target *AgentContext,
	source *traceTelemetrySource,
	opts *traceOptions,
	start, end time.Time,
) (*traceListResult, error) {
	query := buildTraceListQuery(target.Name, opts.status, start, end, opts.limit)
	response, err := logs.QueryResource(ctx, source.ResourceID, query, start, end)
	if err != nil {
		return nil, classifyTraceQueryError(err, source)
	}

	result := &traceListResult{
		ProjectEndpoint: target.ProjectEndpoint,
		AgentName:       target.Name,
		Telemetry:       *source,
		Window:          newTraceWindow(start, end),
		StatusFilter:    opts.status,
		PartialError:    partialQueryError(response),
		Data:            []traceSummary{},
	}

	for _, record := range response.PrimaryTable().Records() {
		summary, err := traceSummaryFromRecord(record)
		if err != nil {
			return nil, err
		}
		result.Data = append(result.Data, summary)
	}
	if len(result.Data) > opts.limit {
		result.Data = result.Data[:opts.limit]
		result.Truncated = true
	}
	return result, nil
}

func traceSummaryFromRecord(record map[string]any) (traceSummary, error) {
	traceID := recordString(record, "trace_id")
	start, ok := recordTime(record, "start_time")
	if traceID == "" || !ok {
		return traceSummary{}, exterrors.Internal(exterrors.CodeInvalidTelemetryQueryResult,
			"Application Insights returned a trace without an ID or start time")
	}
	failed := recordInt(record, "failed_span_count")
	summary := traceSummary{
		TraceID:         traceID,
		StartTime:       start.UTC().Format(time.RFC3339Nano),
		SpanCount:       recordInt(record, "span_count"),
		FailedSpanCount: failed,
		Status:          "ok",
		Sampled:         recordInt(record, "sampled") > 0,
		RootSpanName:    recordString(record, "root_span_name"),
		AgentVersion:    recordString(record, "agent_version"),
		ConversationID:  recordString(record, "conversation_id"),
		start:           start,
	}
	if failed > 0 {
		summary.Status = "failed"
	}
	if end, ok := recordTime(record, "end_time"); ok {
		summary.EndTime = end.UTC().Format(time.RFC3339Nano)
		summary.DurationMs = end.Sub(start).Milliseconds()
	}
	return summary, nil
}

func exportTrace(
	ctx context.Context,
	logs traceQuerier,
	target *AgentContext,
	source *traceTelemetrySource,
	opts *traceOptions,
	start, end time.Time,
) (*traceExport, error) {
	query := buildTraceExportQuery(opts.traceID, start, end, maxTraceExportSpans)
	response, err := logs.QueryResource(ctx, source.ResourceID, query, start, end)
	if err != nil {
		return nil, classifyTraceQueryError(err, source)
	}

	records := response.PrimaryTable().Records()
	partialError := partialQueryError(response)
	if len(records) == 0 {
		message := fmt.Sprintf("trace %q was not found in Application Insights resource %s within the last %s",
			opts.traceID, source.ResourceID, opts.sinceLabel)
		if partialError != "" {
			message += " (the query returned a partial error: " + partialError + ")"
		}
		return nil, exterrors.Validation(
			exterrors.CodeTraceNotFound,
			message,
			"widen --since; the trace may be outside the retention period, sampled out, "+
				"or not yet ingested (ingestion can take several minutes)",
		)
	}

	truncated := len(records) > maxTraceExportSpans
	if truncated {
		records = records[:maxTraceExportSpans]
	}

	export := &traceExport{
		SchemaVersion:   traceExportSchemaVersion,
		TraceID:         opts.traceID,
		ProjectEndpoint: target.ProjectEndpoint,
		AgentName:       target.Name,
		Telemetry:       *source,
		Window:          newTraceWindow(start, end),
		Spans:           []traceSpan{},
		Exceptions:      []traceException{},
	}

	belongsToAgent := false
	for _, record := range records {
		attributes := recordDynamic(record, "attributes")
		timestamp, ok := recordTime(record, "timestamp")
		if !ok {
			return nil, exterrors.Internal(exterrors.CodeInvalidTelemetryQueryResult,
				"Application Insights returned a span without a timestamp")
		}

		if recordString(record, "span_kind") == "exception" {
			export.Exceptions = append(export.Exceptions, traceException{
				Timestamp:    timestamp.UTC().Format(time.RFC3339Nano),
				ParentSpanID: recordString(record, "parent_id"),
				Type:         recordString(record, "type"),
				Message:      recordString(record, "message"),
				ProblemID:    recordString(record, "problem_id"),
				Attributes:   redactTraceAttributes(attributes),
			})
			continue
		}

		if genAISpanMatchesAgent(attributes, target.Name) {
			belongsToAgent = true
		}
		durationMs := recordFloat(record, "duration_ms")
		status := "unset"
		if success, ok := record["success"].(bool); ok {
			status = "error"
			if success {
				status = "ok"
			}
		}
		export.Spans = append(export.Spans, traceSpan{
			SpanID:       recordString(record, "span_id"),
			ParentSpanID: recordString(record, "parent_id"),
			Name:         recordString(record, "name"),
			Kind:         recordString(record, "span_kind"),
			Type:         recordString(record, "type"),
			Target:       recordString(record, "target"),
			Role:         recordString(record, "role"),
			StartTime:    timestamp.UTC().Format(time.RFC3339Nano),
			EndTime:      timestamp.Add(msDuration(durationMs)).UTC().Format(time.RFC3339Nano),
			DurationMs:   durationMs,
			Status:       status,
			ResultCode:   recordString(record, "result_code"),
			ItemCount:    max(recordInt(record, "item_count"), 1),
			Attributes:   redactTraceAttributes(attributes),
			start:        timestamp,
		})
	}

	if !belongsToAgent {
		return nil, exterrors.Validation(
			exterrors.CodeTraceAgentMismatch,
			fmt.Sprintf("trace %q has no GenAI spans attributed to agent %q", opts.traceID, target.Name),
			"check that the trace contains a span with gen_ai.operation.name and matching gen_ai.agent.name "+
				"or gen_ai.agent.id; inherited agent identity on infrastructure spans alone does not qualify",
		)
	}

	export.Completeness = assessTraceCompleteness(opts.traceID, export.Spans, truncated, partialError)
	return export, nil
}

// assessTraceCompleteness reports missing parents, a missing root, truncation, partial
// query errors, and sampling. A trace is complete only when none of these limit it.
//
// Platform services (for example, the Foundry agent service) can start a trace and
// call into the agent without exporting their own spans to the project's Application
// Insights resource. A missing parent is classified as upstream when every span that
// references it is the earliest recorded span of its cloud role, which is how an
// incoming call from another service appears. Upstream parents are reported but do
// not make the trace incomplete. Any other missing parent indicates lost spans.
func assessTraceCompleteness(traceID string, spans []traceSpan, truncated bool, partialError string) traceCompleteness {
	ids := make(map[string]bool, len(spans))
	firstOfRole := map[string]int{}
	for i, span := range spans {
		if span.SpanID != "" {
			ids[span.SpanID] = true
		}
		if span.Role == "" {
			continue
		}
		if first, ok := firstOfRole[span.Role]; !ok || span.start.Before(spans[first].start) {
			firstOfRole[span.Role] = i
		}
	}

	result := traceCompleteness{
		Truncated:             truncated,
		PartialError:          partialError,
		UpstreamParentSpanIDs: []string{},
		MissingParentSpanIDs:  []string{},
		Notes:                 []string{},
	}
	// upstream[parent] is true while every orphan referencing parent is a role entry span.
	upstream := map[string]bool{}
	for i, span := range spans {
		parent := span.ParentSpanID
		if parent == "" || parent == traceID {
			result.RootSpanFound = true
			continue
		}
		if ids[parent] {
			continue
		}
		first, ok := firstOfRole[span.Role]
		entry := ok && first == i
		if prior, seen := upstream[parent]; seen {
			upstream[parent] = prior && entry
		} else {
			upstream[parent] = entry
		}
	}
	sampledSpans := 0
	for _, span := range spans {
		if span.ItemCount > 1 {
			result.Sampled = true
			sampledSpans++
		}
	}
	for parent, isUpstream := range upstream {
		if isUpstream {
			result.UpstreamParentSpanIDs = append(result.UpstreamParentSpanIDs, parent)
		} else {
			result.MissingParentSpanIDs = append(result.MissingParentSpanIDs, parent)
		}
	}
	slices.Sort(result.UpstreamParentSpanIDs)
	slices.Sort(result.MissingParentSpanIDs)

	if !result.RootSpanFound && len(result.UpstreamParentSpanIDs) == 0 {
		result.Notes = append(result.Notes,
			"the root span was not found; it may be outside the --since window, past retention, or not yet ingested")
	}
	if len(result.MissingParentSpanIDs) > 0 {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"%d parent span(s) referenced by recorded spans are missing; spans from the same service were lost, "+
				"sampled out, or are outside the --since window", len(result.MissingParentSpanIDs)))
	}
	if truncated {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"the trace has more than %d spans; only the first %d were exported", maxTraceExportSpans, maxTraceExportSpans))
	}
	if partialError != "" {
		result.Notes = append(result.Notes, "the query returned a partial error: "+partialError)
	}
	if result.Sampled {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"telemetry is sampled; %d span record(s) have item_count > 1 and each stands for several similar operations",
			sampledSpans))
	}

	result.Complete = (result.RootSpanFound || len(result.UpstreamParentSpanIDs) > 0) &&
		len(result.MissingParentSpanIDs) == 0 && !truncated && partialError == ""
	return result
}

func genAISpanMatchesAgent(attributes map[string]any, agentName string) bool {
	operation, _ := attributes["gen_ai.operation.name"].(string)
	if operation == "" {
		return false
	}
	name, _ := attributes["gen_ai.agent.name"].(string)
	id, _ := attributes["gen_ai.agent.id"].(string)
	if strings.EqualFold(name, agentName) || strings.EqualFold(id, agentName) {
		return true
	}
	prefix := agentName + ":"
	return len(id) > len(prefix) && strings.EqualFold(id[:len(prefix)], prefix)
}

// sensitiveAttributeNames are attribute name segments whose values carry credentials.
var sensitiveAttributeNames = map[string]bool{
	"authorization":             true,
	"proxy-authorization":       true,
	"cookie":                    true,
	"set-cookie":                true,
	"api-key":                   true,
	"api_key":                   true,
	"apikey":                    true,
	"x-api-key":                 true,
	"ocp-apim-subscription-key": true,
	"password":                  true,
	"secret":                    true,
	"client_secret":             true,
	"access_token":              true,
	"refresh_token":             true,
	"id_token":                  true,
	"connection_string":         true,
	"connectionstring":          true,
}

// redactTraceAttributes replaces credential-bearing attribute values. Prompt and
// response content recorded by the agent is preserved.
func redactTraceAttributes(attributes map[string]any) map[string]any {
	redacted := make(map[string]any, len(attributes))
	for key, value := range attributes {
		lower := strings.ToLower(key)
		segment := lower[strings.LastIndexAny(lower, ".")+1:]
		if sensitiveAttributeNames[segment] {
			redacted[key] = redactedValue
			continue
		}
		if text, ok := value.(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "bearer ") {
			redacted[key] = redactedValue
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func partialQueryError(response *azure.LogsQueryResult) string {
	if response == nil || response.Error == nil {
		return ""
	}
	message := response.Error.Message
	for _, detail := range response.Error.Details {
		if detail.Message != "" {
			message += ": " + detail.Message
			break
		}
	}
	if message == "" {
		message = response.Error.Code
	}
	return message
}

func writeTraceList(result *traceListResult, opts *traceOptions, out, errOut io.Writer) error {
	if result.Truncated {
		fmt.Fprintf(errOut, "WARNING: more traces matched; showing the newest %d. Narrow --since or raise --tail.\n",
			opts.limit)
	}
	if result.PartialError != "" {
		fmt.Fprintf(errOut, "WARNING: results may be incomplete; the query returned a partial error: %s\n",
			result.PartialError)
	}

	emptyNotice := fmt.Sprintf("No traces found for agent %q in the last %s.", result.AgentName, opts.sinceLabel)
	emptyHint := "Traces appear only after the agent emits OpenTelemetry spans to the connected Application Insights " +
		"resource; ingestion can take several minutes, and data outside retention or without read access is not returned."

	if opts.outFile != "" {
		if err := writeTraceJSONFile(opts.outFile, result); err != nil {
			return err
		}
		if len(result.Data) == 0 {
			fmt.Fprintln(errOut, emptyNotice)
		}
		_, err := fmt.Fprintf(errOut, "Wrote %d trace(s) to %s\n", len(result.Data), opts.outFile)
		return err
	}

	if opts.output == "json" {
		if len(result.Data) == 0 {
			fmt.Fprintln(errOut, emptyNotice+" "+emptyHint)
		}
		return writeIndentedJSON(out, result)
	}

	fmt.Fprintf(out, "Agent: %s\nProject: %s\nTelemetry: %s (%s)\nWindow: %s to %s\n\n",
		result.AgentName, result.ProjectEndpoint, result.Telemetry.ConnectionName, result.Telemetry.ResourceID,
		result.Window.Start, result.Window.End)
	if len(result.Data) == 0 {
		_, err := fmt.Fprintf(out, "%s\n%s\n", emptyNotice, emptyHint)
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TRACE ID\tSTART\tDURATION\tSPANS\tFAILED\tSTATUS\tVERSION\tROOT SPAN")
	fmt.Fprintln(w, "--------\t-----\t--------\t-----\t------\t------\t-------\t---------")
	for _, trace := range result.Data {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			trace.TraceID, formatTraceTime(trace.start, opts.utc), msDuration(float64(trace.DurationMs)),
			trace.SpanCount, trace.FailedSpanCount, trace.Status, trace.AgentVersion, trace.RootSpanName)
	}
	return w.Flush()
}

func writeTraceExport(export *traceExport, opts *traceOptions, out, errOut io.Writer) error {
	for _, note := range export.Completeness.Notes {
		fmt.Fprintf(errOut, "WARNING: %s\n", note)
	}
	if !export.Completeness.Complete {
		fmt.Fprintf(errOut, "WARNING: trace %s is incomplete; see completeness in the exported JSON.\n", export.TraceID)
	}

	if opts.outFile != "" {
		if err := writeTraceJSONFile(opts.outFile, export); err != nil {
			return err
		}
		_, err := fmt.Fprintf(errOut, "Wrote trace %s (%d spans, %d exceptions) to %s\n",
			export.TraceID, len(export.Spans), len(export.Exceptions), opts.outFile)
		return err
	}

	if opts.output == "json" {
		return writeIndentedJSON(out, export)
	}

	complete := "yes"
	if !export.Completeness.Complete {
		complete = "no"
	}
	fmt.Fprintf(out, "Trace: %s\nAgent: %s\nTelemetry: %s (%s)\nSpans: %d  Exceptions: %d  Complete: %s\n\n",
		export.TraceID, export.AgentName, export.Telemetry.ConnectionName, export.Telemetry.ResourceID,
		len(export.Spans), len(export.Exceptions), complete)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "START\tDURATION\tSTATUS\tKIND\tSPAN")
	fmt.Fprintln(w, "-----\t--------\t------\t----\t----")
	for _, row := range orderSpanTree(export.TraceID, export.Spans) {
		span := row.span
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s\n",
			formatTraceTime(span.start, opts.utc), msDuration(span.DurationMs), span.Status, span.Kind,
			strings.Repeat("  ", row.depth), span.Name)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "\nUse --output json or --out-file to export span IDs, parent links, and attributes.")
	return err
}

type spanTreeRow struct {
	span  traceSpan
	depth int
}

// orderSpanTree returns spans depth-first under their parents. Spans whose parent is
// missing are listed as roots.
func orderSpanTree(traceID string, spans []traceSpan) []spanTreeRow {
	ids := make(map[string]bool, len(spans))
	for _, span := range spans {
		if span.SpanID != "" {
			ids[span.SpanID] = true
		}
	}
	children := map[string][]traceSpan{}
	var roots []traceSpan
	for _, span := range spans {
		if span.ParentSpanID == "" || span.ParentSpanID == traceID || !ids[span.ParentSpanID] ||
			span.ParentSpanID == span.SpanID {
			roots = append(roots, span)
			continue
		}
		children[span.ParentSpanID] = append(children[span.ParentSpanID], span)
	}

	rows := make([]spanTreeRow, 0, len(spans))
	visited := map[string]bool{}
	var walk func(span traceSpan, depth int)
	walk = func(span traceSpan, depth int) {
		if span.SpanID != "" {
			if visited[span.SpanID] {
				return
			}
			visited[span.SpanID] = true
		}
		rows = append(rows, spanTreeRow{span: span, depth: depth})
		for _, child := range children[span.SpanID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

// writeTraceJSONFile writes trace output with owner-only permissions because traces
// can contain prompts, responses, and tool data.
func writeTraceJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Clean(path), data, 0o600); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("failed to write %s: %v", path, err),
			"check that the --out-file directory exists and is writable",
		)
	}
	return nil
}

func formatTraceTime(t time.Time, utc bool) string {
	if utc {
		return t.UTC().Format("2006-01-02 15:04:05.000")
	}
	return t.Local().Format("2006-01-02 15:04:05.000")
}

func msDuration(ms float64) time.Duration {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond)).Round(time.Millisecond)
}

func recordString(record map[string]any, key string) string {
	switch value := record[key].(type) {
	case string:
		return value
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func recordFloat(record map[string]any, key string) float64 {
	switch value := record[key].(type) {
	case float64:
		return value
	case string:
		parsed, _ := strconv.ParseFloat(value, 64)
		return parsed
	}
	return 0
}

func recordInt(record map[string]any, key string) int64 {
	return int64(recordFloat(record, key))
}

func recordTime(record map[string]any, key string) (time.Time, bool) {
	value, ok := record[key].(string)
	if !ok || value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

// recordDynamic decodes a dynamic column, which the query API returns as JSON text.
func recordDynamic(record map[string]any, key string) map[string]any {
	switch value := record[key].(type) {
	case map[string]any:
		return value
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(value), &decoded) == nil && decoded != nil {
			return decoded
		}
	}
	return map[string]any{}
}
