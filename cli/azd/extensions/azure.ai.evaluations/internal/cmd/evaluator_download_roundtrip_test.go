// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluatorRoundTripService struct {
	mu        sync.Mutex
	document  json.RawMessage
	published []json.RawMessage
}

func evaluatorRoundTripContext(t *testing.T) (*evalContext, *evaluatorRoundTripService) {
	t.Helper()
	service := &evaluatorRoundTripService{document: json.RawMessage(downloadedEvaluator)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.mu.Lock()
		defer service.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		version := strconv.Itoa(3 + len(service.published))
		switch r.Method {
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/versions") {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"value": []any{map[string]string{"name": "quality", "version": version}},
				}))
			} else {
				_, err := w.Write(service.document)
				assert.NoError(t, err)
			}
		case http.MethodPost:
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, err := json.Marshal(body)
			assert.NoError(t, err)
			service.published = append(service.published, raw)
			body["version"] = json.RawMessage(strconv.Quote(strconv.Itoa(3 + len(service.published))))
			service.document, err = json.Marshal(body)
			assert.NoError(t, err)
			_, err = w.Write(service.document)
			assert.NoError(t, err)
		default:
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	env := &testEnvServer{state: map[string]string{}}
	return &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
		azdClient:  newTestAzdClient(t, env),
		envName:    "test",
	}, service
}

func (s *evaluatorRoundTripService) publishedBody(t *testing.T, count int) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.published, count)
	if count == 0 {
		return nil
	}
	return decodeBody(t, s.published[count-1])
}

func downloadEditableEvaluator(t *testing.T, ec *evalContext) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quality.json")
	action := &evaluatorDownloadAction{
		cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outFile: path,
	}
	require.NoError(t, action.download(t.Context(), ec))
	return path
}

func editDownloadedThreshold(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(raw), `"pass_threshold": 0.6`, `"pass_threshold": 0.7`, 1)
	require.NotEqual(t, string(raw), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
	return []byte(edited)
}

func TestEvaluatorDownloadRoundTripWithDeclaration(t *testing.T) {
	ec, service := evaluatorRoundTripContext(t)
	path := downloadEditableEvaluator(t, ec)
	service.publishedBody(t, 0)
	decl := declaredEvaluator()
	decl.Name, decl.Source = "quality", path
	r := &evalReconciler{ec: ec}

	version, published, err := r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.False(t, published, "downloading an unchanged rubric does not publish another version")
	require.Equal(t, "3", version)

	editDownloadedThreshold(t, path)
	version, published, err = r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.True(t, published)
	require.Equal(t, "4", version)
	body := service.publishedBody(t, 1)
	require.Equal(t, decl.DisplayName, body["display_name"])
	require.Equal(t, []any{"quality", "safety"}, body["categories"], "the declaration remains authoritative")
	require.Equal(t, []any{"turn", "conversation"}, body["supported_evaluation_levels"])
	definition, ok := body["definition"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0.7, definition["pass_threshold"])
	require.Contains(t, definition, "future_option")
	for _, key := range rubricOwnedByTheService {
		require.NotContains(t, definition, key)
	}

	ec.state = nil
	version, published, err = r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.False(t, published, "a repeat with reloaded private state must not create version 5")
	require.Equal(t, "4", version)
	service.publishedBody(t, 1)
}

func TestEvaluatorDownloadRoundTripWithStandaloneUpdate(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			ec, service := evaluatorRoundTripContext(t)
			path := downloadEditableEvaluator(t, ec)
			body, err := normalizeRubricBody("quality", editDownloadedThreshold(t, path))
			require.NoError(t, err)
			if explicit {
				var document map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &document))
				document["display_name"] = json.RawMessage(`""`)
				document["description"] = json.RawMessage(`"Edited description"`)
				document["categories"] = json.RawMessage(`[]`)
				document["supported_evaluation_levels"] = json.RawMessage(`["conversation"]`)
				body, err = json.Marshal(document)
				require.NoError(t, err)
			}
			cmd := evaluatorDownloadCmd(t)
			cmd.Flags().String("output", "json", "")
			var out strings.Builder
			cmd.SetOut(&out)
			action := &evaluatorWriteAction{cmd: cmd, name: "quality", verb: "update"}
			require.NoError(t, action.write(t.Context(), ec, body))

			published := service.publishedBody(t, 1)
			if explicit {
				require.Equal(t, "", published["display_name"])
				require.Equal(t, "Edited description", published["description"])
				require.Empty(t, published["categories"])
				require.Equal(t, []any{"conversation"}, published["supported_evaluation_levels"])
			} else {
				require.Equal(t, "Support quality", published["display_name"])
				require.Equal(t, "Grades support conversations", published["description"])
				require.Equal(t, []any{"quality", "agents"}, published["categories"])
				require.Equal(t, []any{"turn", "conversation"}, published["supported_evaluation_levels"])
			}
			require.NotContains(t, published, "version")
			require.NotContains(t, published, "created_at")
			definition, ok := published["definition"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, 0.7, definition["pass_threshold"])
			for _, key := range rubricOwnedByTheService {
				require.NotContains(t, definition, key)
			}
			require.True(t, json.Valid([]byte(out.String())), "update stdout remains one JSON document")
		})
	}
}

func TestEvaluatorUpdateRefusesUnreadableMetadata(t *testing.T) {
	for _, document := range []string{"null", "[]", "not JSON"} {
		t.Run(document, func(t *testing.T) {
			ec, service := evaluatorRoundTripContext(t)
			service.document = json.RawMessage(document)
			body, err := normalizeRubricBody("quality", []byte(authoredRubric))
			require.NoError(t, err)
			action := &evaluatorWriteAction{cmd: evaluatorDownloadCmd(t), name: "quality", verb: "update"}
			require.Error(t, action.write(t.Context(), ec, body))
			service.publishedBody(t, 0)
		})
	}
}

func TestEvaluatorUpdateCancellationDoesNotPublish(t *testing.T) {
	ec, service := evaluatorRoundTripContext(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	action := &evaluatorWriteAction{cmd: evaluatorDownloadCmd(t), name: "quality", verb: "update"}
	err := action.write(ctx, ec, json.RawMessage(`{"name":"quality","definition":{"type":"rubric"}}`))
	require.ErrorIs(t, err, context.Canceled)
	service.publishedBody(t, 0)
}
