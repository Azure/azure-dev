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
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/recordproxy"
)

// Contract assertions are grounded in the public Tasks API's routes.tsp and models.tsp:
// https://github.com/Azure/azure-rest-api-specs/pull/45095/files
// Lifecycle and opaque application payload semantics are documented in:
// https://github.com/Azure/azure-sdk-for-python/blob/main/sdk/agentserver/azure-ai-agentserver-core/docs/tasks-guide.md
// Tests additionally cover the live-validated v1/preview-header wire contract
// and local CLI safety policies; these are not new service API guarantees.

type taskRoundTripFunc func(*http.Request) (*http.Response, error)

func (f taskRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newTaskHTTPClient(t *testing.T, transport http.RoundTripper) (*AgentClient, *previewTokenCredential) {
	t.Helper()
	original := recordproxy.Transport
	recordproxy.Transport = transport
	t.Cleanup(func() { recordproxy.Transport = original })
	credential := &previewTokenCredential{}
	return NewAgentClient("https://account.services.ai.azure.com/api/projects/project/", credential), credential
}

func taskHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Request: req,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestTasksHTTPContract(t *testing.T) {
	const raw = " \n{\"data\":[{\"status\":\"future_status\",\"n\":9007199254740993123456789," +
		"\"input\":{\"unknown\":[true,null,1.234567890123456789]}}]," +
		"\"last_id\":\"opaque+/= %cursor\",\"has_more\":true,\"new_field\":{\"a\":1}}\n"
	options := TaskListOptions{
		AgentName: "agent/作業 &+", SessionID: "session/?#%", Status: "in_progress",
		SourceType: "future_type", LeaseOwner: "worker|session:1", After: "opaque+/= %cursor",
		Order: "asc", Limit: 100, HasError: new(false), LeaseExpired: new(true),
		Tags:                 []string{"kind:review", "url:https://example/a:b?x=1&y=2", "kind:second"},
		OmitAttachmentValues: new(false),
	}
	var requests []*http.Request
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Empty(t, data)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, tasksPreviewFeature, r.Header.Get("Foundry-Features"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"))
		assert.Empty(t, r.Header.Get("Content-Type"))
		assert.Empty(t, r.Header.Get(UserIdentityHeader))
		_, _ = io.WriteString(w, raw)
	}))
	defer server.Close()
	client, credential := newTaskHTTPClient(t, server.Client().Transport)
	client.endpoint = server.URL + "/api/projects/project/"
	result, err := client.ListTasks(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, raw, string(result))
	require.Equal(t, []string{"https://ai.azure.com/.default"}, credential.scopes)
	require.Len(t, requests, 1)
	require.Equal(t, "/api/projects/project/tasks", requests[0].URL.EscapedPath())
	require.Equal(t, url.Values{
		"api-version": {"v1"}, "agent_name": {options.AgentName}, "session_id": {options.SessionID},
		"status": {"in_progress"}, "source_type": {"future_type"}, "lease_owner": {options.LeaseOwner},
		"after": {options.After}, "order": {"asc"}, "limit": {"100"}, "has_error": {"false"},
		"lease_expired": {"true"}, "tag": options.Tags, "omit_attachment_values": {"false"},
	}, requests[0].URL.Query())

	for _, id := range []string{"task/作業 ?#%+&=", "%2F", ".", "..", "../task"} {
		result, err := client.GetTask(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, raw, string(result))
		request := requests[len(requests)-1]
		escapedID := url.PathEscape(id)
		if id == "." || id == ".." {
			escapedID = strings.ReplaceAll(id, ".", "%2E")
		}
		require.Equal(t, "/api/projects/project/tasks/"+escapedID, request.URL.EscapedPath())
		require.Equal(t, url.Values{"api-version": {"v1"}}, request.URL.Query())
	}
}

