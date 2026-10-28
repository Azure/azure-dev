// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordedAnalyzerID = "cu_cli_test_v1"

var recordedLifecycleCases = []struct {
	name                         string
	createStart, createEnd, show int
	polls                        time.Duration
}{
	{"basic", 0, 4, 0, time.Second},
	{"recorded-template", 4, 11, 1, 4 * time.Second},
}

type recordedAnalyzerStep struct {
	action  string
	fixture recordedFixture
	args    []string
	result  json.RawMessage
	wait    time.Duration
}

func executeRecordedAnalyzerStep(
	t *testing.T, step recordedAnalyzerStep, analyzer string, definition json.RawMessage, format string,
) {
	t.Helper()
	transport := &recordedTransport{fixture: step.fixture, jsonBody: definition}
	root := newRecordedCommand(t, transport, "")
	args := []string{"analyzer", step.action, analyzer}
	args = append(args, step.args...)
	args = append(args, "-o", format, "--debug", "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	require.NoError(t, ValidateInvocation(root, args))
	root.SetArgs(args)
	start := time.Now()
	require.NoError(t, root.ExecuteContext(t.Context()), step.action)
	require.NoError(t, transport.complete(), step.action)
	assert.Equal(t, step.wait, time.Since(start), step.action)
	assert.Empty(t, stderr.String(), step.action)
	if format == "none" {
		assert.Empty(t, stdout.String(), step.action)
	} else {
		wanted, err := recordedJSON(step.result)
		require.NoError(t, err)
		actual, err := recordedJSON(stdout.Bytes())
		require.NoError(t, err)
		assert.Equal(t, wanted, actual, "compare complete "+step.action+" output")
	}
}

func recordedLifecycleDefinition(t *testing.T, fixture recordedFixture, basic bool) json.RawMessage {
	t.Helper()
	if basic {
		return json.RawMessage(`{
			"baseAnalyzerId":"prebuilt-document","models":{"completion":"gpt-4.1"},
			"fieldSchema":{"fields":{"vendor_name":{"type":"string","method":"extract",
			"description":"Full legal vendor name from the invoice header."}}}
		}`)
	}
	// The source PUT JSON is scrubbed; these public definition fields are a synthetic expectation.
	var ready map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fixture.Interactions[len(fixture.Interactions)-1].Response.Body, &ready))
	definition := map[string]json.RawMessage{}
	for _, key := range []string{"baseAnalyzerId", "config", "description", "fieldSchema", "models"} {
		require.Contains(t, ready, key)
		definition[key] = ready[key]
	}
	body, err := json.Marshal(definition)
	require.NoError(t, err)
	return body
}

func TestRecordedAnalyzerLifecycle(t *testing.T) {
	for _, test := range recordedLifecycleCases {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						create := loadRecordedFixture(t, "analyzer_create")
						require.Len(t, create.Interactions, 11)
						require.Empty(t, create.Source.UnusedInteractions)
						finalRead := create
						finalRead.Interactions = create.Interactions[test.createEnd-1 : test.createEnd]
						create.Interactions = create.Interactions[test.createStart : test.createEnd-1]
						require.Equal(t, test.createStart, create.Interactions[0].SourceIndex)
						require.Equal(t, test.createEnd-2, create.Interactions[len(create.Interactions)-1].SourceIndex)
						var terminal struct {
							Result json.RawMessage `json:"result"`
						}
						require.NoError(t, json.Unmarshal(
							create.Interactions[len(create.Interactions)-1].Response.Body, &terminal))
						embedded, err := recordedJSON(terminal.Result)
						require.NoError(t, err)
						ready, err := recordedJSON(finalRead.Interactions[0].Response.Body)
						require.NoError(t, err)
						require.Equal(t, ready, embedded, "the recorded final GET repeats the embedded ready analyzer")
						show := loadRecordedFixture(t, "analyzer_show")
						require.Len(t, show.Interactions, 2)
						show.Interactions = show.Interactions[test.show : test.show+1]
						deletion := loadRecordedFixture(t, "analyzer_delete")
						require.Len(t, deletion.Interactions, 2, "GET preflight precedes DELETE")
						require.Equal(t, 3, deletion.Source.InteractionCount)
						expected := recordedLifecycleDefinition(t, finalRead, test.name == "basic")
						var schema map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(expected, &schema))
						schema["analyzerId"] = json.RawMessage(`"` + recordedAnalyzerID + `"`)
						schema["apiVersion"] = json.RawMessage(`"` + cuclient.DefaultAPIVersion + `"`)
						schemaJSON, err := json.Marshal(schema)
						require.NoError(t, err)
						schemaFile := filepath.Join(t.TempDir(), "schema.json")
						require.NoError(t, os.WriteFile(schemaFile, schemaJSON, 0o600))
						for _, step := range []recordedAnalyzerStep{
							{"create", create, []string{"--schema", schemaFile},
								finalRead.Interactions[0].Response.Body, test.polls},
							{"show", finalRead, nil, finalRead.Interactions[0].Response.Body, 0},
							{"show", show, nil, show.Interactions[0].Response.Body, 0},
							{"delete", deletion, []string{"--yes"},
								json.RawMessage(`{"deleted":true,"analyzerId":"cu_cli_test_v1"}`), 0},
						} {
							executeRecordedAnalyzerStep(t, step, recordedAnalyzerID, schemaJSON, format)
						}
					})
				})
			}
		})
	}
}

