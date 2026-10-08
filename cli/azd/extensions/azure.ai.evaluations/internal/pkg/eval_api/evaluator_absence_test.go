// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorAbsenceRequiresValidCompleteEmptyVersions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		absent bool
	}{
		{"empty", http.StatusOK, `{"value":[]}`, true},
		{"not found", http.StatusNotFound, `{"error":{"code":"NotFound"}}`, true},
		{"unauthorized", http.StatusUnauthorized, "", false},
		{"forbidden", http.StatusForbidden, "", false},
		{"throttled", http.StatusTooManyRequests, "", false},
		{"server failure", http.StatusServiceUnavailable, "", false},
		{"empty body", http.StatusOK, "", false},
		{"malformed", http.StatusOK, `not JSON`, false},
		{"null", http.StatusOK, `null`, false},
		{"missing list", http.StatusOK, `{}`, false},
		{"null list", http.StatusOK, `{"value":null}`, false},
		{"wrong list type", http.StatusOK, `{"value":{}}`, false},
		{"missing version", http.StatusOK, `{"value":[{"name":"e"}]}`, false},
		{"blank version", http.StatusOK, `{"value":[{"name":"e","version":" "}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := recorder(t, tc.status, tc.body)
			_, err := client.LatestEvaluatorVersion(t.Context(), "e", "v1")
			require.Error(t, err)
			assert.Equal(t, tc.absent, IsEvaluatorAbsent(fmt.Errorf("wrapped: %w", err)))
			assert.Equal(t, tc.status == http.StatusNotFound, IsNotFound(err),
				"an empty successful list is not an HTTP 404")
		})
	}
}

func TestEvaluatorAbsenceWaitsForEveryVersionPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		version string
		absent  bool
	}{
		{"later version", http.StatusOK, `{"value":[{"name":"e","version":"7"}]}`, "7", false},
		{"complete empty", http.StatusOK, `{"value":[]}`, "", true},
		{"later not found", http.StatusNotFound, "", "", false},
		{"later forbidden", http.StatusForbidden, "", "", false},
		{"later failure", http.StatusServiceUnavailable, "", "", false},
		{"later empty body", http.StatusOK, "", "", false},
		{"later missing list", http.StatusOK, `{}`, "", false},
		{"later missing version", http.StatusOK, `{"value":[{}]}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			client, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
				reads++
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("page") == "" {
					_, err := w.Write([]byte(`{"value":[],"nextLink":"/evaluators/e/versions?page=2"}`))
					assert.NoError(t, err)
					return
				}
				w.WriteHeader(tc.status)
				_, err := w.Write([]byte(tc.body))
				assert.NoError(t, err)
			})
			version, err := client.LatestEvaluatorVersion(t.Context(), "e", "v1")
			assert.Equal(t, 2, reads)
			assert.Equal(t, tc.version, version)
			if tc.version != "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.absent, IsEvaluatorAbsent(err))
			}
		})
	}
}

type evaluatorVersionTransportFailure struct{}

func (evaluatorVersionTransportFailure) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport unavailable")
}

func TestEvaluatorTransportFailureAndCancellationAreNotAbsence(t *testing.T) {
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
		Transport: evaluatorVersionTransportFailure{}, Retry: policy.RetryOptions{MaxRetries: -1},
	})
	client := NewEvalClientFromPipeline("https://example.invalid", pipeline)
	_, err := client.LatestEvaluatorVersion(t.Context(), "e", "v1")
	require.ErrorContains(t, err, "transport unavailable")
	assert.False(t, IsEvaluatorAbsent(err))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.LatestEvaluatorVersion(ctx, "e", "v1")
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, IsEvaluatorAbsent(err))
}
