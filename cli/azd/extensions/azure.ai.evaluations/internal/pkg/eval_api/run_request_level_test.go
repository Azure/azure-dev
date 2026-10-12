// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service reads the level from the top of the run request. Carrying it only
// under metadata left it opaque, so the service built turn rows for every run
// and a conversation evaluator was handed rows it cannot score. ADO 5631281.
func TestCreateOpenAIEvalRunRequest_SendsEvaluationLevelAtTheTopLevel(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(&CreateOpenAIEvalRunRequest{
		Name:            "retail-support-multiturn",
		EvaluationLevel: "conversation",
		Metadata:        map[string]string{"evaluation_level": "conversation"},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.Equal(t, "conversation", decoded["evaluation_level"],
		"the level has to sit beside name, not inside metadata")

	metadata, ok := decoded["metadata"].(map[string]any)
	require.True(t, ok, "metadata still travels")
	assert.Equal(t, "conversation", metadata["evaluation_level"],
		"anything listing runs still reads the level from metadata")
}

// An eval that declares no level must not put an empty one on the wire: the
// service would have to interpret "" rather than apply its own default.
func TestCreateOpenAIEvalRunRequest_OmitsAnUnstatedLevel(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(&CreateOpenAIEvalRunRequest{Name: "nightly"})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	_, present := decoded["evaluation_level"]
	assert.False(t, present, "an unstated level is absent, not empty")
}

// Turn is the level every existing eval runs at, and it has to reach the
// service the same way conversation does.
func TestCreateOpenAIEvalRunRequest_SendsTurnLevelToo(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(&CreateOpenAIEvalRunRequest{
		Name:            "nightly",
		EvaluationLevel: "turn",
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.Equal(t, "turn", decoded["evaluation_level"])
}