func TestRecordedAnalyzerCreateSyntheticFailures(t *testing.T) {
	for _, name := range []string{
		"missing-json-expectation", "different-json", "replacement", "implicit-replacement",
		"cross-origin", "wrong-operation", "missing-final-get", "wrong-analyzer", "not-ready", "write-not-retried",
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := loadRecordedFixture(t, "analyzer_create")
				fixture.Interactions = fixture.Interactions[:4]
				definition := recordedLifecycleDefinition(t, fixture, true)
				transport := &recordedTransport{fixture: fixture, jsonBody: definition}
				first := &transport.fixture.Interactions[0]
				switch name {
				case "missing-json-expectation":
					transport.jsonBody = nil
				case "different-json":
					transport.jsonBody = json.RawMessage(`{"baseAnalyzerId":"prebuilt-document"}`)
				case "replacement":
					first.Request.URL = strings.Replace(first.Request.URL, "allowReplace=false", "allowReplace=true", 1)
				case "implicit-replacement":
					first.Request.URL = strings.Replace(first.Request.URL, "&allowReplace=false", "", 1)
				case "cross-origin":
					first.Response.Headers["Operation-Location"] = strings.Replace(
						first.Response.Headers["Operation-Location"], "sanitized.services.ai.azure.com", "example.com", 1)
				case "wrong-operation":
					first.Response.Headers["Operation-Location"] = strings.Replace(
						first.Response.Headers["Operation-Location"], "/operations/", "/operations/other-", 1)
				case "wrong-analyzer", "not-ready":
					var ready map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(transport.fixture.Interactions[3].Response.Body, &ready))
					if name == "wrong-analyzer" {
						ready["analyzerId"] = json.RawMessage(`"another_analyzer"`)
					} else {
						ready["status"] = json.RawMessage(`"creating"`)
					}
					body, err := json.Marshal(ready)
					require.NoError(t, err)
					transport.fixture.Interactions[3].Response.Body = body
				case "write-not-retried":
					first.Response.Status = http.StatusInternalServerError
					first.Response.Body = json.RawMessage(`{
						"error":{"code":"InternalServerError","message":"Synthetic failure."}
					}`)
				}
				if name == "missing-final-get" || name == "wrong-analyzer" || name == "not-ready" {
					var terminal map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(transport.fixture.Interactions[2].Response.Body, &terminal))
					terminal["result"] = json.RawMessage("null")
					body, err := json.Marshal(terminal)
					require.NoError(t, err)
					transport.fixture.Interactions[2].Response.Body = body
					if name == "missing-final-get" {
						transport.fixture.Interactions = transport.fixture.Interactions[:3]
					}
				}
				client, err := cuclient.NewRESTTransport("https://sanitized.services.ai.azure.com",
					&recordedCredential{}, &cuclient.TransportOptions{Transport: transport})
				require.NoError(t, err)
				_, err = client.CreateAnalyzer(t.Context(), cuclient.CreateAnalyzerRequest{
					AnalyzerID: recordedAnalyzerID, Definition: definition,
				})
				require.Error(t, err, "mutated recordings are synthetic failure contracts")
				if name == "wrong-analyzer" || name == "not-ready" {
					require.NoError(t, transport.complete())
				} else {
					require.Error(t, transport.complete())
				}
				if name == "write-not-retried" {
					assert.Equal(t, 1, transport.next)
					assert.Nil(t, transport.failure, "no second submission should even reach request matching")
				}
			})
		})
	}
}

func TestRecordedAnalyzerSyntheticFinalGetFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := loadRecordedFixture(t, "analyzer_create")
		fixture.Interactions = fixture.Interactions[:4]
		var terminal map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fixture.Interactions[2].Response.Body, &terminal))
		terminal["result"] = json.RawMessage("null")
		body, err := json.Marshal(terminal)
		require.NoError(t, err)
		fixture.Interactions[2].Response.Body = body
		definition := recordedLifecycleDefinition(t, fixture, true)
		transport := &recordedTransport{fixture: fixture, jsonBody: definition}
		client, err := cuclient.NewRESTTransport("https://sanitized.services.ai.azure.com",
			&recordedCredential{}, &cuclient.TransportOptions{Transport: transport})
		require.NoError(t, err)
		result, err := client.CreateAnalyzer(t.Context(), cuclient.CreateAnalyzerRequest{
			AnalyzerID: recordedAnalyzerID, Definition: definition,
		})
		require.NoError(t, err)
		require.NoError(t, transport.complete())
		wanted, err := recordedJSON(fixture.Interactions[3].Response.Body)
		require.NoError(t, err)
		actual, err := recordedJSON(result.Analyzer.JSON)
		require.NoError(t, err)
		assert.Equal(t, wanted, actual, "synthetic missing-result envelopes require the recorded resource GET")
	})
}

func TestRecordedDeleteHasEmptyNoContentResponse(t *testing.T) {
	for _, body := range []string{"null", "{}"} {
		t.Run(body, func(t *testing.T) {
			fixture := loadRecordedFixture(t, "analyzer_delete")
			fixture.Interactions = fixture.Interactions[1:]
			fixture.Interactions[0].Response.Body = json.RawMessage(body)
			transport := &recordedTransport{fixture: fixture}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete,
				fixture.Interactions[0].Request.URL, nil)
			require.NoError(t, err)
			request.Header.Set("Accept", "application/json")
			request.Header.Set("Authorization", "Bearer playback-token")
			response, err := transport.Do(request)
			if body != "null" {
				require.Error(t, err)
				require.Nil(t, response)
				require.Error(t, transport.complete())
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, response.StatusCode)
			actual, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			assert.Empty(t, actual, "fixture null represents an empty HTTP body, not JSON null bytes")
			require.NoError(t, transport.complete())
		})
	}
}
