// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// serviceRelativeDir answers relative to the project, because that is what a
// service's $ref and relativePath are written relative to. Resolved against the
// process's working directory instead, `azd up` from any subdirectory reported
// every dataset as not yet generated and offered to bill a generation job to
// rewrite a file already on disk.
func TestSourcePathsResolveAgainstTheProjectAndNotTheWorkingDirectory(t *testing.T) {
	svc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval"}
	props, err := structpb.NewStruct(map[string]any{"$ref": "evals/azure.eval.yaml"})
	require.NoError(t, err)
	svc.Config = props

	root := filepath.Join(string(filepath.Separator), "work", "proj")
	base := filepath.Join(root, serviceRelativeDir(svc))

	assert.Equal(t, filepath.Join(root, "evals"), base,
		"the base a source: resolves against has to be under the project")
	assert.Equal(t,
		filepath.Join(root, "evals", "datasets", "rows.jsonl"),
		ResolveSource(base, "./datasets/rows.jsonl"),
		"a relative source is the project's, wherever the caller happened to be standing")
}

// An absolute source is still taken as written, project root or not.
func TestAnAbsoluteSourceIsNotRerooted(t *testing.T) {
	// From TempDir so it carries a volume name on Windows, where a leading
	// separator alone is not an absolute path.
	absolute := filepath.Join(t.TempDir(), "rows.jsonl")

	assert.Equal(t, absolute,
		ResolveSource(filepath.Join(string(filepath.Separator), "work", "proj", "evals"), absolute))
}

// max_samples caps the rows the CLI sends on a run. It never reaches the eval
// the service stores, so hashing it recreated the eval for a change the service
// cannot see -- orphaning every run recorded against the old id.
func TestARowCapDoesNotCostAnEvalItsRunHistory(t *testing.T) {
	group := Eval{
		Name:       "quality",
		Dataset:    "regression",
		MaxSamples: 20,
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
	}
	capped := group
	capped.MaxSamples = 50

	before, err := FingerprintGroup(group)
	require.NoError(t, err)
	after, err := FingerprintGroup(capped)
	require.NoError(t, err)

	assert.Equal(t, before, after, "the eval the service holds did not change")

	// And a change the service can see still forks the eval, which is what the
	// fingerprint is for.
	retargeted := group
	retargeted.Dataset = "regression-v2"
	forked, err := FingerprintGroup(retargeted)
	require.NoError(t, err)
	assert.NotEqual(t, before, forked, "a different dataset is a different eval")
}
