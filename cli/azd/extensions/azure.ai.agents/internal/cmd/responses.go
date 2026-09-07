// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type responseCommandFlags struct {
	userIdentityFlags
	agentName     string
	agentEndpoint string
	responseID    string
	version       string
	clientHeaders []string
	noPrompt      bool
	output        string
}

func newResponsesCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "responses",
		Short: "Manage Responses for a hosted agent endpoint.",
		Long: `Manage Responses for a hosted agent endpoint.

Show, follow, and cancel Responses created by 'azd ai agent invoke'. When a
Response ID is omitted, the command uses the latest Response saved for the
selected agent. Explicit IDs do not change the saved current Response.`,
	}
	cmd.AddCommand(newResponsesShowCommand(extCtx))
	cmd.AddCommand(newResponsesFollowCommand(extCtx))
	cmd.AddCommand(newResponsesCancelCommand(extCtx))
	return cmd
}

func addResponseCommandFlags(cmd *cobra.Command, flags *responseCommandFlags) {
	cmd.Flags().StringVarP(
		&flags.agentName,
		"agent-name",
		"n",
		"",
		"Agent name (matches azure.yaml service name; auto-detected when only one exists)",
	)
	cmd.Flags().StringVar(
		&flags.agentEndpoint,
		"agent-endpoint",
		"",
		"Full Responses endpoint URL of a deployed agent",
	)
	cmd.Flags().StringVar(&flags.responseID, "response-id", "", "Response ID; defaults to the current Response")
	cmd.Flags().StringVar(&flags.version, "version", "", "Agent version used to select saved Response state")
	cmd.Flags().StringArrayVar(
		&flags.clientHeaders,
		"client-header",
		nil,
		`Custom x-client-* request header in "Name: Value" format (repeatable)`,
	)
	addUserIdentityFlag(cmd, &flags.userIdentityFlags)
}

func newResponsesShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &responseCommandFlags{}
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a Response snapshot.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.noPrompt = extCtx.NoPrompt
			flags.output = extCtx.OutputFormat
			return runResponseShow(azdext.WithAccessToken(cmd.Context()), flags, cmd.OutOrStdout())
		},
	}
	addResponseCommandFlags(cmd, flags)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "json",
	})
	return cmd
}

func newResponsesFollowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &responseCommandFlags{}
	cmd := &cobra.Command{
		Use:   "follow",
		Short: "Replay and follow a Response until completion.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.noPrompt = extCtx.NoPrompt
			return runResponseFollow(azdext.WithAccessToken(cmd.Context()), flags, cmd.OutOrStdout())
		},
	}
	addResponseCommandFlags(cmd, flags)
	return cmd
}

func newResponsesCancelCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &responseCommandFlags{}
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel a Response.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.noPrompt = extCtx.NoPrompt
			return runResponseCancel(azdext.WithAccessToken(cmd.Context()), flags, cmd.OutOrStdout())
		},
	}
	addResponseCommandFlags(cmd, flags)
	return cmd
}

func resolveResponseCommand(
	ctx context.Context,
	flags *responseCommandFlags,
) (*InvokeAction, *remoteContext, string, error) {
	headers, err := parseCustomHeaders(flags.clientHeaders)
	if err != nil {
		return nil, nil, "", err
	}
	invokeFlags := &invokeFlags{
		name:          flags.agentName,
		agentEndpoint: flags.agentEndpoint,
		version:       flags.version,
	}
	invokeFlags.userIdentityFlags = flags.userIdentityFlags
	action := &InvokeAction{flags: invokeFlags, noPrompt: flags.noPrompt, clientHeaders: headers}
	if flags.agentEndpoint != "" {
		if flags.agentName != "" || flags.version != "" {
			return nil, nil, "", exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-endpoint cannot be combined with --agent-name or --version",
				"remove --agent-name and --version; the endpoint identifies the deployed agent",
			)
		}
		parsed, err := parseAgentEndpoint(flags.agentEndpoint)
		if err != nil {
			return nil, nil, "", err
		}
		if parsed.Protocol != agent_api.AgentProtocolResponses {
			return nil, nil, "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--agent-endpoint must identify a Responses protocol endpoint",
				"use the endpoint ending in /protocols/openai/responses",
			)
		}
		action.endpoint = parsed
		invokeFlags.name = parsed.AgentName
	}

	rc, err := action.resolveRemoteContext(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	responseID := flags.responseID
	if responseID == "" {
		if rc.azdClient == nil || rc.agentKey == "" {
			if rc.azdClient != nil {
				rc.azdClient.Close()
			}
			return nil, nil, "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"no Response ID was provided and current Response state is unavailable",
				"provide --response-id or run through azd after invoking the agent",
			)
		}
		record, err := newUserConfigResponseStateStore(rc.azdClient).Get(ctx, rc.agentKey)
		if err != nil {
			rc.azdClient.Close()
			return nil, nil, "", classifyResponseStateReadError(err)
		}
		if record == nil || record.ResponseID == "" {
			rc.azdClient.Close()
			return nil, nil, "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"no current Response is saved for the selected agent",
				"run `azd ai agent invoke \"<message>\"` or provide --response-id",
			)
		}
		responseID = record.ResponseID
	}
	return action, rc, responseID, nil
}

