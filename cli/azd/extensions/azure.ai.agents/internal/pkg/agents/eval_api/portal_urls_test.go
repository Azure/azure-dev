// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPortalPrefix_Valid(t *testing.T) {
	t.Parallel()
	resID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1"
	prefix, err := NewPortalPrefix(resID)
	require.NoError(t, err)
	assert.Contains(t, prefix.prefix, "ai.azure.com/nextgen/r/")
	assert.Contains(t, prefix.prefix, "rg1")
	assert.Contains(t, prefix.prefix, "acct1")
	assert.Contains(t, prefix.prefix, "proj1")
}

func TestNewPortalPrefix_InvalidResourceID(t *testing.T) {
	t.Parallel()
	_, err := NewPortalPrefix("not-a-resource-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

func TestNewPortalPrefix_MissingParent(t *testing.T) {
	t.Parallel()
	// Resource ID without a parent (not a nested resource).
	resID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1"
	_, err := NewPortalPrefix(resID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Foundry project")
}

func TestPortalPrefix_EvalRunURL(t *testing.T) {
	t.Parallel()
	p := &PortalPrefix{prefix: "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj"}
	url := p.EvalRunURL("eval-123", "run-456")
	assert.Equal(t, "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj/build/evaluations/eval-123/run/run-456", url)
}

func TestPortalPrefix_EvaluatorURL(t *testing.T) {
	t.Parallel()
	p := &PortalPrefix{prefix: "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj"}
	url := p.EvaluatorURL("coherence", "v1")
	assert.Equal(t, "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj/build/evaluations/catalog/coherence/v1", url)
}

func TestPortalPrefix_DatasetURL(t *testing.T) {
	t.Parallel()
	p := &PortalPrefix{prefix: "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj"}
	url := p.DatasetURL("my-dataset", "v2")
	assert.Equal(t, "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj/build/data/datasets/my-dataset/v2", url)
}

func TestPortalPrefix_OptimizationURL(t *testing.T) {
	t.Parallel()
	p := &PortalPrefix{prefix: "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj"}
	url := p.OptimizationURL("my-agent", "op-789")
	assert.Equal(t, "https://ai.azure.com/nextgen/r/sub,rg,,acct,proj/build/agents/my-agent/optimization/op-789", url)
}
