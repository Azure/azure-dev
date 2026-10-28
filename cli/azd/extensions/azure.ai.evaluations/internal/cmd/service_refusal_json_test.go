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

// A named run that does not exist under a verified evaluation is still a
// service refusal, and ReadingRun wraps it rather than replacing it the way
// jobLookupError's not-found branch does. ADO 5572140: the wrapped message
// used to carry the full internal service endpoint into -o json. Existing
// human diagnostics still name which service answered; only the JSON
// projection drops it.
func TestMissingRunKeepsContextButOmitsTheEndpointFromJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-error-code", "ResourceNotFound")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"ResourceNotFound","message":"run 'run_missing' was not found"}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	cmd := jsonCmd(t, "table")
	cmd.SetContext(t.Context())

	_, _, err := ec.latestOrNamedRun(cmd, "eval_verified", "run_missing", true)
	require.Error(t, err)

	full := err.Error()
	assert.Contains(t, full, "reading run run_missing")
	assert.Contains(t, full, "run 'run_missing' was not found")
	assert.Contains(t, full, "127.0.0.1",
		"existing human diagnostics still name which service answered")

	safe := jsonMessage(err)
	assert.Contains(t, safe, "reading run run_missing",
		"the outer context ReadingRun added must survive")
	assert.Contains(t, safe, "run 'run_missing' was not found",
		"the service's own sentence is still useful and is not what leaked")
	assert.NotContains(t, safe, "127.0.0.1",
		"the JSON document must not disclose the internal service endpoint")

	assert.Equal(t, "ResourceNotFound", errorCode(err),
		"a JSON consumer needs the service's own code when one was supplied")
}

// A terminal generation-job delete conflict goes through jobLookupError's
// other branch, JobActionFailed, which also wraps rather than replaces. ADO
// 5572140. No x-ms-error-code header here, covering the stable http_<status>
// fallback when the service supplies no code of its own.
func TestJobDeleteConflictKeepsContextButOmitsTheEndpointFromJSON(t *testing.T) {
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

	full := wrapped.Error()
	assert.Contains(t, full, "deleting dataset generation job job_1")
	assert.Contains(t, full, "job still running")
	assert.Contains(t, full, "127.0.0.1",
		"existing human diagnostics still name which service answered")

	safe := jsonMessage(wrapped)
	assert.Contains(t, safe, "deleting dataset generation job job_1")
	assert.Contains(t, safe, "job still running")
	assert.NotContains(t, safe, "127.0.0.1",
		"the JSON document must not disclose the internal service endpoint")

	code := errorCode(wrapped)
	assert.Equal(t, "http_409", code,
		"a stable code must be reported even when the service supplies none")

	// The full -o json failure contract: exactly one document, nonzero exit,
	// the same safe message and code, and no endpoint anywhere in the stream.
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	priorExit := exitProcess
	exitCode := 0
	exitProcess = func(c int) { exitCode = c }
	t.Cleanup(func() { exitProcess = priorExit })

	require.ErrorIs(t, failAs(cmd, wrapped), wrapped)
	assert.Equal(t, 1, exitCode)

	var doc jsonError
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	require.NoError(t, decoder.Decode(&doc))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "exactly one JSON document")
	assert.Equal(t, safe, doc.Error.Message)
	assert.Equal(t, code, doc.Error.Code)
	assert.NotContains(t, out.String(), "127.0.0.1")
}

// A 401 or 403 is reclassified into an auth LocalError with its own
// suggestion, which used to flatten the concise cause's safe/code interface:
// -o json still disclosed the full endpoint for an authorization failure even
// after the other statuses were fixed. The auth classification, suggestion,
// and full human/stderr diagnostic must all survive alongside the fix.
func TestAuthRefusalJSONOmitsTheEndpointButKeepsItsClassification(t *testing.T) {
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

			full := err.Error()
			assert.Contains(t, full, "127.0.0.1")
			assert.Contains(t, full, "caller lacks access")
			assert.Contains(t, full, "azd auth login")

			safe := jsonMessage(err)
			assert.NotContains(t, safe, "127.0.0.1",
				"the JSON document must not disclose the internal service endpoint")
			assert.Contains(t, safe, "caller lacks access")
			assert.Contains(t, safe, "azd auth login")

			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "the auth classification must still be reachable")
			assert.Equal(t, exterrors.CodeAuthFailed, local.Code)
			assert.Equal(t, "run `azd auth login`, and check you have access to this project", local.Suggestion)
			assert.Equal(t, exterrors.CodeAuthFailed, errorCode(err))

			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			cmd.SetOut(&out)
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(c int) { exitCode = c }
			t.Cleanup(func() { exitProcess = priorExit })

			require.ErrorIs(t, failAs(cmd, err), err)
			assert.Equal(t, 1, exitCode)

			var doc jsonError
			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			require.NoError(t, decoder.Decode(&doc))
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "exactly one JSON document")
			assert.Equal(t, safe, doc.Error.Message)
			assert.Equal(t, exterrors.CodeAuthFailed, doc.Error.Code)
			assert.Equal(t, "run `azd auth login`, and check you have access to this project", doc.Error.Suggestion)
			assert.NotContains(t, out.String(), "127.0.0.1")
		})
	}
}

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

func TestMissingRunResourceEnvelopeIsReduced(t *testing.T) {
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
