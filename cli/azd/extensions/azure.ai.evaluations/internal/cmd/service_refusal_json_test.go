// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaieval/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingRunProducesConciseHumanAndStableJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-error-code", "ResourceNotFound")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"ResourceNotFound","message":`+
			`"Resource {\"error\":{\"code\":\"ResourceNotFound\",`+
			`\"message\":\"run 'run_missing' was not found\"},`+
			`\"endpoint\":\"https://fixture-user:fixture-password@internal.example/`+
			`runs/run_missing?sig=fixture-signature#fixture-fragment\"}"}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	cmd := jsonCmd(t, "table")
	cmd.SetContext(t.Context())

	_, _, err := ec.latestOrNamedRun(cmd, "eval_verified", "run_missing", true)
	require.Error(t, err)

	human := err.Error()
	assert.Contains(t, human, "reading run run_missing")
	assert.Contains(t, human, "run 'run_missing' was not found")
	assert.Contains(t, human, "127.0.0.1")
	assert.NotContains(t, human, "Resource {")
	assert.NotContains(t, human, `"error"`)
	assertNoEndpointSecrets(t, human)

	wire := azdext.WrapError(err)
	assert.Contains(t, wire.GetMessage(), "run 'run_missing' was not found")
	assert.NotContains(t, wire.GetMessage(), "Resource {")
	assertNoEndpointSecrets(t, wire.GetMessage())
	service := wire.GetServiceError()
	require.NotNil(t, service)
	assert.Equal(t, "ResourceNotFound", service.GetErrorCode())
	assert.Equal(t, int32(http.StatusNotFound), service.GetStatusCode())

	safe := jsonMessage(err)
	assert.Contains(t, safe, "reading run run_missing")
	assert.Contains(t, safe, "run 'run_missing' was not found")
	assert.NotContains(t, safe, "127.0.0.1")
	assert.NotContains(t, safe, "Resource {")
	assertNoEndpointSecrets(t, safe)
	assert.Equal(t, "ResourceNotFound", errorCode(err))

	cmd = jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	priorExit := exitProcess
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	t.Cleanup(func() { exitProcess = priorExit })

	require.ErrorIs(t, failAs(cmd, err), err)
	assert.Equal(t, 1, exitCode)

	var document jsonError
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	require.NoError(t, decoder.Decode(&document))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "exactly one JSON document")
	assert.Equal(t, safe, document.Error.Message)
	assert.Equal(t, "ResourceNotFound", document.Error.Code)
	assert.NotContains(t, out.String(), "127.0.0.1")
	assert.NotContains(t, out.String(), "Resource {")
	assertNoEndpointSecrets(t, out.String())
}

func TestBuild47MissingRunResourceEnvelopeIsReduced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-error-code", "UserError")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":`+
			`"Resource {\n  \"error\": {\n    \"message\": `+
			`\"Eval eval_verified cannot be found. Please confirm the eval_id or permissions to view it.\",`+
			`\n    \"type\": \"invalid_request_error\",\n    \"param\": null,\n    \"code\": null`+
			`\n  }\n} not found (HTTP 404 UserError)","code":"UserError"}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())

	_, _, err := ec.latestOrNamedRun(cmd, "eval_verified", "run_missing", true)
	require.Error(t, err)

	assert.Contains(t, err.Error(),
		"Eval eval_verified cannot be found. Please confirm the eval_id or permissions to view it.")
	assert.Contains(t, err.Error(), "HTTP 404 UserError")
	assert.NotContains(t, err.Error(), "Resource {")
	assert.NotContains(t, err.Error(), `"error"`)
	assert.Equal(t, "UserError", errorCode(err))

	var out bytes.Buffer
	cmd.SetOut(&out)
	priorExit := exitProcess
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	t.Cleanup(func() { exitProcess = priorExit })

	require.ErrorIs(t, failAs(cmd, err), err)
	assert.Equal(t, 1, exitCode)

	var document jsonError
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	require.NoError(t, decoder.Decode(&document))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "exactly one JSON document")
	assert.Equal(t, "UserError", document.Error.Code)
	assert.Contains(t, document.Error.Message, "reading run run_missing")
	assert.Contains(t, document.Error.Message,
		"Eval eval_verified cannot be found. Please confirm the eval_id or permissions to view it.")
	assert.NotContains(t, document.Error.Message, "Resource {")
	assert.NotContains(t, document.Error.Message, `"error"`)
}

func TestServiceConflictWithoutCodeUsesStableHTTPCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"innererror":{"message":"job still running"}}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)

	removeErr := datasetJobs.remove(t.Context(), ec, "job_1")
	wrapped := jobLookupError("deleting", datasetJobs, "job_1", removeErr)
	require.Error(t, wrapped)

	assert.Contains(t, wrapped.Error(), "deleting dataset generation job job_1")
	assert.Contains(t, wrapped.Error(), "job still running")
	assert.Contains(t, wrapped.Error(), "127.0.0.1")
	assert.NotContains(t, jsonMessage(wrapped), "127.0.0.1")
	assert.Equal(t, "http_409", errorCode(wrapped))
}

func TestAuthRefusalJSONKeepsClassificationWithoutEndpoint(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"caller lacks access"}}`)
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)

			_, err := ec.evalClient.GetOpenAIEval(t.Context(), "eval_verified")
			require.Error(t, err)

			assert.Contains(t, err.Error(), "127.0.0.1")
			assert.Contains(t, err.Error(), "caller lacks access")
			assert.NotContains(t, jsonMessage(err), "127.0.0.1")

			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeAuthFailed, local.Code)
			assert.Equal(t, exterrors.CodeAuthFailed, errorCode(err))
			assert.Equal(t,
				"run `azd auth login`, and check you have access to this project",
				local.Suggestion)
		})
	}
}

func assertNoEndpointSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{
		"fixture-user",
		"fixture-password",
		"fixture-signature",
		"fixture-fragment",
		"sig=",
	} {
		assert.NotContains(t, text, secret)
	}
}
