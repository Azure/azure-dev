// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceSampleCapSchemaAndRuntimeAgree(t *testing.T) {
	t.Parallel()

	const resourceURI = "https://example.test/eval.schema.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, evalSchemaDocument(t)))
	schema, err := compiler.Compile(resourceURI)
	require.NoError(t, err)

	for name, source := range map[string]map[string]any{
		"traces":     {"type": "traces", "agent_name": "agent", "max_traces": 2},
		"responses":  {"type": "responses", "response_ids": []string{"response"}},
		"referenced": {"$ref": "source.yaml"},
	} {
		for _, tc := range []struct {
			name string
			cap  *int
		}{
			{name: "omitted"},
			{name: "zero", cap: new(0)},
			{name: "positive", cap: new(1)},
			{name: "negative", cap: new(-1)},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				eval := map[string]any{
					"name": "quality", "source": source,
					"evaluators": []any{map[string]any{"evaluator": "builtin.relevance"}},
				}
				if tc.cap != nil {
					eval["max_samples"] = *tc.cap
				}
				body, err := json.Marshal(map[string]any{"evals": []any{eval}})
				require.NoError(t, err)
				var instance any
				require.NoError(t, json.Unmarshal(body, &instance))
				schemaErr := schema.Validate(instance)

				dir := t.TempDir()
				path := filepath.Join(dir, "azure.eval.yaml")
				require.NoError(t, os.WriteFile(path, body, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "source.yaml"),
					[]byte("type: traces\nagent_name: agent\nmax_traces: 2\n"), 0o600))
				cfg, err := project.LoadEvalConfig(path)
				require.NoError(t, err)
				require.NoError(t, cfg.ValidateForLookup(), "listing by name does not validate run settings")
				runtimeErr := cfg.Validate()

				if tc.cap != nil && *tc.cap != 0 {
					assert.Error(t, schemaErr)
					require.ErrorContains(t, runtimeErr, "max_samples")
					assert.Contains(t, runtimeErr.Error(), "quality")
					if *tc.cap > 0 {
						assert.Contains(t, azdext.WrapError(runtimeErr).GetMessage(), "quality")
						local, ok := errors.AsType[*azdext.LocalError](runtimeErr)
						require.True(t, ok)
						assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
						assert.Contains(t, local.Suggestion, "source.max_traces")
						assert.Contains(t, local.Suggestion, "source.response_ids")
					}
				} else {
					assert.NoError(t, schemaErr)
					assert.NoError(t, runtimeErr)
				}
			})
		}
	}
}
