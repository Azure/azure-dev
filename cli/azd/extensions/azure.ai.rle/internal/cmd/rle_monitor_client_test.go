// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azure.ai.rle/internal/monitor"
)

const realMonitorJobID = "ftjob-7898670d3ae8453e89e591f9"

func TestRleRolloutEventsReadContract(t *testing.T) {
	const id = "3c27c30f5fba261c3a7a3e856b4e1388"
	for _, tc := range []struct {
		name, body, token string
		status            int
		wantError         bool
	}{
		{"progress", `{"data":[{"sequence":2,"phase":"execute","status":"running","message":"Started",` +
			`"elapsed_ms":12,"partial_response":{"reward":0}}],"nextContinuationToken":"next"}`, "", 200, false},
		{"empty page with token", `{"data":[],"nextContinuationToken":"next"}`, "opaque+/=&", 200, false},
		{"last page", `{"data":[],"nextContinuationToken":null}`, "next", 200, false},
		{"missing data", `{}`, "", 200, true},
		{"null data", `{"data":null}`, "", 200, true},
		{"invalid data", `{"data":{}}`, "", 200, true},
		{"not found", `{"error":{"code":"RolloutNotFound"}}`, "", 404, true},
		{"throttled", `{"error":{"code":"TooManyRequests"}}`, "", 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet ||
					r.URL.Path != "/api/projects/project/rl_environments/rollouts/"+id+"/events" {
					t.Errorf("unexpected events request %s %s", r.Method, r.URL)
				}
				query := r.URL.Query()
				if query.Get("limit") != "100" || query.Get("api-version") != foundryAPIVersion ||
					query.Get("continuationToken") != tc.token || query.Has("lastSequence") {
					t.Errorf("unexpected events query %s", r.URL.RawQuery)
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("events request is missing Foundry authentication")
				}
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := newRleClientWithCredential(server.URL+"/api/projects/project", &testTokenCredential{})
			client.httpClient = server.Client()
			source := &rleJobSource{rle: client, jobID: realMonitorJobID}
			page, err := source.Events(t.Context(), id, tc.token)
			if (err != nil) != tc.wantError {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			if !tc.wantError {
				var want monitor.JobPage[json.RawMessage]
				if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
					t.Fatal(err)
				}
				if len(page.Data) != len(want.Data) || page.Next != want.Next {
					t.Fatalf("page=%+v, want %+v", page, want)
				}
				for i := range want.Data {
					if string(page.Data[i]) != string(want.Data[i]) {
						t.Fatalf("event payload changed: %s", page.Data[i])
					}
				}
			}
			if tc.status != http.StatusOK {
				readErr, ok := errors.AsType[*monitor.ReadError](err)
				if !ok || readErr.Status != tc.status || readErr.RetryAfter != 17*time.Second {
					t.Fatalf("missing event read error details: %v", err)
				}
			}
			if _, err := source.Events(t.Context(), "../invalid", ""); err == nil || calls != 1 {
				t.Fatal("invalid rollout ID reached the service")
			}
		})
	}
}

func stubAPIJobMonitor(t *testing.T) {
	t.Helper()
	oldRun, oldRle := runAPIJobMonitor, createRleClient
	t.Cleanup(func() { runAPIJobMonitor, createRleClient = oldRun, oldRle })
	createRleClient = func(endpoint string) (*rleClient, error) {
		return newRleClientWithCredential(endpoint, &testTokenCredential{}), nil
	}
	runAPIJobMonitor = func(context.Context, monitor.JobSource, string, bool, io.Writer, io.Writer) error {
		return nil
	}
}

func TestTrainAndMonitorUseTheSameAPIBackend(t *testing.T) {
	action, _ := stubbedTrain(t, t.Context(), &rleTrainFlags{})
	stubAPIJobMonitor(t)
	calls := 0
	runAPIJobMonitor = func(
		ctx context.Context, source monitor.JobSource, id string, noBrowser bool, out, errOut io.Writer,
	) error {
		calls++
		s, ok := source.(*rleJobSource)
		if !ok || id != realMonitorJobID || s.jobID != id ||
			s.rle.baseUrl != "https://account.services.ai.azure.com/api/projects/project" {
			t.Fatalf("unexpected monitor source: %#v", source)
		}
		s.rle.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			want := "https://account.services.ai.azure.com/api/projects/project" +
				finetuneJobsPath + "/" + realMonitorJobID
			if req.Method != http.MethodGet || req.URL.String() != want {
				t.Fatalf("unexpected status request %s %s", req.Method, req.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(`{"id":"` + id + `","status":"succeeded"}`)),
			}, nil
		})
		if status, err := source.Status(ctx); err != nil || status != "succeeded" {
			t.Fatalf("status=%q err=%v", status, err)
		}
		return nil
	}
	if err := action.Run(); err != nil {
		t.Fatal(err)
	}
	originalCreateFT := createFinetuneClient
	createFinetuneClient = func(string) (*finetuneClient, error) {
		t.Fatal("standalone monitoring must not create a fine-tuning client")
		return nil, errors.New("unexpected fine-tuning client")
	}
	t.Cleanup(func() { createFinetuneClient = originalCreateFT })
	command := newMonitorCommand()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--job-id", realMonitorJobID, "--no-browser"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("monitor started %d times, want 2", calls)
	}
}