func TestTasksOptionalFilters(t *testing.T) {
	var requests []*http.Request
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r)
		return taskHTTPResponse(r, 200, `{}`), nil
	}))
	_, err := client.ListTasks(t.Context(), TaskListOptions{AgentName: "agent"})
	require.NoError(t, err)
	require.Equal(t, url.Values{"api-version": {"v1"}, "agent_name": {"agent"}}, requests[0].URL.Query())
	for _, value := range []bool{false, true} {
		_, err := client.ListTasks(t.Context(), TaskListOptions{
			AgentName: "agent", HasError: new(value), LeaseExpired: new(value), OmitAttachmentValues: new(value),
		})
		require.NoError(t, err)
		query := requests[len(requests)-1].URL.Query()
		for _, key := range []string{"has_error", "lease_expired", "omit_attachment_values"} {
			if value {
				require.Equal(t, "true", query.Get(key))
			} else {
				require.Equal(t, "false", query.Get(key))
			}
		}
	}
	for _, status := range []string{"pending", "in_progress", "suspended", "completed"} {
		_, err := client.ListTasks(t.Context(), TaskListOptions{AgentName: "agent", Status: status, Limit: 1, Order: "desc"})
		require.NoError(t, err)
	}
}

func TestTasksValidationBeforeHTTP(t *testing.T) {
	requests := 0
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return taskHTTPResponse(r, 200, `{}`), nil
	}))
	for _, options := range []TaskListOptions{
		{}, {AgentName: " "}, {AgentName: "a", Limit: -1}, {AgentName: "a", Limit: 101},
		{AgentName: "a", Status: "failed"}, {AgentName: "a", Status: "cancelled"},
		{AgentName: "a", Status: "secret-status"}, {AgentName: "a", Order: "secret-order"},
		{AgentName: "a", Tags: []string{"secret-tag"}}, {AgentName: "a", Tags: []string{":value"}},
		{AgentName: "a", Tags: []string{"key:"}}, {AgentName: "a", Tags: []string{" :value"}},
	} {
		_, err := client.ListTasks(t.Context(), options)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
	for _, id := range []string{"", " "} {
		_, err := client.GetTask(t.Context(), id)
		require.Error(t, err)
	}
	for _, endpoint := range []string{
		"http://example.test/project", "https://user:password@example.test/project", "https://",
		"https://example.test/project?secret=cursor", "https://example.test/project#secret", "https://%zz",
	} {
		client.endpoint = endpoint
		_, err := client.GetTask(t.Context(), "secret-id")
		require.EqualError(t, err, "invalid task project endpoint")
	}
	require.Zero(t, requests)
}

func TestTasksRawJSONValidation(t *testing.T) {
	for _, raw := range []string{
		"", " ", "null", "[]", `"secret"`, "123", "true", `{"secret":}`, `{} {}`, `{} secret`,
	} {
		t.Run(raw, func(t *testing.T) {
			client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return taskHTTPResponse(r, 200, raw), nil
			}))
			result, err := client.GetTask(t.Context(), "task")
			require.Nil(t, result)
			require.EqualError(t, err, "invalid task response: expected one JSON object")
		})
	}
}

func captureTaskLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	log.SetListener(func(_ log.Event, message string) { logs.WriteString(message) })
	// No event allowlist means all events, including retry and response errors.
	log.SetEvents()
	t.Cleanup(func() { log.SetListener(nil); log.SetEvents() })
	return &logs
}

