// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type generationStateObserver struct {
	testEnvServer
	mu         sync.Mutex
	once       sync.Once
	recorded   chan struct{}
	key        string
	value      string
	writeCount int
}

func (s *generationStateObserver) GetConfig(
	ctx context.Context, req *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testEnvServer.GetConfig(ctx, req)
}

func (s *generationStateObserver) SetConfig(
	ctx context.Context, req *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response, err := s.testEnvServer.SetConfig(ctx, req)
	if err != nil {
		return response, err
	}
	s.writeCount++
	var state map[string]string
	if err := json.Unmarshal(req.Value, &state); err != nil {
		return nil, err
	}
	if state[s.key] == s.value {
		s.once.Do(func() { close(s.recorded) })
	}
	return response, nil
}

func TestRunGenerationsHasOnePrivateStateOwner(t *testing.T) {
	for _, tc := range []struct {
		name         string
		noWait       bool
		datasetFails bool
	}{
		{name: "submit without waiting", noWait: true},
		{name: "collect both artifacts"},
		{name: "retain job state after a failed generation", datasetFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			dir := t.TempDir()
			flags := generateFlags{
				path: dir, target: "agent", model: "generator", instruction: "A local test task.", noWait: tc.noWait,
			}
			var output bytes.Buffer
			cmd := catalogCommand(t, &output)
			cmd.SetContext(ctx)
			require.NoError(t, cmd.Flags().Set("output", "json"))
			plans, err := buildGeneratePlans(generateRequest{
				flags: &flags, cmd: cmd, target: "agent", dataset: true, evaluator: true,
				datasetName: "seeds", evaluatorName: "rubric", evaluationLevel: "conversation",
				from: []string{"prompt"},
			})
			require.NoError(t, err)
			require.Len(t, plans, 2)
			require.Equal(t, generateKindDataset, plans[0].Kind)
			require.Equal(t, generateKindEvaluator, plans[1].Kind)

			levelKey := generationLevelKey("data-job")
			env := &generationStateObserver{
				testEnvServer: testEnvServer{state: map[string]string{"unrelated": "preserved"}},
				recorded:      make(chan struct{}), key: levelKey, value: "conversation",
			}
			if !tc.noWait && !tc.datasetFails {
				env.key, env.value = versionKey("dataset", "seeds"), "1"
			}
			started := make(chan string, 2)
			release := make(chan struct{})
			const rows = "{\"test_case_description\":\"A local fixture.\"}\n"
			var service *httptest.Server
			service = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/data_generation_jobs", "/evaluator_generation_jobs":
					assert.Equal(t, http.MethodPost, r.Method)
					started <- r.URL.Path
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					id := "data-job"
					if r.URL.Path == "/evaluator_generation_jobs" {
						// Keep the rubric worker in flight while all dataset state writes happen.
						select {
						case <-env.recorded:
						case <-r.Context().Done():
							return
						}
						id = "rubric-job"
					}
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "running"}))
				case "/data_generation_jobs/data-job":
					if tc.datasetFails {
						_, _ = w.Write([]byte(`{"id":"data-job","status":"failed","error":{"message":"fixture failure"}}`))
						return
					}
					_, _ = w.Write([]byte(`{"id":"data-job","status":"succeeded","result":{"name":"seeds","version":"1"}}`))
				case "/evaluator_generation_jobs/rubric-job":
					_, _ = w.Write([]byte(`{"id":"rubric-job","status":"succeeded","result":` +
						`{"name":"rubric","version":"1","definition":{"dimensions":[]}}}`))
				case "/datasets/seeds/versions/1":
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
						"name": "seeds", "version": "1", "tags": seedDatasetTags("conversation"),
					}))
				case "/datasets/seeds/versions/1/credentials":
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"sas_uri": service.URL + "/rows.jsonl"}))
				case "/rows.jsonl":
					_, _ = w.Write([]byte(rows))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(service.Close)
			ec := evalContextFor(service)
			ec.endpoint = service.URL
			env.state[stateEndpointKey] = normalizedEndpoint(service.URL)
			ec.azdClient = newTestAzdClient(t, env)
			ec.envName = "test"

			finished := make(chan struct{})
			var runErr error
			go func() {
				defer close(finished)
				runErr = ec.runGenerations(cmd, plans, flags)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("generation workers did not stop after cancellation")
				}
			})
			var submitted []string
			for range len(plans) {
				select {
				case name := <-started:
					submitted = append(submitted, name)
				case <-ctx.Done():
					t.Fatal("both generation submissions must overlap")
				}
			}
			require.ElementsMatch(t, []string{"/data_generation_jobs", "/evaluator_generation_jobs"}, submitted)
			close(release)
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("state must be persisted before the rubric worker finishes")
			}
			if tc.datasetFails {
				require.ErrorContains(t, runErr, "fixture failure")
			} else {
				require.NoError(t, runErr)
			}
			env.mu.Lock()
			recordedLevel := env.stored(t, levelKey)
			preserved := env.stored(t, "unrelated")
			recordedVersion := env.stored(t, versionKey("dataset", "seeds"))
			recordedDigest := env.stored(t, project.FingerprintKey("dataset", "seeds"))
			writes := env.writeCount
			env.mu.Unlock()
			assert.Equal(t, "conversation", recordedLevel)
			assert.Equal(t, "preserved", preserved)
			assert.Equal(t, recordedLevel, ec.privateValue(ctx, levelKey), "the cache agrees after worker completion")
			wantWrites := 1
			if !tc.noWait && !tc.datasetFails {
				wantWrites = 3
				assert.Equal(t, "1", recordedVersion)
				path := filepath.Join(dir, "datasets", "seeds.jsonl")
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				assert.Equal(t, digest, recordedDigest)
				content, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, rows, string(content))
			} else {
				assert.Empty(t, recordedVersion)
				assert.Empty(t, recordedDigest)
			}
			assert.Equal(t, wantWrites, writes, "only the dataset worker writes private state")
			var document map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &document), "reporting waits for both workers")
			dataset, ok := document["dataset"].(map[string]any)
			require.True(t, ok)
			if tc.noWait || tc.datasetFails {
				assert.Equal(t, "data-job", dataset["job_id"])
			} else {
				assert.Equal(t, "seeds", dataset["name"])
			}
			evaluator, ok := document["evaluator"].(map[string]any)
			require.True(t, ok)
			if tc.noWait {
				assert.Equal(t, "rubric-job", evaluator["job_id"])
			} else {
				assert.Equal(t, "rubric", evaluator["name"])
			}
		})
	}
}