func TestRleJobStatusUsesFoundryFineTuningAPI(t *testing.T) {
	id := `"id":"` + realMonitorJobID + `"`
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{
		{"running without RLE metadata", `{` + id + `,"status":"running"}`, "running", 200},
		{"succeeded", `{` + id + `,"status":"succeeded"}`, "succeeded", 200},
		{"failed", `{` + id + `,"status":"failed"}`, "failed", 200},
		{"cancelled", `{` + id + `,"status":"cancelled"}`, "cancelled", 200},
		{"RLE metadata ignored", `{` + id + `,"status":"running","metadata":{"status":"succeeded"}}`, "running", 200},
		{"missing status", `{` + id + `}`, "", 200},
		{"blank status", `{` + id + `,"status":" "}`, "", 200},
		{"numeric status", `{` + id + `,"status":42}`, "", 200},
		{"wrong id", `{"id":"other","status":"running"}`, "", 200},
		{"malformed response", `{`, "", 200},
		{"unauthorized", `{"error":{"code":"Unauthorized"}}`, "", 401},
		{"not found", `{"error":{"code":"NotFound"}}`, "", 404},
		{"throttled", `{"error":{"code":"TooManyRequests"}}`, "", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := &testTokenCredential{}
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet ||
					r.URL.Path != "/api/projects/project/openai/v1/fine_tuning/jobs/"+realMonitorJobID ||
					r.URL.RawQuery != "" {
					t.Errorf("unexpected status request %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") == "" ||
					r.Header.Get("azureai-project") != "" || r.Header.Get("azureai-project-is-default") != "" {
					t.Error("status must use Foundry authentication without direct fine-tuning project headers")
				}
				w.Header().Set("Retry-After", "17")
				w.Header().Set("x-request-id", "status-request")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			rle := newRleClientWithCredential(server.URL+"/api/projects/project", credential)
			rle.httpClient = server.Client()
			source := &rleJobSource{rle: rle, jobID: realMonitorJobID}
			status, err := source.Status(t.Context())
			if (err != nil) != (tc.want == "") || status != tc.want {
				t.Fatalf("status=%q err=%v, want %q", status, err, tc.want)
			}
			if tc.code != http.StatusOK {
				readErr, ok := errors.AsType[*monitor.ReadError](err)
				if !ok || readErr.Status != tc.code || readErr.RetryAfter != 17*time.Second ||
					readErr.Request != "status-request" {
					t.Fatalf("missing service error details: %v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("status made %d requests, want one fine-tuning request and no RLE fallback", calls.Load())
			}
			if len(credential.scopes) != 1 || credential.scopes[0] != foundryTokenScope {
				t.Fatalf("unexpected status token scopes %v", credential.scopes)
			}
		})
	}
}

func TestRleJobConfigReadContract(t *testing.T) {
	id := `"id":"` + realMonitorJobID + `"`
	legacyID := `"job_id":"` + realMonitorJobID + `"`
	config := `{"model_name":"sample","max_steps":100}`
	envelope := `{` + id + `,"status":"succeeded","metadata":` + config + `}`
	for _, tc := range []struct {
		name, body, want, config string
		code                     int
	}{
		{"new envelope", envelope, "succeeded", config, 200},
		{"new running", `{` + id + `,"status":"running","metadata":{}}`, "running", `{}`, 200},
		{"completed", `{` + id + `,"status":"completed","metadata":{}}`, "completed", `{}`, 200},
		{"raw status", `{` + id + `,"status":"custom_state","metadata":{}}`, "custom_state", `{}`, 200},
		{"metadata status is opaque", `{` + id + `,"status":"running","metadata":{"status":42}}`,
			"running", `{"status":42}`, 200},
		{"no configuration unwrap", `{` + id + `,"status":"running","metadata":{"configuration":` + config + `}}`,
			"running", `{"configuration":` + config + `}`, 200},
		{"root config is not authoritative", `{` + id +
			`,"status":"running","model_name":"wrong","max_steps":1,"metadata":` + config + `}`,
			"running", config, 200},
		{"legacy id is ignored", `{` + id + `,` + legacyID + `,"status":"running","metadata":` + config + `}`,
			"running", config, 200},
		{"legacy running is rejected", `{` + legacyID + `,"status":"running"}`, "", "", 200},
		{"legacy config", `{` + legacyID + `,"status":"succeeded","model_name":"sample","max_steps":100}`,
			"", "", 200},
		{"legacy opaque metadata", `{` + legacyID + `,"status":"running","metadata":null}`,
			"", "", 200},
		{"legacy id with valid metadata is rejected", `{` + legacyID + `,"status":"running","metadata":` + config + `}`,
			"", "", 200},
		{"missing status", `{` + id + `,"metadata":{}}`, "", "", 200},
		{"blank status", `{` + id + `,"status":" ","metadata":{}}`, "", "", 200},
		{"null status", `{` + id + `,"status":null,"metadata":{}}`, "", "", 200},
		{"numeric status", `{` + id + `,"status":42,"metadata":{}}`, "", "", 200},
		{"object status", `{` + id + `,"status":{},"metadata":{}}`, "", "", 200},
		{"array status", `{` + id + `,"status":[],"metadata":{}}`, "", "", 200},
		{"boolean status", `{` + id + `,"status":true,"metadata":{}}`, "", "", 200},
		{"metadata status cannot replace status", `{` + id + `,"metadata":{"status":"succeeded"}}`, "", "", 200},
		{"wrong new job", `{"id":"ftjob-000000000000000000000000","status":"running","metadata":{}}`, "", "", 200},
		{"missing identity", `{"status":"running","metadata":{}}`, "", "", 200},
		{"null id cannot fall back", `{"id":null,` + legacyID + `,"status":"running","metadata":{}}`, "", "", 200},
		{"blank id cannot fall back", `{"id":"",` + legacyID + `,"status":"running","metadata":{}}`, "", "", 200},
		{"numeric id cannot fall back", `{"id":42,` + legacyID + `,"status":"running","metadata":{}}`, "", "", 200},
		{"object id", `{"id":{},"status":"running","metadata":{}}`, "", "", 200},
		{"array id", `{"id":[],"status":"running","metadata":{}}`, "", "", 200},
		{"boolean id", `{"id":true,"status":"running","metadata":{}}`, "", "", 200},
		{"conflicting legacy id is ignored", `{` + id + `,"job_id":"other","status":"running","metadata":{}}`,
			"running", `{}`, 200},
		{"wrong id cannot fall back", `{"id":"other",` + legacyID + `,"status":"running","metadata":{}}`, "", "", 200},
		{"null legacy id is ignored", `{` + id + `,"job_id":null,"status":"running","metadata":{}}`,
			"running", `{}`, 200},
		{"numeric legacy id is ignored", `{` + id + `,"job_id":42,"status":"running","metadata":{}}`,
			"running", `{}`, 200},
		{"wrong legacy job", `{"job_id":"other","status":"running"}`, "", "", 200},
		{"legacy missing status", `{` + legacyID + `}`, "", "", 200},
		{"legacy blank status", `{` + legacyID + `,"status":" "}`, "", "", 200},
		{"missing metadata cannot fall back", `{` + id + `,` + legacyID + `,"status":"running","model_name":"old"}`,
			"", "", 200},
		{"null metadata cannot fall back", `{` + id + `,` + legacyID + `,"status":"running","metadata":null}`,
			"", "", 200},
		{"array metadata", `{` + id + `,"status":"running","metadata":[]}`, "", "", 200},
		{"string metadata", `{` + id + `,"status":"running","metadata":"{}"}`, "", "", 200},
		{"numeric metadata", `{` + id + `,"status":"running","metadata":42}`, "", "", 200},
		{"boolean metadata", `{` + id + `,"status":"running","metadata":true}`, "", "", 200},
		{"null response", `null`, "", "", 200},
		{"array response", `[]`, "", "", 200},
		{"malformed response", `{`, "", "", 200},
		{"unregistered", `{"code":"JobNotFound"}`, "", "", 404},
		{"throttled", `{"code":"TooManyRequests"}`, "", "", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := &testTokenCredential{}
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet ||
					r.URL.Path != "/api/projects/project/rl_environments/jobs/"+realMonitorJobID ||
					r.URL.RawQuery != "api-version="+foundryAPIVersion {
					t.Errorf("unexpected status request %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") == "" ||
					r.Header.Get("azureai-project") != "" || r.Header.Get("azureai-project-is-default") != "" {
					t.Error("status must use Foundry authentication without direct fine-tuning project headers")
				}
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			rle := newRleClientWithCredential(server.URL+"/api/projects/project", credential)
			rle.httpClient = server.Client()
			source := &rleJobSource{rle: rle, jobID: realMonitorJobID}
			hasErr := tc.want == ""
			raw, err := source.Config(t.Context())
			if (err != nil) != hasErr || string(raw) != tc.config {
				t.Fatalf("got config=%s err=%v, want config=%s error=%v", raw, err, tc.config, hasErr)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("config made %d requests, want one", got)
			}
			if len(credential.scopes) != 1 || credential.scopes[0] != foundryTokenScope {
				t.Fatalf("unexpected status token scopes %v", credential.scopes)
			}
		})
	}
}

func TestRleJobRejectsInvalidRequestIdentity(t *testing.T) {
	for _, id := range []string{"", "other", realMonitorJobID + "/metrics", " " + realMonitorJobID} {
		t.Run("id="+id, func(t *testing.T) {
			rle := newRleClientWithCredential("https://example.com", &testTokenCredential{})
			rle.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid job identity must fail before a request")
				return nil, errors.New("unexpected request")
			})
			source := &rleJobSource{rle: rle, jobID: id}
			if status, err := source.Status(t.Context()); err == nil || status != "" {
				t.Fatalf("got status=%q err=%v", status, err)
			}
			if config, err := source.Config(t.Context()); err == nil || config != nil {
				t.Fatalf("got config=%s err=%v", config, err)
			}
		})
	}
}

