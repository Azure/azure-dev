// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// This composition regression uses the selected version, not unrelated local rows.
func TestMappingPinnedVersionUsesSelectedSavedAnswer(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, cached := range []bool{false, true} {
			name := caller + "/new"
			if cached {
				name = caller + "/cached"
			}
			t.Run(name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				path := filepath.Join(dir, "rows.jsonl")
				local := []byte("{\"query\":\"local v2 question\",\"response\":\"local v2 answer\"}\n")
				require.NoError(t, os.WriteFile(path, local, 0o600))
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
				env.state[versionKey("dataset", "turn-tests")] = "2"
				cfg.Datasets[0].Version = "1"
				cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"response": "{{item.saved_answer}}"}
				service.dataset = true
				service.registeredRows = "{\"query\":\"selected v1 question\",\"saved_answer\":\"selected v1 answer\"}\n"
				service.definition = `{"name":"builtin.valid","version":"1","definition":{"data_schema":` +
					`{"properties":{"query":{"type":"string"},"response":{"type":"string"}},` +
					`"required":["query","response"]}}}`
				if cached {
					schema, err := evaluatorContract(json.RawMessage(service.definition))
					require.NoError(t, err)
					columns := map[string]bool{"query": true, "saved_answer": true}
					stored, err := buildEvalRequest(&cfg.Evals[0],
						map[string]*eval_api.EvaluatorSummary{"builtin.valid": schema}, columns)
					require.NoError(t, err)
					service.storedRequest = stored
					service.eval = true
					env.state[idKey("eval", cfg.Evals[0].Name)] = "eval_valid"
					baseline, err := project.FingerprintDefinition(cfg.Evals[0])
					require.NoError(t, err)
					env.state[project.FingerprintKey("eval", cfg.Evals[0].Name)] = fingerprintEra + baseline
				}
				for range 2 {
					ec.state = nil
					require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				}
				require.Contains(t, service.requests, "POST /datasets/turn-tests/versions/1/credentials")
				require.Equal(t, "1", cfg.Datasets[0].Version)
				require.Equal(t, "2", env.stored(t, versionKey("dataset", "turn-tests")))
				require.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "turn-tests")))
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, local, after)
				for _, request := range service.requests {
					require.NotContains(t, request, "startPendingUpload")
					require.False(t, strings.HasPrefix(request, "PUT "), "must not publish the unrelated v2 rows")
				}
				if cached {
					require.Zero(t, service.createCount)
				} else {
					require.Equal(t, 1, service.createCount)
					require.Len(t, service.createdRequests, 1)
					require.Equal(t, "{{item.saved_answer}}",
						service.createdRequests[0].TestingCriteria[0].DataMapping["response"])
				}
			})
		}
	}
}