func classifyResponseStateReadError(cause error) error {
	if _, ok := errors.AsType[*azdext.ConfigError](cause); !ok {
		return exterrors.FromHost(cause, exterrors.OpReadResponseState, "reading current Response state failed")
	}
	return exterrors.Validation(
		exterrors.CodeInvalidResponseState,
		fmt.Sprintf("saved Response state at %q could not be read: %v", responsesConfigPath, cause),
		fmt.Sprintf(
			"clear the invalid state with `azd config unset %s`, or repair that config value",
			responsesConfigPath,
		),
	)
}

func classifyResponseLifecycleError(cause error, operation, label string) error {
	if cause == nil {
		return nil
	}
	httpErr, ok := errors.AsType[*responseLifecycleHTTPError](cause)
	if !ok {
		return cause
	}
	serviceName := ""
	if parsed, err := url.Parse(httpErr.requestURL); err == nil {
		serviceName = parsed.Hostname()
	}
	serviceErr := exterrors.Service(
		operation,
		strconv.Itoa(httpErr.statusCode),
		fmt.Sprintf("%s failed with HTTP %d: %s", label, httpErr.statusCode, httpErr.status),
		serviceName,
		"",
	)
	serviceErr.StatusCode = httpErr.statusCode
	if cause.Error() != httpErr.Error() {
		serviceErr.Suggestion = "use `azd ai agent responses show` to inspect the Response, or run the follow command again"
	}
	return serviceErr
}

func runResponseShow(ctx context.Context, flags *responseCommandFlags, writer io.Writer) error {
	action, rc, responseID, err := resolveResponseCommand(ctx, flags)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}
	result, err := action.getResponseSnapshot(ctx, rc, responseID)
	if err != nil {
		return classifyResponseLifecycleError(err, exterrors.OpShowResponse, "showing Response")
	}
	return printResponseSnapshot(writer, result, flags.output)
}

func runResponseFollow(ctx context.Context, flags *responseCommandFlags, writer io.Writer) error {
	action, rc, responseID, err := resolveResponseCommand(ctx, flags)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}
	return classifyResponseLifecycleError(
		action.followResponse(ctx, rc, responseID, writer),
		exterrors.OpFollowResponse,
		"following Response",
	)
}

func runResponseCancel(
	ctx context.Context,
	flags *responseCommandFlags,
	writer io.Writer,
) (returnErr error) {
	defer func() {
		returnErr = classifyResponseLifecycleError(returnErr, exterrors.OpCancelResponse, "cancelling Response")
	}()
	action, rc, responseID, err := resolveResponseCommand(ctx, flags)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}

	token, err := action.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	cancelURL := buildResponseCancelURL(rc.projectEndpoint, rc.name, responseID, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cancelURL, nil)
	if err != nil {
		return fmt.Errorf("create Response cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, action.clientHeaders)
	applyRemoteUserIdentityHeader(req, &action.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("cancel Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read Response cancel result: %w", readErr)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		// Cancellation is idempotent from the CLI perspective. Confirm a terminal
		// state from the service rather than trusting stale local status.
		result, snapshotErr := action.getResponseSnapshot(ctx, rc, responseID)
		if snapshotErr == nil && isTerminalResponseStatus(result.snapshot.Status) {
			_, err = fmt.Fprintf(writer, "Response %s is already %s; nothing to cancel.\n", responseID, result.snapshot.Status)
			return err
		}
		return &responseLifecycleHTTPError{
			method:     http.MethodPost,
			requestURL: cancelURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}

	result, decodeErr := decodeResponseSnapshot(body)
	if decodeErr == nil && result.Status != "" {
		_, err = fmt.Fprintf(writer, "Response %s is %s.\n", responseID, result.Status)
		return err
	}
	_, err = fmt.Fprintf(writer, "Cancellation requested for Response %s.\n", responseID)
	return err
}

func (a *InvokeAction) getResponseSnapshot(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
) (responseSnapshotResult, error) {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return responseSnapshotResult{}, err
	}
	snapshotURL := buildResponseLifecycleURL(rc.projectEndpoint, rc.name, responseID, rc.apiVersion, false)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("create Response show request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("show Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("read Response snapshot: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return responseSnapshotResult{}, &responseLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: snapshotURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}
	snapshot, err := decodeResponseSnapshot(body)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("decode Response snapshot: %w", err)
	}
	actualID := snapshot.ID
	if actualID == "" {
		actualID = snapshot.ResponseID
	}
	if actualID != "" && actualID != responseID {
		return responseSnapshotResult{}, fmt.Errorf("Response snapshot ID %q does not match requested ID %q", actualID, responseID)
	}
	return responseSnapshotResult{snapshot: snapshot, raw: body}, nil
}

func decodeResponseSnapshot(body []byte) (responsesSnapshot, error) {
	var snapshot responsesSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return responsesSnapshot{}, err
	}
	return snapshot, nil
}

func printResponseSnapshot(writer io.Writer, result responseSnapshotResult, format string) error {
	if format != "table" {
		var formatted any
		if err := json.Unmarshal(result.raw, &formatted); err != nil {
			return fmt.Errorf("decode Response JSON: %w", err)
		}
		data, err := json.MarshalIndent(formatted, "", "  ")
		if err != nil {
			return fmt.Errorf("format Response JSON: %w", err)
		}
		_, err = fmt.Fprintln(writer, string(data))
		return err
	}

	id := result.snapshot.ID
	if id == "" {
		id = result.snapshot.ResponseID
	}
	table := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "FIELD\tVALUE")
	fmt.Fprintln(table, "-----\t-----")
	fmt.Fprintf(table, "Response ID\t%s\n", id)
	fmt.Fprintf(table, "Status\t%s\n", result.snapshot.Status)
	fmt.Fprintf(table, "Session ID\t%s\n", result.snapshot.AgentSessionID)
	return table.Flush()
}

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

