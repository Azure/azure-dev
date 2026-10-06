// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live shape of a create refused for a judge model the project does not
// have: HTTP 400, a message that only says the request was invalid, and the one
// place that names the model, with the part of the request it is about, in
// details[0].
const refusedJudgeModelBody = `{"error":{"code":"invalid_request","message":"The request is invalid.",
	"details":[{"code":"model_not_found",
	"message":"Model 'mh-missing-judge' was not found. Verify the name and version are correct.",
	"target":"testing_criteria[0].initialization_parameters.model"}]}}`

func refusingEvalServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, refusedJudgeModelBody)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// `eval create` reaches the service through the one client every command shares,
// so a refusal reads the same wherever it surfaces: the sentence, then what in
// the request it was about.
func TestARefusedCreateNamesTheMissingJudgeModelFromTheDetails(t *testing.T) {
	ec := evalContextFor(refusingEvalServer(t))

	_, err := ec.evalClient.CreateOpenAIEval(context.Background(), &eval_api.CreateOpenAIEvalRequest{Name: "quality"})
	require.Error(t, err)
	text := err.Error()
	assert.Contains(t, text, "The request is invalid.")
	assert.Contains(t, text, "Model 'mh-missing-judge' was not found. Verify the name and version are correct.")
	assert.Contains(t, text, "(target: testing_criteria[0].initialization_parameters.model)")
	assert.NotContains(t, text, "\n", "one line")
}

// -o json answers with the sentence the refusal always carried: the details are
// for the human line.
func TestARefusedCreateKeepsItsJSONErrorUnchanged(t *testing.T) {
	ec := evalContextFor(refusingEvalServer(t))
	_, err := ec.evalClient.CreateOpenAIEval(context.Background(), &eval_api.CreateOpenAIEvalRequest{Name: "quality"})
	require.Error(t, err)

	message := jsonMessage(err)
	assert.Equal(t, "The request is invalid. (HTTP 400 invalid_request)", message)
	assert.Equal(t, "invalid_request", errorCode(err))

	var out bytes.Buffer
	command := jsonCmd(t, "json")
	command.SetOut(&out)
	exited := 0
	prior := exitProcess
	exitProcess = func(code int) { exited = code }
	t.Cleanup(func() { exitProcess = prior })
	_ = failAs(command, err)
	assert.Equal(t, 1, exited)
	var document struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &document))
	assert.Equal(t, message, document.Error.Message)
	assert.NotContains(t, out.String(), "mh-missing-judge")
}

// The retry command a partial create prints has to run as printed: the real,
// shell-quoted path and name, never the placeholder, and the same in the JSON
// field a script reads.
func TestTheCreateRetryHintCarriesTheRealPath(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Me\My Evals\azure.eval.yaml`,
		`/home/me/my evals/azure.eval.yaml`,
		`/tmp/team evals/run (a)/azure.eval.yaml`,
	} {
		for _, format := range []string{"table", "json"} {
			t.Run(format+" "+path, func(t *testing.T) {
				var out bytes.Buffer
				command := jsonCmd(t, format)
				command.SetContext(t.Context())
				command.SetOut(&out)
				err := reportCreatePartial(command, &evalContext{}, "my eval", path,
					[]reconciledArtifact{{Kind: "dataset", Name: "golden", Version: "1", Published: true}},
					errors.New("boom"))
				require.NoError(t, err)

				text := out.String()
				assert.NotContains(t, text, "VALUE_NEEDS_QUOTING")
				if format == "json" {
					var document map[string]any
					require.NoError(t, json.Unmarshal(out.Bytes(), &document))
					text, _ = document["recovery_command"].(string)
				}
				assert.Contains(t, text, "azd ai eval create \"my eval\" --from-file ")
				assert.NotContains(t, text, "VALUE_NEEDS_QUOTING")
				assert.Contains(t, text, path)
			})
		}
	}
}
