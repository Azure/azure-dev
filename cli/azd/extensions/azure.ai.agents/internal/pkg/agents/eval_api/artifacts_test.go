// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/stretchr/testify/assert"
)

func TestDatasetArtifactPath_Basic(t *testing.T) {
	t.Parallel()
	ref := &opt_eval.DatasetRef{Name: "test-ds", Version: "v2"}
	got := DatasetArtifactPath("/project", ref)
	assert.Equal(t, filepath.Join("/project", "datasets", "test-ds"), got)
}

func TestDatasetArtifactPath_NilRef(t *testing.T) {
	t.Parallel()
	got := DatasetArtifactPath("/project", nil)
	assert.Empty(t, got)
}

func TestDatasetArtifactPath_EmptyName(t *testing.T) {
	t.Parallel()
	ref := &opt_eval.DatasetRef{Name: ""}
	got := DatasetArtifactPath("/project", ref)
	assert.Empty(t, got)
}

func TestDatasetLocalURI(t *testing.T) {
	t.Parallel()
	got := DatasetLocalURI("my-dataset")
	assert.Equal(t, filepath.Join("datasets", "my-dataset"), got)
}

func TestEvaluatorLocalURI(t *testing.T) {
	t.Parallel()
	got := EvaluatorLocalURI("coherence")
	assert.Equal(t, filepath.Join("evaluators", "coherence", "rubric_dimensions.json"), got)
}

func TestIsContainerSAS(t *testing.T) {
	t.Parallel()
	assert.True(t, isContainerSAS("https://blob.core.windows.net/container?sr=c&sig=abc"))
	assert.False(t, isContainerSAS("https://blob.core.windows.net/container?sr=b&sig=abc"))
	assert.False(t, isContainerSAS("https://blob.core.windows.net/container"))
}

func TestFilenameFromURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "data.jsonl", filenameFromURL("https://blob.core.windows.net/c/data.jsonl?sig=abc"))
	assert.Equal(t, "data.jsonl", filenameFromURL("https://blob.core.windows.net/c/prefix/data.jsonl?sig=abc"))
	assert.Equal(t, "data.jsonl", filenameFromURL("https://blob.core.windows.net/c/noext"))
}
