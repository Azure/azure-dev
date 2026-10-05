// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This integration-only matrix runs with immutable mapping commit 081c46
// overlaid on the lifecycle helper. It does not replace either owner's tests.
func TestCombinedDefaultInteractionsBeforePublication(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, tc := range []struct {
			name, rows, level, target, missing string
		}{
			{"turn missing query", `{"response":"answer"}`, "turn", "", `"query"`},
			{"turn missing response", `{"query":"question"}`, "turn", "", `"response"`},
			{"later missing response", "{\"query\":\"q\",\"response\":\"a\"}\n{\"query\":\"q2\"}", "turn", "", `"response"`},
			{"conversation missing transcript", `{"query":"q","response":"a"}`, "conversation", "", `"messages"`},
			{
				"agent later missing query", "{\"query\":\"q\"}\n{\"response\":\"a\"}",
				"turn", project.TargetTypeAgent, `"query"`,
			},
			{"valid turn without tools", `{"query":"q","response":"a"}`, "turn", "", ""},
			{"valid conversation without tools", `{"messages":[]}`, "conversation", "", ""},
			{"agent generated response", `{"query":"q"}`, "turn", project.TargetTypeAgent, ""},
			{"model generated response", `{"query":"q"}`, "turn", project.TargetTypeModel, ""},
		} {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				service.definition = `{"definition":{"data_schema":{"properties":{}}}}`
				cfg.Evals[0].EvaluationLevel = tc.level
				if tc.target != "" {
					cfg.Evals[0].Target = &project.Target{Type: tc.target, Name: "target"}
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(tc.rows), 0o600))
				err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
				if tc.missing == "" {
					require.NoError(t, err)
					require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
					assert.Equal(t, 1, service.createCount)
					assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
					return
				}
				require.ErrorContains(t, err, tc.missing)
				for _, request := range service.requests {
					assert.True(t, strings.HasPrefix(request, "GET "), "unexpected publication: %s", request)
				}
				assert.False(t, service.dataset)
				assert.Zero(t, service.createCount)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
			})
		}
	}
}
