// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorPointReadFailureStopsPublication(t *testing.T) {
	for _, caller := range []string{"create", "up", "ensure evaluator", "evaluator create", "evaluator update"} {
		t.Run(caller, func(t *testing.T) {
			quickEvaluatorSettle(t)
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			service.deniedVersion, service.deniedStatus = "2", http.StatusNotFound
			cfg.Evaluators[0].Version = ""
			cfg.Evaluators[0].Source = "custom.json"
			path := filepath.Join(dir, "custom.json")
			raw := []byte(`{"type":"rubric","dimensions":[{"id":"a","weight":5}]}`)
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			env.state = map[string]string{
				project.FingerprintKey("evaluator", "custom"): "old-authored-digest",
				versionKey("evaluator", "custom"):             "1",
				"unrelated":                                   "preserve",
			}
			before := maps.Clone(env.state)
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			cmd.SetOut(&out)
			var err error
			switch caller {
			case "ensure evaluator":
				_, _, err = (&evalReconciler{ec: ec}).EnsureEvaluator(t.Context(), cfg.Evaluators[0], path)
			case "evaluator create", "evaluator update":
				body, normalizeErr := normalizeRubricBody("custom", raw)
				require.NoError(t, normalizeErr)
				verb := "create"
				if caller == "evaluator update" {
					verb = "update"
				}
				err = (&evaluatorWriteAction{cmd: cmd, name: "custom", verb: verb}).write(t.Context(), ec, body)
			default:
				err = reconcileArtifactConfig(t, caller, ec, cfg, dir)
			}
			assert.Error(t, err)
			assert.True(t, eval_api.IsNotFound(err), "the original point-read status remains inspectable")
			assert.False(t, eval_api.IsEvaluatorAbsent(err), "publication must not infer evaluator absence")
			assert.Zero(t, service.publishes)
			assert.Empty(t, service.created)
			assert.Empty(t, env.config)
			assert.Empty(t, env.values)
			assert.Equal(t, before, env.state)
			assert.Empty(t, out.String())
			assert.Equal(t, []string{"/evaluators/custom/versions", "/evaluators/custom/versions/2"}, service.reads,
				"do not enter absence retries or publishing after a listed version fails to read")
		})
	}
}
