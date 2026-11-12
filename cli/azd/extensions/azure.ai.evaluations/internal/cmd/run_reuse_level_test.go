// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rerun repeats the level the previous run was taken at. That level was read
// only from metadata this extension writes, so a run created by the portal or
// an SDK -- which carries the service's own evaluation_level and none of this
// extension's metadata -- was rerun turn-shaped whatever it had been, silently
// regrading a conversation run one turn at a time.
func TestReusedEvaluationLevelPrefersTheServiceField(t *testing.T) {
	t.Parallel()

	portalRun := &eval_api.OpenAIEvalRun{EvaluationLevel: "conversation"}
	assert.Equal(t, "conversation", reusedEvaluationLevel(portalRun),
		"a run this extension did not create still states its level")

	// Runs made before the field was read carry it only in metadata.
	legacyRun := &eval_api.OpenAIEvalRun{
		Metadata: map[string]string{metaEvaluationLevel: "conversation"},
	}
	assert.Equal(t, "conversation", reusedEvaluationLevel(legacyRun),
		"the metadata fallback keeps older runs rerunnable at their own level")

	// When both are present the service's own field is the authority.
	both := &eval_api.OpenAIEvalRun{
		EvaluationLevel: "conversation",
		Metadata:        map[string]string{metaEvaluationLevel: "turn"},
	}
	assert.Equal(t, "conversation", reusedEvaluationLevel(both))

	// Nothing stated stays nothing rather than becoming a default that would
	// rerun the eval at a level nobody chose.
	assert.Empty(t, reusedEvaluationLevel(&eval_api.OpenAIEvalRun{}))
	assert.Empty(t, reusedEvaluationLevel(nil))
}

// The field has to survive the decoder, not just exist on the struct. It is
// sent top-level on the run, beside metadata rather than inside it.
func TestOpenAIEvalRunDecodesTheServiceEvaluationLevel(t *testing.T) {
	t.Parallel()

	const body = `{
      "id": "evalrun_1",
      "status": "completed",
      "evaluation_level": "conversation",
      "metadata": { "something_else": "kept" }
    }`

	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(body), &run))

	assert.Equal(t, "conversation", run.EvaluationLevel)
	assert.Equal(t, "kept", run.Metadata["something_else"])
	assert.Equal(t, "conversation", reusedEvaluationLevel(&run))
}
