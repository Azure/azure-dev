// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLoomOperationEnvelopeAcceptsStringAndObjectErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		message string
		code    string
	}{
		{
			name:    "string",
			payload: `{"status":"failed","error":"The model allocation is unavailable."}`,
			message: "The model allocation is unavailable.",
		},
		{
			name:    "object",
			payload: `{"status":"failed","error":{"code":"CapacityUnavailable","message":"Try another model."}}`,
			message: "Try another model.",
			code:    "CapacityUnavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var envelope loomOperationEnvelope
			if err := json.Unmarshal([]byte(tc.payload), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil ||
				envelope.Error.Message != tc.message ||
				envelope.Error.Code != tc.code {
				t.Fatalf("unexpected Loom error: %#v", envelope.Error)
			}
		})
	}
}

func TestLoomCreateSessionCleanup(t *testing.T) {
	pollErr := errors.New("poll connection failed")
	for _, tc := range []struct {
		name          string
		createBody    string
		createStatus  int
		pollBody      string
		pollStatus    int
		pollErr       error
		cancelPoll    bool
		timeout       time.Duration
		cleanupStatus int
		wantError     string
		wantCause     error
		wantPoll      bool
		wantCleanup   bool
	}{
		{
			name: "success", pollBody: `{"status":"completed"}`, wantPoll: true,
		},
		{
			name: "create failure", createStatus: http.StatusServiceUnavailable, wantError: "HTTP 503",
		},
		{
			name: "missing session ID", createBody: `{"request_id":"request_test"}`,
			wantError: "session_id and request_id",
		},
		{
			name: "missing request ID", createBody: `{"session_id":"model_test"}`,
			wantError: "session_id and request_id", wantCleanup: true,
		},
		{
			name: "invalid request ID", createBody: `{"session_id":"model_test","request_id":1}`,
			wantError: "decode Loom", wantCleanup: true,
		},
		{
			name: "poll HTTP failure", pollStatus: http.StatusServiceUnavailable, wantError: "HTTP 503",
			wantPoll: true, wantCleanup: true,
		},
		{
			name: "failed operation", pollBody: `{"status":"failed","error":"allocation failed"}`,
			wantError: "allocation failed", wantPoll: true, wantCleanup: true,
		},
		{
			name: "poll transport failure", pollErr: pollErr, wantCause: pollErr,
			wantError: pollErr.Error(), wantPoll: true, wantCleanup: true,
		},
		{
			name: "poll timeout", pollBody: `{"status":"pending"}`, timeout: -time.Second,
			wantError: "did not complete within", wantPoll: true, wantCleanup: true,
		},
		{
			name: "poll cancellation", cancelPoll: true, timeout: time.Minute,
			wantError: "context canceled", wantCause: context.Canceled, wantPoll: true, wantCleanup: true,
		},
		{
			name: "cleanup failure", pollStatus: http.StatusServiceUnavailable,
			cleanupStatus: http.StatusInternalServerError, wantError: "HTTP 503",
			wantPoll: true, wantCleanup: true,
		},
		{
			name: "canceled cleanup failure", cancelPoll: true, timeout: time.Minute,
			cleanupStatus: http.StatusInternalServerError,
			wantError:     "context canceled", wantCause: context.Canceled, wantPoll: true, wantCleanup: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := newLoomSessionClientWithCredential(
				"https://account.services.ai.azure.com/api/projects/project", &testTokenCredential{})
			polls, cleanups := 0, 0
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sessions"):
					body := tc.createBody
					if body == "" {
						body = `{"session_id":"model_test","request_id":"request_test"}`
					}
					status := tc.createStatus
					if status == 0 {
						status = http.StatusOK
					}
					return harborTestResponse(status, body), nil
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/model_test/request/request_test"):
					polls++
					if tc.cancelPoll {
						cancel()
						return nil, r.Context().Err()
					}
					if tc.pollErr != nil {
						return nil, tc.pollErr
					}
					status := tc.pollStatus
					if status == 0 {
						status = http.StatusOK
					}
					return harborTestResponse(status, tc.pollBody), nil
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/model_test/complete"):
					cleanups++
					if err := r.Context().Err(); err != nil {
						t.Fatalf("cleanup inherited canceled context: %v", err)
					}
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > loomSessionCleanupTimeout {
						t.Fatal("cleanup must have a bounded, independent timeout")
					}
					status := tc.cleanupStatus
					if status == 0 {
						status = http.StatusNoContent
					}
					return harborTestResponse(status, ""), nil
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
					return nil, errors.New("unexpected request")
				}
			})

			sessionID, err := client.createSession(ctx, "test-model", 0, tc.timeout)
			if tc.wantError == "" {
				if err != nil || sessionID != "session_test" {
					t.Fatalf("expected successful session, got %q, %v", sessionID, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || sessionID != "" {
					t.Fatalf("expected %q and no session, got %q, %v", tc.wantError, sessionID, err)
				}
				reported := loomServiceErrorFor("create Loom training session", err)
				if tc.wantCause != nil && !errors.Is(reported, tc.wantCause) {
					t.Fatalf("expected original cause %v, got %v", tc.wantCause, reported)
				}
				if tc.cleanupStatus != 0 {
					for _, message := range []string{tc.wantError, "session_test", "may remain allocated", "HTTP 500"} {
						if !strings.Contains(reported.Error(), message) {
							t.Fatalf("expected cleanup diagnostic %q, got %v", message, reported)
						}
					}
				}
			}
			if (polls == 1) != tc.wantPoll || polls > 1 {
				t.Fatalf("poll requests = %d, want poll = %v", polls, tc.wantPoll)
			}
			if (cleanups == 1) != tc.wantCleanup || cleanups > 1 {
				t.Fatalf("cleanup requests = %d, want cleanup = %v", cleanups, tc.wantCleanup)
			}
		})
	}
}
