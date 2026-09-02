// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"azure.ai.loom/internal/experimenttracking"
	"azure.ai.loom/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

const maxExperimentInputBytes = 64 << 20

const experimentAPIKeyEnv = "AZURE_AI_PROJECT_API_KEY"

type experimentFlags struct {
	projectEndpoint string
	projectID       string
	apiVersion      string
}

type runRequestFlags struct {
	experimentFlags
	runID string
	take  int
}

func newExperimentRunCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Inspect Foundry experiment-tracking runs.",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newRunListCommand(extCtx))
	cmd.AddCommand(newRunHistoryKeysCommand(extCtx))
	cmd.AddCommand(newRunSummaryCommand(extCtx))
	cmd.AddCommand(newRunMetricsCommand(extCtx))
	cmd.AddCommand(newRunSystemMetricsCommand(extCtx))
	cmd.AddCommand(newRunLogsCommand(extCtx))
	cmd.AddCommand(newRunLogRecordsCommand(extCtx))
	cmd.AddCommand(newRunTraceCommand(extCtx))
	cmd.AddCommand(newRunCompareCommand(extCtx))
	cmd.AddCommand(newRunSpansCommand(extCtx))
	cmd.AddCommand(newExperimentIngestCommand(extCtx))
	cmd.AddCommand(newExperimentWandBCommand(extCtx))
	return cmd
}

func newRunListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{take: 10}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List experiment-tracking runs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.take <= 0 {
				return invalidExperimentParameter("take", "--take must be greater than zero")
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			return executeExperimentJSON(
				cmd,
				client,
				http.MethodGet,
				"runs",
				takeQuery(flags.take),
				nil,
				nil,
			)
		},
	}
	addExperimentFlags(cmd, &flags.experimentFlags)
	cmd.Flags().IntVar(&flags.take, "take", 10, "Maximum number of runs to return")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunHistoryKeysCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(
		extCtx,
		"history-keys",
		"List history keys for a run.",
		"history/keys",
		false,
	)
}

func newRunSummaryCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(extCtx, "summary", "Get the summary for a run.", "summary", true)
}

func newRunMetricsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(extCtx, "metrics", "List metrics for a run.", "metrics", true)
}

func newRunLogsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(extCtx, "logs", "Get console logs for a run.", "logs", true)
}

func newRunLogRecordsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(extCtx, "log-records", "Get structured log records for a run.", "log-records", true)
}

func newRunTracesCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newRunGetCommand(extCtx, "list", "List traces for a run.", "traces", true)
}

func newRunGetCommand(
	extCtx *azdext.ExtensionContext,
	use string,
	short string,
	suffix string,
	withTake bool,
) *cobra.Command {
	flags := &runRequestFlags{take: 10}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValue("run-id", flags.runID); err != nil {
				return err
			}
			if withTake && flags.take <= 0 {
				return invalidExperimentParameter("take", "--take must be greater than zero")
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			var query url.Values
			if withTake {
				query = takeQuery(flags.take)
			}
			headers := http.Header(nil)
			if suffix == "metrics" {
				headers = client.RunHeaders(flags.runID)
			}
			return executeExperimentJSON(
				cmd,
				client,
				http.MethodGet,
				fmt.Sprintf("runs/%s/%s", url.PathEscape(flags.runID), suffix),
				query,
				headers,
				nil,
			)
		},
	}
	addRunFlags(cmd, flags, withTake)
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunSystemMetricsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{take: 10}
	var names []string
	cmd := &cobra.Command{
		Use:   "system-metrics",
		Short: "Get selected system metrics for a run.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValue("run-id", flags.runID); err != nil {
				return err
			}
			if len(names) == 0 {
				return invalidExperimentParameter("name", "provide at least one system metric name")
			}
			if flags.take <= 0 {
				return invalidExperimentParameter("take", "--take must be greater than zero")
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			query := takeQuery(flags.take)
			for _, name := range names {
				query.Add("names", name)
			}
			return executeExperimentJSON(
				cmd,
				client,
				http.MethodGet,
				fmt.Sprintf("runs/%s/system-metrics", url.PathEscape(flags.runID)),
				query,
				nil,
				nil,
			)
		},
	}
	addRunFlags(cmd, flags, true)
	cmd.Flags().StringSliceVar(&names, "name", nil, "System metric name; may be specified multiple times")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunTraceCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Inspect or analyze a run trace.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newRunTracesCommand(extCtx))
	cmd.AddCommand(newRunTraceShowCommand(extCtx))
	cmd.AddCommand(newRunTraceChatCommand(extCtx))
	return cmd
}

func newRunTraceShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{}
	var traceID string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Get one trace and its details.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValues(map[string]string{"run-id": flags.runID, "trace-id": traceID}); err != nil {
				return err
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			return executeExperimentJSON(
				cmd,
				client,
				http.MethodGet,
				fmt.Sprintf(
					"runs/%s/traces/%s",
					url.PathEscape(flags.runID),
					url.PathEscape(traceID),
				),
				nil,
				nil,
				nil,
			)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunTraceChatCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{}
	var traceID string
	var requestFile string
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Request the agent trace-chat response for a trace.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValue("run-id", flags.runID); err != nil {
				return err
			}
			if requestFile == "" {
				if err := requireValue("trace-id", traceID); err != nil {
					return err
				}
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}

			body := any(map[string]any{
				"project_id": client.ProjectID(),
				"trace_id":   traceID,
			})
			if requestFile != "" {
				body, err = readJSONObject(requestFile)
				if err != nil {
					return err
				}
			}

			return executeExperimentJSON(
				cmd,
				client,
				http.MethodPost,
				fmt.Sprintf("runs/%s/agents/traces/chat", url.PathEscape(flags.runID)),
				nil,
				client.RunHeaders(flags.runID),
				body,
			)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID")
	cmd.Flags().StringVar(&requestFile, "request-file", "", "Path to a complete JSON request body")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunCompareCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &experimentFlags{}
	var runIDs []string
	var metricNames []string
	var minStep float64
	var maxStep float64
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare metrics across experiment-tracking runs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(runIDs) < 2 {
				return invalidExperimentParameter("run-id", "provide at least two run IDs")
			}
			if len(metricNames) == 0 {
				return invalidExperimentParameter("metric", "provide at least one metric name")
			}
			if math.IsNaN(minStep) || math.IsInf(minStep, 0) {
				return invalidExperimentParameter("min", "--min must be a finite number")
			}
			if math.IsNaN(maxStep) || math.IsInf(maxStep, 0) {
				return invalidExperimentParameter("max", "--max must be a finite number")
			}
			if maxStep < minStep {
				return invalidExperimentParameter("max", "--max must be greater than or equal to --min")
			}
			client, err := newExperimentClient(cmd.Context(), *flags)
			if err != nil {
				return err
			}
			body := map[string]any{
				"runIds":      runIDs,
				"metricNames": metricNames,
				"min":         minStep,
				"max":         maxStep,
			}
			return executeExperimentJSON(cmd, client, http.MethodPost, "runs/compare", nil, nil, body)
		},
	}
	addExperimentFlags(cmd, flags)
	cmd.Flags().StringSliceVar(&runIDs, "run-id", nil, "Run ID; specify at least twice")
	cmd.Flags().StringSliceVar(&metricNames, "metric", nil, "Metric name; may be specified multiple times")
	cmd.Flags().Float64Var(&minStep, "min", 0, "Minimum metric step")
	cmd.Flags().Float64Var(&maxStep, "max", 0, "Maximum metric step")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newRunSpansCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "span",
		Short: "Query run-scoped spans.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newRunSpansQueryCommand(extCtx))
	return cmd
}

func newRunSpansQueryCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{}
	var filter string
	var filterFile string
	var requestFile string
	var includeDetails bool
	var limit int

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query spans using a JSON filter expression.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValue("run-id", flags.runID); err != nil {
				return err
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}

			var body any
			if requestFile != "" {
				if filter != "" || filterFile != "" {
					return invalidExperimentParameter(
						"request-file",
						"--request-file cannot be combined with --filter or --filter-file",
					)
				}
				body, err = readJSONObject(requestFile)
			} else {
				query, queryErr := readFilterExpression(filter, filterFile)
				if queryErr != nil {
					return queryErr
				}
				if limit <= 0 {
					return invalidExperimentParameter("limit", "--limit must be greater than zero")
				}
				body = buildSpanQueryBody(client.ProjectID(), query, includeDetails, limit)
			}
			if err != nil {
				return err
			}

			return executeExperimentJSON(
				cmd,
				client,
				http.MethodPost,
				fmt.Sprintf("runs/%s/agents/spans/query", url.PathEscape(flags.runID)),
				nil,
				client.RunHeaders(flags.runID),
				body,
			)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&filter, "filter", "", "Inline JSON span filter expression")
	cmd.Flags().StringVar(&filterFile, "filter-file", "", "Path to a JSON span filter expression")
	cmd.Flags().StringVar(&requestFile, "request-file", "", "Path to a complete JSON request body")
	cmd.Flags().BoolVar(&includeDetails, "include-details", false, "Include detailed span data")
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of spans to return")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newExperimentIngestCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Send telemetry to Foundry experiment tracking.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newOTLPIngestCommand(extCtx, "metrics"))
	cmd.AddCommand(newOTLPIngestCommand(extCtx, "logs"))
	cmd.AddCommand(newOTLPIngestCommand(extCtx, "traces"))
	cmd.AddCommand(newAgentTracesIngestCommand(extCtx))
	return cmd
}

