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

// azd does not re-root an absolute `$ref` or an absolute `project:`, so neither
// may the base a `source:` resolves against: joining one under the project
// produced <root>/C:/shared/evals, which is the same failure the join was added
// to fix, reached from a different input.
func TestAnAbsoluteServiceRefIsNotJoinedUnderTheProject(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared", "evals")

	svc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval"}
	props, err := structpb.NewStruct(map[string]any{
		"$ref": filepath.ToSlash(filepath.Join(shared, "azure.eval.yaml")),
	})
	require.NoError(t, err)
	svc.Config = props

	relative := serviceRelativeDir(svc)
	require.True(t, filepath.IsAbs(relative), "the fixture has to exercise the absolute branch")

	// A project root that is not empty, or evalBaseDir returns early on the
	// fallback and the guard under test is never reached.
	assert.Equal(t, relative,
		baseDirUnder(filepath.Join(string(filepath.Separator), "work", "proj"), svc),
		"an absolute ref names its own directory, wherever the project is")

	// And a relative ref is still joined, which is what the guard must not undo.
	relativeSvc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval", RelativePath: "evals"}
	root := filepath.Join(string(filepath.Separator), "work", "proj")
	assert.Equal(t, filepath.Join(root, "evals"), baseDirUnder(root, relativeSvc))
}

// max_samples and source: cap and window a run. Neither reaches the eval the
// service stores, so recreating the eval for a change to either points the
// declaration at a new id and leaves every run taken before it reachable only
// through the old one.
func TestARowCapDoesNotCostAnEvalItsRunHistory(t *testing.T) {
	group := Eval{
		Name:       "quality",
		Dataset:    "regression",
		MaxSamples: 20,
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
	}
	capped := group
	capped.MaxSamples = 50

	before, err := FingerprintDefinition(group)
	require.NoError(t, err)
	after, err := FingerprintDefinition(capped)
	require.NoError(t, err)

	assert.Equal(t, before, after, "the eval the service holds did not change")

	// source: is applied per run too. Its window and agent name reach the run's
	// data source, never CreateOpenAIEvalRequest.
	widened := group
	widened.Source = &SourceDecl{Type: "traces", LookbackHours: 48}
	narrow := group
	narrow.Source = &SourceDecl{Type: "traces", LookbackHours: 24}

	wide, err := FingerprintDefinition(widened)
	require.NoError(t, err)
	tight, err := FingerprintDefinition(narrow)
	require.NoError(t, err)
	assert.Equal(t, wide, tight, "a different lookback is the same eval")

	// And a change the service can see still forks the eval, which is what the
	// fingerprint is for.
	retargeted := group
	retargeted.Dataset = "regression-v2"
	forked, err := FingerprintDefinition(retargeted)
	require.NoError(t, err)
	assert.NotEqual(t, before, forked, "a different dataset is a different eval")
}

// The identity digest is the other half, and it must keep them: it is also the
// key a rename looks the eval up by, so two declarations differing only in
// their window would share it -- the second would adopt the first one's id,
// never be created, and rename the first to whichever came last in the file.
func TestTwoEvalsDifferingOnlyInTheirWindowStayTwoEvals(t *testing.T) {
	base := Eval{
		Dataset:    "",
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
	}

	recent := base
	recent.Name = "last-24h"
	recent.Source = &SourceDecl{Type: "traces", AgentName: "chat", LookbackHours: 24}

	weekly := base
	weekly.Name = "last-7d"
	weekly.Source = &SourceDecl{Type: "traces", AgentName: "billing", LookbackHours: 168}

	first, err := FingerprintGroup(recent)
	require.NoError(t, err)
	second, err := FingerprintGroup(weekly)
	require.NoError(t, err)

	assert.NotEqual(t, first, second,
		"one key for both hands the second declaration the first one's eval")

	capped := base
	capped.Name = "sampled"
	capped.MaxSamples = 20
	uncapped := base
	uncapped.Name = "full"

	cappedDigest, err := FingerprintGroup(capped)
	require.NoError(t, err)
	uncappedDigest, err := FingerprintGroup(uncapped)
	require.NoError(t, err)
	assert.NotEqual(t, cappedDigest, uncappedDigest, "the same holds for a row cap")
}
