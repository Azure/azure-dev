// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	tasksPreviewFeature = "Routines=V2Preview"
	// These are local safety caps, not service contract limits. The transport applies
	// them before SDK body downloading and retry draining can consume a response.
	maxTaskResponseBytes      = 32 * 1024 * 1024
	maxTaskErrorResponseBytes = 64 * 1024
)

type taskTargetKey struct{}

type taskResponseTransport struct {
	next policy.Transporter
}

// newTaskPipeline retains the shared authentication, correlation, user-agent and
// read retry policies. Only its transport and the URLs visible to SDK logging differ.
func newTaskPipeline(options *policy.ClientOptions, transport policy.Transporter) runtime.Pipeline {
	optionsCopy := *options
	optionsCopy.Transport = &taskResponseTransport{next: transport}
	optionsCopy.Logging.IncludeBody = false
	return runtime.NewPipeline("azure-ai-agents", "v1.0.0", runtime.PipelineOptions{}, &optionsCopy)
}

func (t *taskResponseTransport) Do(req *http.Request) (*http.Response, error) {
	target, ok := req.Context().Value(taskTargetKey{}).(*url.URL)
	if !ok {
		return nil, errors.New("missing task request target")
	}
	// SDK retry/request logging does not redact URL path segments. Keep the actual
	// task ID (and opaque cursor) outside that pipeline's URL, restoring it only on
	// a clone at the HTTP boundary. Never return the real request through a response.
	wireRequest := req.Clone(req.Context())
	targetCopy := *target
	wireRequest.URL = &targetCopy
	resp, err := t.next.Do(wireRequest)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, safeTaskIOError(req.Context(), "sending task request")
	}
	if resp == nil {
		return nil, errors.New("missing task response")
	}
	limit := maxTaskResponseBytes
	if resp.StatusCode != http.StatusOK {
		limit = maxTaskErrorResponseBytes
	}
	var body []byte
	if resp.Body != nil {
		body, err = io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		_ = resp.Body.Close()
	}
	if err != nil || req.Context().Err() != nil {
		return nil, safeTaskIOError(req.Context(), "reading task response")
	}
	headers := http.Header{}
	// Keep only parsed retry hints: even normally safe response headers may echo
	// customer content. SDK read retries still honor the service's retry delay.
	for _, key := range []string{"Retry-After", "Retry-After-Ms", "X-Ms-Retry-After-Ms"} {
		value := resp.Header.Get(key)
		if n, err := strconv.ParseUint(value, 10, 63); err == nil {
			headers.Set(key, strconv.FormatUint(n, 10))
		} else if key == "Retry-After" {
			if date, err := http.ParseTime(value); err == nil {
				headers.Set(key, date.UTC().Format(http.TimeFormat))
			}
		}
	}
	if resp.StatusCode != http.StatusOK {
		code := allowlistedTaskErrorCode(resp.Header.Get("X-Ms-Error-Code"))
		if code == "" && len(body) <= limit {
			var envelope struct {
				Code  string `json:"code"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &envelope) == nil {
				code = allowlistedTaskErrorCode(envelope.Error.Code)
				if code == "" {
					code = allowlistedTaskErrorCode(envelope.Code)
				}
			}
		}
		headers.Set("X-Ms-Error-Code", code)
		body = nil
	} else if len(body) > limit {
		return nil, &taskResponseTooLargeError{}
	}
	return &http.Response{
		StatusCode: resp.StatusCode,
		Status:     http.StatusText(resp.StatusCode),
		Header:     headers,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

type taskResponseTooLargeError struct{}

func (*taskResponseTooLargeError) Error() string {
	return "task response exceeds the local safety cap of 32 MiB"
}

// NonRetriable prevents retrying a locally rejected oversized successful response.
func (*taskResponseTooLargeError) NonRetriable() {}

func safeTaskIOError(ctx context.Context, operation string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New(operation + " failed")
}

func allowlistedTaskErrorCode(code string) string {
	switch code {
	case "Forbidden", "PermissionDenied", "AuthorizationFailed", "NotFound", "TaskNotFound",
		"ResourceNotFound", "UnsupportedApiVersion", "InvalidApiVersionParameter", "ApiVersionNotSupported",
		"forbidden", "permission_denied", "hosted_agent_required", "not_found", "task_not_found",
		"unsupported_api_version", "invalid_api_version", "api_version_not_supported":
		return code
	default:
		return ""
	}
}

// ListTasks retrieves one agent-filtered page without changing service JSON or following cursors.
func (c *AgentClient) ListTasks(ctx context.Context, options TaskListOptions) (json.RawMessage, error) {
	query, err := taskListQuery(options)
	if err != nil {
		return nil, err
	}
	return c.taskRequest(ctx, "", query)
}

// GetTask retrieves one project-scoped Task by its exact, unencoded ID.
func (c *AgentClient) GetTask(ctx context.Context, id string) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("task ID is required")
	}
	return c.taskRequest(ctx, id, url.Values{})
}

func taskListQuery(options TaskListOptions) (url.Values, error) {
	if strings.TrimSpace(options.AgentName) == "" {
		return nil, errors.New("agent name is required")
	}
	if options.Limit < 0 || options.Limit > 100 {
		return nil, errors.New("limit must be between 1 and 100, or omitted")
	}
	switch options.Status {
	case "", "pending", "in_progress", "suspended", "completed":
	default:
		return nil, errors.New("status must be pending, in_progress, suspended, or completed")
	}
	if options.Order != "" && options.Order != "asc" && options.Order != "desc" {
		return nil, errors.New("order must be asc or desc")
	}
	query := url.Values{}
	for key, value := range map[string]string{
		"agent_name": options.AgentName, "session_id": options.SessionID, "status": options.Status,
		"source_type": options.SourceType, "lease_owner": options.LeaseOwner, "after": options.After, "order": options.Order,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if options.Limit != 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	for key, value := range map[string]*bool{
		"has_error": options.HasError, "lease_expired": options.LeaseExpired,
		"omit_attachment_values": options.OmitAttachmentValues,
	} {
		if value != nil {
			query.Set(key, strconv.FormatBool(*value))
		}
	}
	for _, tag := range options.Tags {
		key, value, found := strings.Cut(tag, ":")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, errors.New("tags must use key:value with nonempty keys and values")
		}
		query.Add("tag", tag)
	}
	return query, nil
}

func (c *AgentClient) taskRequest(ctx context.Context, id string, query url.Values) (json.RawMessage, error) {
	base, err := url.Parse(c.endpoint)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil ||
		base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, errors.New("invalid task project endpoint")
	}
	endpoint := strings.TrimRight(c.endpoint, "/") + "/tasks"
	if id != "" {
		segment := url.PathEscape(id)
		// Dot-only IDs must not become traversal segments for intermediaries.
		if id == "." || id == ".." {
			segment = strings.ReplaceAll(id, ".", "%2E")
		}
		endpoint += "/" + segment
	}
	query.Set("api-version", AgentEndpointAPIVersion)
	target, err := url.Parse(endpoint + "?" + query.Encode())
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return nil, errors.New("invalid task project endpoint")
	}
	ctx = context.WithValue(ctx, taskTargetKey{}, target)
	masked := *target
	masked.Path, masked.RawPath, masked.RawQuery = "/tasks", "", "api-version="+AgentEndpointAPIVersion
	req, err := runtime.NewRequest(ctx, http.MethodGet, masked.String())
	if err != nil {
		return nil, errors.New("creating task request failed")
	}
	req.Raw().Header.Set("Foundry-Features", tasksPreviewFeature)
	resp, err := c.tasksPipeline.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// All errors from the private transport are sanitized before SDK logging.
		// Authentication policies can still return arbitrary errors: do not expose them.
		if _, ok := errors.AsType[*taskResponseTooLargeError](err); ok {
			return nil, err
		}
		return nil, errors.New("sending task request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &azcore.ResponseError{
			StatusCode: resp.StatusCode,
			ErrorCode:  allowlistedTaskErrorCode(resp.Header.Get("X-Ms-Error-Code")),
			RawResponse: &http.Response{
				StatusCode: resp.StatusCode, Status: http.StatusText(resp.StatusCode), Body: http.NoBody,
			},
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTaskResponseBytes+1))
	if err != nil {
		return nil, safeTaskIOError(ctx, "reading task response")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(body) > maxTaskResponseBytes {
		return nil, &taskResponseTooLargeError{}
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("invalid task response: expected one JSON object")
	}
	return json.RawMessage(body), nil
}