func newOTLPIngestCommand(extCtx *azdext.ExtensionContext, signal string) *cobra.Command {
	flags := &runRequestFlags{}
	var file string
	cmd := &cobra.Command{
		Use:   signal,
		Short: fmt.Sprintf("Ingest OTLP %s from a protobuf file or stdin.", signal),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValues(map[string]string{"run-id": flags.runID, "file": file}); err != nil {
				return err
			}
			payload, err := readNonEmptyExperimentInput(file)
			if err != nil {
				return err
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			response, err := client.DoBytes(
				cmd.Context(),
				http.MethodPost,
				"protocols/otlp/v1/"+signal,
				nil,
				client.RunHeaders(flags.runID),
				"application/x-protobuf",
				payload,
			)
			if err != nil {
				return classifyExperimentError(err)
			}
			formattedResponse, err := formatOTLPIngestResponse(signal, response)
			if err != nil {
				return err
			}
			return writeExperimentResponse(cmd, formattedResponse, nil)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&file, "file", "", "Protobuf payload path, or - for stdin")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newAgentTracesIngestCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{}
	var file string
	cmd := &cobra.Command{
		Use:   "agent-traces",
		Short: "Ingest agent OTEL traces from a JSON file or stdin.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValues(map[string]string{"run-id": flags.runID, "file": file}); err != nil {
				return err
			}
			body, err := readJSONObject(file)
			if err != nil {
				return err
			}
			setAgentTracesRunID(body, flags.runID)
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			return executeExperimentJSON(cmd, client, http.MethodPost, "agents/otel/v1/traces", nil, nil, body)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&file, "file", "", "JSON payload path, or - for stdin")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newExperimentWandBCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wandb",
		Short: "Use experiment-tracking W&B compatibility APIs.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newWandBGraphQLCommand(extCtx))
	cmd.AddCommand(newWandBFileStreamCommand(extCtx))
	return cmd
}

func newWandBGraphQLCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &experimentFlags{}
	var file string
	cmd := &cobra.Command{
		Use:   "graphql",
		Short: "Execute a W&B-compatible GraphQL request.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValue("file", file); err != nil {
				return err
			}
			body, err := readJSONObject(file)
			if err != nil {
				return err
			}
			client, err := newExperimentClient(cmd.Context(), *flags)
			if err != nil {
				return err
			}
			return executeExperimentJSON(cmd, client, http.MethodPost, "graphql", nil, nil, body)
		},
	}
	addExperimentFlags(cmd, flags)
	cmd.Flags().StringVar(&file, "file", "", "GraphQL JSON request path, or - for stdin")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func newWandBFileStreamCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runRequestFlags{}
	var entity string
	var project string
	var file string
	cmd := &cobra.Command{
		Use:   "file-stream",
		Short: "Send a W&B-compatible FileStream payload.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireValues(map[string]string{
				"run-id": flags.runID,
				"file":   file,
			}); err != nil {
				return err
			}
			body, err := readJSONObject(file)
			if err != nil {
				return err
			}
			client, err := newExperimentClient(cmd.Context(), flags.experimentFlags)
			if err != nil {
				return err
			}
			if project == "" {
				project = client.ProjectID()
			}
			if entity == "" {
				entity = client.AccountID()
			}
			apiPath := fmt.Sprintf(
				"files/%s/%s/%s/file_stream",
				url.PathEscape(entity),
				url.PathEscape(project),
				url.PathEscape(flags.runID),
			)
			return executeExperimentJSON(cmd, client, http.MethodPost, apiPath, nil, nil, body)
		},
	}
	addRunFlags(cmd, flags, false)
	cmd.Flags().StringVar(&entity, "entity", "", "W&B entity or account name")
	cmd.Flags().StringVar(&project, "wandb-project", "", "W&B project name; defaults to the Foundry project ID")
	cmd.Flags().StringVar(&file, "file", "", "FileStream JSON request path, or - for stdin")
	registerJSONOutput(cmd, extCtx)
	return cmd
}