func TestTasksSanitizeHTTPErrorsAndLogs(t *testing.T) {
	logs := captureTaskLogs(t)
	for _, tc := range []struct {
		status                         int
		body, headerCode, expectedCode string
	}{
		{403, `{"error":{"code":"PermissionDenied","message":"secret-body"}}`, "", "PermissionDenied"},
		{404, `{"code":"task_not_found","message":"secret-body"}`, "", "task_not_found"},
		{400, `{"error":{"code":"UnsupportedApiVersion","message":"secret-body"}}`, "", "UnsupportedApiVersion"},
		{403, `{"code":"secret-code","message":"secret-body"}`, "", ""},
		{404, `secret-body`, "secret-header-code", ""},
		{403, `secret-body`, "hosted_agent_required", "hosted_agent_required"},
		{302, `secret-body`, "", ""},
	} {
		client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp := taskHTTPResponse(r, tc.status, tc.body)
			resp.Header.Set("X-Ms-Error-Code", tc.headerCode)
			resp.Header.Set("X-Ms-Request-Id", "secret-header")
			resp.Status = "secret-status"
			return resp, nil
		}))
		for _, call := range []func() (json.RawMessage, error){
			func() (json.RawMessage, error) { return client.GetTask(t.Context(), "secret-task-id") },
			func() (json.RawMessage, error) {
				return client.ListTasks(t.Context(), TaskListOptions{AgentName: "secret-agent", After: "secret-cursor"})
			},
		} {
			result, err := call()
			require.Nil(t, result)
			require.Error(t, err)
			responseError, ok := errors.AsType[*azcore.ResponseError](err)
			require.True(t, ok)
			require.Equal(t, tc.status, responseError.StatusCode)
			require.Equal(t, tc.expectedCode, responseError.ErrorCode)
			require.Nil(t, responseError.RawResponse.Request)
			require.Empty(t, responseError.RawResponse.Header)
			body, readErr := io.ReadAll(responseError.RawResponse.Body)
			require.NoError(t, readErr)
			require.Empty(t, body)
			require.NotContains(t, err.Error(), "secret")
		}
	}
	require.NotContains(t, logs.String(), "secret")
}

type taskCountingBody struct {
	remaining, read int
	closed          bool
}

func (b *taskCountingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := range n {
		p[i] = ' '
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *taskCountingBody) Close() error { b.closed = true; return nil }

func TestTasksResponseSafetyCaps(t *testing.T) {
	for _, status := range []int{200, 403, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var bodies []*taskCountingBody
			client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := &taskCountingBody{remaining: maxTaskResponseBytes * 2}
				bodies = append(bodies, body)
				resp := taskHTTPResponse(r, status, "")
				resp.Body = body
				return resp, nil
			}))
			ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{
				MaxRetries: 2, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond,
			})
			_, err := client.GetTask(ctx, "secret-id")
			require.Error(t, err)
			limit := maxTaskErrorResponseBytes
			attempts := 1
			if status == 200 {
				limit = maxTaskResponseBytes
				require.ErrorContains(t, err, "32 MiB")
			} else {
				responseError, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				require.Equal(t, status, responseError.StatusCode)
				if status == 503 {
					attempts = 3
				}
			}
			require.Len(t, bodies, attempts)
			for _, body := range bodies {
				require.True(t, body.closed)
				require.Equal(t, limit+1, body.read, "retry draining must not read the underlying unbounded body")
			}
		})
	}
}

func TestTasksRetriesAndCursorPreservation(t *testing.T) {
	attempts := 0
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		require.Equal(t, "opaque+/= %cursor", r.URL.Query().Get("after"))
		if attempts < 3 {
			return taskHTTPResponse(r, 429, `{"message":"secret-body"}`), nil
		}
		return taskHTTPResponse(r, 200, `{"last_id":"next+/=cursor","has_more":true}`), nil
	}))
	ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{
		MaxRetries: 2, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond,
	})
	result, err := client.ListTasks(ctx, TaskListOptions{AgentName: "agent", After: "opaque+/= %cursor"})
	require.NoError(t, err)
	require.Equal(t, `{"last_id":"next+/=cursor","has_more":true}`, string(result))
	require.Equal(t, 3, attempts, "must not fetch subsequent pages")
}