const responsesConfigPath = configPathPrefix + ".responses"

type savedResponse struct {
	ResponseID string `json:"responseId"`
}

type responseStateStore interface {
	Get(ctx context.Context, agentKey string) (*savedResponse, error)
	Save(ctx context.Context, agentKey string, record savedResponse) error
	Delete(ctx context.Context, agentKey string) error
}

type userConfigResponseStateStore struct {
	client *azdext.AzdClient
}

func newUserConfigResponseStateStore(client *azdext.AzdClient) responseStateStore {
	return &userConfigResponseStateStore{client: client}
}

func (s *userConfigResponseStateStore) Get(ctx context.Context, agentKey string) (*savedResponse, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		return nil, nil
	}
	record, ok := records[agentKey]
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (s *userConfigResponseStateStore) Save(ctx context.Context, agentKey string, record savedResponse) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedResponse)
	}
	records[agentKey] = record

	if err := config.SetUserJSON(ctx, responsesConfigPath, records); err != nil {
		return fmt.Errorf("write responses: %w", err)
	}
	return nil
}

func (s *userConfigResponseStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		return nil
	}
	delete(records, agentKey)
	if err := config.SetUserJSON(ctx, responsesConfigPath, records); err != nil {
		return fmt.Errorf("write responses: %w", err)
	}
	return nil
}

// followResponse performs one streaming GET. A later command replays the
// Response from the beginning; azd does not maintain a replay cursor.
func (a *InvokeAction) followResponse(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
	writer io.Writer,
) error {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	followURL := buildResponseLifecycleURL(
		rc.projectEndpoint,
		rc.name,
		responseID,
		rc.apiVersion,
		true,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, followURL, nil)
	if err != nil {
		return fmt.Errorf("create Response follow request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := responseStreamHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("follow Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return &responseLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: followURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}

	if err := readResponsesSSE(
		ctx,
		resp.Body,
		writer,
		rc.name,
		responsesSSEOptions{
			requireTerminal:    true,
			expectedResponseID: responseID,
		},
	); err != nil {
		if errors.Is(err, errResponsesStreamDisconnected) {
			return fmt.Errorf(
				"%w; rerun `azd ai agent responses follow --response-id %s` to replay and follow again",
				err,
				responseID,
			)
		}
		return err
	}
	return nil
}

type responseLifecycleHTTPError struct {
	method     string
	requestURL string
	statusCode int
	status     string
	body       []byte
}

func (e *responseLifecycleHTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with HTTP %d: %s\n%s", e.method, e.requestURL, e.statusCode, e.status, e.body)
}

func responseStreamHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

// buildResponsesURL builds the Foundry "openai/responses" protocol URL for an agent.
// apiVersion is URL-encoded so unusual characters cannot break out of the query value.
func buildResponsesURL(projectEndpoint, agentName, apiVersion string) string {
	if apiVersion == "" {
		apiVersion = DefaultAgentAPIVersion
	}
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/responses?api-version=%s",
		projectEndpoint, agentName, url.QueryEscape(apiVersion),
	)
}

func buildResponseLifecycleURL(
	projectEndpoint string,
	agentName string,
	responseID string,
	apiVersion string,
	stream bool,
) string {
	if apiVersion == "" {
		apiVersion = DefaultAgentAPIVersion
	}
	base := fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/responses/%s",
		projectEndpoint,
		agentName,
		url.PathEscape(responseID),
	)
	query := url.Values{"api-version": []string{apiVersion}}
	if stream {
		query.Set("stream", "true")
	}
	return base + "?" + query.Encode()
}

func buildResponseCancelURL(projectEndpoint, agentName, responseID, apiVersion string) string {
	lifecycleURL := buildResponseLifecycleURL(projectEndpoint, agentName, responseID, apiVersion, false)
	parts := strings.SplitN(lifecycleURL, "?", 2)
	return parts[0] + "/cancel?" + parts[1]
}