func addExperimentFlags(cmd *cobra.Command, flags *experimentFlags) {
	cmd.Flags().StringVarP(
		&flags.projectEndpoint,
		"project-endpoint",
		"p",
		"",
		"Foundry project endpoint URL",
	)
	cmd.Flags().StringVar(
		&flags.projectID,
		"project-id",
		"",
		"Override the project ID derived from the endpoint",
	)
	cmd.Flags().StringVar(&flags.apiVersion, "api-version", "v1", "Experiment-tracking API version")
}

func addRunFlags(cmd *cobra.Command, flags *runRequestFlags, withTake bool) {
	addExperimentFlags(cmd, &flags.experimentFlags)
	cmd.Flags().StringVar(&flags.runID, "run-id", "", "Experiment-tracking run ID")
	if withTake {
		cmd.Flags().IntVar(&flags.take, "take", 10, "Maximum number of records to return")
	}
}

func registerJSONOutput(cmd *cobra.Command, _ *azdext.ExtensionContext) {
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json"},
		Default:       "json",
	})
}

func newExperimentClient(
	ctx context.Context,
	flags experimentFlags,
) (*experimenttracking.Client, error) {
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{
		FlagValue: flags.projectEndpoint,
	})
	if err != nil {
		return nil, err
	}

	if apiKey := os.Getenv(experimentAPIKeyEnv); apiKey != "" {
		client, err := experimenttracking.NewClientWithAPIKey(
			resolved.Endpoint,
			flags.projectID,
			flags.apiVersion,
			apiKey,
		)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("configure experiment-tracking client: %s", err),
				"verify the Foundry project endpoint and API key configuration",
			)
		}
		return client, nil
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(nil)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("create Azure Developer CLI credential: %s", err),
			"run 'azd auth login' and retry",
		)
	}

	client, err := experimenttracking.NewClient(
		resolved.Endpoint,
		flags.projectID,
		flags.apiVersion,
		credential,
	)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("configure experiment-tracking client: %s", err),
			"provide a standard Foundry project endpoint or set --project-id explicitly",
		)
	}
	return client, nil
}

func executeExperimentJSON(
	cmd *cobra.Command,
	client *experimenttracking.Client,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	body any,
) error {
	response, err := client.DoJSON(cmd.Context(), method, apiPath, query, headers, body)
	return writeExperimentResponse(cmd, response, classifyExperimentError(err))
}

func classifyExperimentError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "access token") {
		return exterrors.Auth(
			exterrors.CodeAuthenticationFailed,
			fmt.Sprintf("authenticate to Foundry experiment tracking: %s", err),
			"run 'azd auth login' and verify access to the Foundry project",
		)
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpExperimentRequest)
}

func writeExperimentResponse(cmd *cobra.Command, response json.RawMessage, err error) error {
	if err != nil {
		return err
	}

	var formatted bytes.Buffer
	if indentErr := json.Indent(&formatted, response, "", "  "); indentErr != nil {
		return exterrors.Internal(
			exterrors.CodeExperimentRequestFailed,
			fmt.Sprintf("format experiment-tracking response: %s", indentErr),
		)
	}
	formatted.WriteByte('\n')
	_, writeErr := formatted.WriteTo(cmd.OutOrStdout())
	if writeErr != nil {
		return fmt.Errorf("write response: %w", writeErr)
	}
	return nil
}

func takeQuery(take int) url.Values {
	query := make(url.Values)
	query.Set("take", strconv.Itoa(take))
	return query
}

func readFilterExpression(inline string, file string) (json.RawMessage, error) {
	if inline != "" && file != "" {
		return nil, invalidExperimentParameter("filter", "--filter and --filter-file cannot be combined")
	}

	data := []byte(`{"$expr":true}`)
	var err error
	if file != "" {
		data, err = readExperimentInput(file)
	} else if inline != "" {
		data = []byte(inline)
	}
	if err != nil {
		return nil, err
	}
	if !isJSONObject(data) {
		return nil, invalidExperimentPayload("span filter must be a JSON object")
	}
	return json.RawMessage(data), nil
}

func buildSpanQueryBody(
	projectID string,
	query json.RawMessage,
	includeDetails bool,
	limit int,
) map[string]any {
	return map[string]any{
		"project_id":      projectID,
		"query":           query,
		"include_details": includeDetails,
		"limit":           limit,
	}
}

func readJSONObject(file string) (map[string]any, error) {
	data, err := readExperimentInput(file)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, invalidExperimentPayload(fmt.Sprintf("parse JSON object: %s", err))
	}
	if object == nil {
		return nil, invalidExperimentPayload("payload must be a JSON object")
	}
	if err := ensureJSONDocumentEnd(decoder); err != nil {
		return nil, invalidExperimentPayload(fmt.Sprintf("parse JSON object: %s", err))
	}
	return object, nil
}

func ensureJSONDocumentEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("payload must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func isJSONObject(data []byte) bool {
	var object map[string]any
	return json.Unmarshal(data, &object) == nil && object != nil
}

func formatOTLPIngestResponse(signal string, response []byte) (json.RawMessage, error) {
	if isJSONObject(response) {
		if bytes.Equal(bytes.TrimSpace(response), []byte("{}")) {
			return json.RawMessage(`{"status":"accepted"}`), nil
		}
		return response, nil
	}

	var partialSuccess map[string]any
	switch signal {
	case "metrics":
		decoded := &collectormetrics.ExportMetricsServiceResponse{}
		if err := proto.Unmarshal(response, decoded); err != nil {
			return nil, fmt.Errorf("decode OTLP metrics response: %w", err)
		}
		if partial := decoded.GetPartialSuccess(); partial != nil &&
			(partial.GetRejectedDataPoints() != 0 || partial.GetErrorMessage() != "") {
			partialSuccess = map[string]any{
				"rejected_data_points": partial.GetRejectedDataPoints(),
				"error_message":        partial.GetErrorMessage(),
			}
		}
	case "logs":
		decoded := &collectorlogs.ExportLogsServiceResponse{}
		if err := proto.Unmarshal(response, decoded); err != nil {
			return nil, fmt.Errorf("decode OTLP logs response: %w", err)
		}
		if partial := decoded.GetPartialSuccess(); partial != nil &&
			(partial.GetRejectedLogRecords() != 0 || partial.GetErrorMessage() != "") {
			partialSuccess = map[string]any{
				"rejected_log_records": partial.GetRejectedLogRecords(),
				"error_message":        partial.GetErrorMessage(),
			}
		}
	case "traces":
		decoded := &collectortrace.ExportTraceServiceResponse{}
		if err := proto.Unmarshal(response, decoded); err != nil {
			return nil, fmt.Errorf("decode OTLP traces response: %w", err)
		}
		if partial := decoded.GetPartialSuccess(); partial != nil &&
			(partial.GetRejectedSpans() != 0 || partial.GetErrorMessage() != "") {
			partialSuccess = map[string]any{
				"rejected_spans": partial.GetRejectedSpans(),
				"error_message":  partial.GetErrorMessage(),
			}
		}
	default:
		return nil, fmt.Errorf("unsupported OTLP signal %q", signal)
	}

	if partialSuccess == nil {
		return json.RawMessage(`{"status":"accepted"}`), nil
	}
	formatted, err := json.Marshal(map[string]any{
		"status":          "partial_success",
		"partial_success": partialSuccess,
	})
	if err != nil {
		return nil, fmt.Errorf("encode OTLP response: %w", err)
	}
	return formatted, nil
}

func setAgentTracesRunID(body map[string]any, runID string) {
	body["run_id"] = runID
}

func readNonEmptyExperimentInput(file string) ([]byte, error) {
	data, err := readExperimentInput(file)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, invalidExperimentPayload("payload must not be empty")
	}
	return data, nil
}

func readExperimentInput(file string) ([]byte, error) {
	var reader io.Reader
	if file == "-" {
		reader = os.Stdin
	} else {
		//nolint:gosec // The payload path is explicitly supplied by the user.
		opened, err := os.Open(file)
		if err != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeExperimentInputReadFailed,
				fmt.Sprintf("open experiment payload %q: %s", file, err),
				"verify the file path and permissions",
			)
		}
		defer opened.Close()
		reader = opened
	}

	data, err := io.ReadAll(io.LimitReader(reader, maxExperimentInputBytes+1))
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeExperimentInputReadFailed,
			fmt.Sprintf("read experiment payload: %s", err),
			"verify the input can be read and retry",
		)
	}
	if len(data) > maxExperimentInputBytes {
		return nil, invalidExperimentPayload(
			fmt.Sprintf("payload exceeds %d bytes", maxExperimentInputBytes),
		)
	}
	return data, nil
}

func requireValue(name string, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalidExperimentParameter(name, fmt.Sprintf("--%s is required", name))
	}
	return nil
}

func requireValues(values map[string]string) error {
	for name, value := range values {
		if err := requireValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

func invalidExperimentParameter(name string, message string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		message,
		fmt.Sprintf("provide a valid --%s value", name),
	)
}

func invalidExperimentPayload(message string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidExperimentPayload,
		message,
		"provide a valid JSON object using the documented request schema",
	)
}