func TestTasksTransportErrorsAndCancellation(t *testing.T) {
	logs := captureTaskLogs(t)
	t.Run("transport errors are sanitized before retry logging", func(t *testing.T) {
		client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("secret-body " + r.URL.String())
		}))
		ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{
			MaxRetries: 1, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond,
		})
		_, err := client.GetTask(ctx, "secret-id")
		require.EqualError(t, err, "sending task request failed")
	})
	for _, deadline := range []bool{false, true} {
		t.Run("cancel", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
			}
			defer cancel()
			client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
				return nil, errors.New("secret-transport")
			}))
			_, err := client.ListTasks(ctx, TaskListOptions{AgentName: "secret-agent", After: "secret-cursor"})
			if deadline {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
	require.NotContains(t, logs.String(), "secret")
}

type taskErrorBody struct {
	err    error
	closed bool
}

func (b *taskErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (b *taskErrorBody) Close() error             { b.closed = true; return nil }

func TestTasksBodyReadErrorsAreSanitized(t *testing.T) {
	logs := captureTaskLogs(t)
	for _, status := range []int{200, 503} {
		body := &taskErrorBody{err: errors.New("secret-body-error")}
		client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp := taskHTTPResponse(r, status, "")
			resp.Body = body
			return resp, nil
		}))
		ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{MaxRetries: -1})
		_, err := client.GetTask(ctx, "secret-id")
		require.EqualError(t, err, "sending task request failed")
		require.True(t, body.closed)
	}
	require.NotContains(t, logs.String(), "secret")
}

func TestTasksDoNotFollowRedirects(t *testing.T) {
	requests := 0
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		resp := taskHTTPResponse(r, 302, "secret-body")
		resp.Header.Set("Location", "https://untrusted.example/secret-id")
		return resp, nil
	}))
	_, err := client.GetTask(t.Context(), "task")
	require.Error(t, err)
	require.Equal(t, 1, requests)
}

func TestTasksSuccessfulBodiesStayOutOfLogs(t *testing.T) {
	logs := captureTaskLogs(t)
	const raw = `{"id":"secret-id","input":{"secret-body":9007199254740993},"error":"secret-error"}`
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return taskHTTPResponse(r, 200, raw), nil
	}))
	result, err := client.GetTask(t.Context(), "secret-id")
	require.NoError(t, err)
	require.Equal(t, raw, string(result))
	_, err = client.ListTasks(t.Context(), TaskListOptions{AgentName: "secret-agent", After: "secret-cursor"})
	require.NoError(t, err)
	require.NotEmpty(t, logs.String(), "the privacy assertion must run with SDK logging enabled")
	require.NotContains(t, logs.String(), "secret")
}

func TestTasksAcceptResponseAtSafetyCap(t *testing.T) {
	body := "{}" + strings.Repeat(" ", maxTaskResponseBytes-2)
	client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return taskHTTPResponse(r, 200, body), nil
	}))
	result, err := client.GetTask(t.Context(), "task")
	require.NoError(t, err)
	require.Len(t, result, maxTaskResponseBytes)
	require.Equal(t, body, string(result))
}

type taskCancelBody struct {
	cancel context.CancelFunc
	closed bool
}

func (b *taskCancelBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, errors.New("secret-canceled-read")
}

func (b *taskCancelBody) Close() error { b.closed = true; return nil }

func TestTasksCancellationDuringResponseAndRetryDelay(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry delay", true: "body read"}[duringRead], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			attempts := 0
			body := &taskCancelBody{cancel: cancel}
			client, _ := newTaskHTTPClient(t, taskRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				if duringRead {
					response := taskHTTPResponse(r, 200, "")
					response.Body = body
					return response, nil
				}
				response := taskHTTPResponse(r, 429, `{"message":"secret-body"}`)
				response.Header.Set("Retry-After", "30")
				return response, nil
			}))
			_, err := client.GetTask(ctx, "secret-id")
			if duringRead {
				require.ErrorIs(t, err, context.Canceled)
				require.True(t, body.closed)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.Equal(t, 1, attempts)
		})
	}
}