func TestMonitorRejectsFineTuningEndpointOverride(t *testing.T) {
	command := newMonitorCommand()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--job-id", realMonitorJobID, "--endpoint", "https://facade.example.com"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --endpoint") {
		t.Fatalf("expected removed endpoint flag to be rejected, got %v", err)
	}
}

func TestRleMonitorClientRoutesAndScopes(t *testing.T) {
	credential := &testTokenCredential{}
	rle := newRleClientWithCredential("https://account.services.ai.azure.com/api/projects/project", credential)
	s := &rleJobSource{rle: rle, jobID: realMonitorJobID}
	rle.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" {
			t.Fatalf("unexpected request %s", req.URL)
		}
		if strings.Contains(req.URL.Path, "/openai/v1/fine_tuning/jobs/") {
			if req.URL.RawQuery != "" {
				t.Fatalf("fine-tuning status must not have an API version query: %s", req.URL)
			}
		} else if req.URL.Query().Get("api-version") != foundryAPIVersion {
			t.Fatalf("RLE request is missing its API version: %s", req.URL)
		}
		body := `{"id":"` + realMonitorJobID +
			`","status":"running","metadata":{"model_name":"sample","max_steps":100}}`
		switch {
		case strings.HasSuffix(req.URL.Path, "/metrics"):
			if req.URL.Query().Get("lastStep") != "7" || req.URL.Query().Get("continuationToken") != "a+/=" {
				t.Fatal("metric cursor was not preserved")
			}
			body = `{"data":[{"step":8,"custom":1}],"nextContinuationToken":"opaque"}`
		case strings.HasSuffix(req.URL.Path, "/rollouts"):
			query := req.URL.Query()
			if query.Has("lastSequence") || query.Has("after") || query.Has("createdAfter") ||
				query.Get("limit") != "100" || query.Get("continuationToken") != "t+/=" || len(query) != 3 ||
				!strings.Contains(req.URL.RawQuery, "continuationToken=t%2B%2F%3D") {
				t.Fatalf("unexpected rollout query %s", req.URL.RawQuery)
			}
			body = `{"data":[{"rollout_id":"` + monitorTestID + `","job_id":"` + realMonitorJobID +
				`","status":"completed","success":false}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	if config, err := s.Config(t.Context()); err != nil ||
		string(config) != `{"model_name":"sample","max_steps":100}` {
		t.Fatalf("dashboard config=%s err=%v", config, err)
	}
	page, err := s.Metrics(t.Context(), 7, "a+/=")
	if err != nil || page.Next != "opaque" {
		t.Fatalf("%+v %v", page, err)
	}
	entries, err := s.Rollouts(t.Context(), "t+/=")
	if err != nil || entries.Data[0].Success == nil || *entries.Data[0].Success {
		t.Fatalf("%+v %v", entries, err)
	}
	if status, err := s.Status(t.Context()); err != nil || status != "running" {
		t.Fatalf("%s %v", status, err)
	}
	for _, scope := range credential.scopes {
		if scope != foundryTokenScope {
			t.Fatal("RLE used fine-tuning auth scope")
		}
	}
}

func TestRleMonitorResultGzipAndUnavailable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		zip := gzip.NewWriter(w)
		defer zip.Close()
		_, _ = io.WriteString(zip, `{"rollout_id":"`+monitorTestID+`","reward":0.5,"rollout":{"turns":[]}}`)
	}))
	defer server.Close()
	rle := newRleClientWithCredential(server.URL, &testTokenCredential{})
	rle.httpClient = server.Client()
	s := &rleJobSource{rle: rle, jobID: realMonitorJobID}
	snapshot, err := s.Result(t.Context(), monitor.JobRollout{RolloutID: monitorTestID})
	if err != nil || !strings.Contains(string(snapshot.Response), `"reward":0.5`) {
		t.Fatalf("%+v %v", snapshot, err)
	}
}

func TestMonitorReadLimitsAndCredentialNonDisclosure(t *testing.T) {
	auth := func(context.Context) (string, error) { return "Bearer test", nil }
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"throttled", 429, `{"code":"TooManyRequests","message":"sig=secret"}`, "TooManyRequests"},
		{"malformed", 200, `{`, "decode"},
		{"oversized", 200, `{"data":"larger than cap"}`, "size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)),
					Header: http.Header{"Retry-After": {"60"}}}, nil
			})}
			var target json.RawMessage
			err := readMonitorJSON(t.Context(), client, "https://example.com", "/read?sig=secret",
				auth, nil, &target, 8)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.status == 429 {
				readErr, ok := errors.AsType[*monitor.ReadError](err)
				if !ok || readErr.RetryAfter != time.Minute {
					t.Fatal("Retry-After lost")
				}
			}
		})
	}
	var target any
	err := readMonitorJSON(t.Context(), http.DefaultClient, "https://user:password@example.com", "/read?sig=secret",
		auth, nil, &target, 100)
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credential-bearing endpoint not rejected safely: %v", err)
	}
}

func TestMonitorErrorKeepsCorrelationIDs(t *testing.T) {
	auth := func(context.Context) (string, error) { return "******", nil }
	for _, tc := range []struct {
		name, body, header, code, operation, request string
	}{
		{"nested", `{"error":{"code":"ServiceError","message":"sig=secret"},` +
			`"correlation":{"operation":"573804dcdf39af6a10d06775fc7cfc92","request":"abc-123"}}`,
			"0f4c-77aa", "ServiceError", "573804dcdf39af6a10d06775fc7cfc92", "0f4c-77aa"},
		{"unsafe", `{"error":{"code":"bad code!"},"correlation":{"operation":"x y"}}`,
			"bad\nvalue", "Internal Server Error", "", ""},
		{"body request", `{"correlation":{"request":"abc-123"}}`, "", "Internal Server Error", "", "abc-123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				header := http.Header{}
				if tc.header != "" {
					header.Set("x-ms-request-id", tc.header)
				}
				return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(tc.body)),
					Header: header}, nil
			})}
			var target any
			err := readMonitorJSON(t.Context(), client, "https://example.com", "/read", auth, nil, &target, 100)
			readErr, ok := errors.AsType[*monitor.ReadError](err)
			if !ok || readErr.Code != tc.code || readErr.Operation != tc.operation || readErr.Request != tc.request {
				t.Fatalf("unexpected error: %#v", err)
			}
			if strings.Contains(err.Error(), "secret") ||
				(tc.operation != "" && !strings.Contains(err.Error(), "operation ID "+tc.operation)) {
				t.Fatalf("unexpected message: %v", err)
			}
		})
	}
}

func TestRealMonitorFailureDoesNotResubmit(t *testing.T) {
	action, output := stubbedTrain(t, t.Context(), &rleTrainFlags{})
	stubAPIJobMonitor(t)
	calls := 0
	monitorErr := errors.New("listener unavailable")
	runAPIJobMonitor = func(context.Context, monitor.JobSource, string, bool, io.Writer, io.Writer) error {
		calls++
		return monitorErr
	}
	err := action.Run()
	if !errors.Is(err, monitorErr) || !strings.Contains(err.Error(), realMonitorJobID+" was submitted") ||
		!strings.Contains(err.Error(), "the remote job was not canceled") {
		t.Fatalf("expected a recoverable monitor failure after submission, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("job monitor started %d times, want 1", calls)
	}
	if !strings.Contains(output.String(), "azd ai rle monitor --job-id "+realMonitorJobID) {
		t.Fatalf("output = %q, want the command to reopen monitoring", output.String())
	}
}
