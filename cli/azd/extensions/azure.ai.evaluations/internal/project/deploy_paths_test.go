// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// A declared path resolves against the project, not the process's working
// directory. Resolved against the latter, `azd up` from any subdirectory
// reported every dataset as not yet generated and offered to bill a generation
// job to rewrite a file already on disk.
//
// Core rebases `file` and `source` onto the root it was given, so what reaches
// here is already project-relative and the base is the root itself.
func TestSourcePathsResolveAgainstTheProjectAndNotTheWorkingDirectory(t *testing.T) {
	svc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval"}
	props, err := structpb.NewStruct(map[string]any{"$ref": "evals/azure.eval.yaml"})
	require.NoError(t, err)
	svc.Config = props

	root := filepath.Join(string(filepath.Separator), "work", "proj")

	assert.Equal(t,
		filepath.Join(root, "evals", "datasets", "rows.jsonl"),
		ResolveSource(root, filepath.ToSlash(filepath.Join("evals", "datasets", "rows.jsonl"))),
		"a relative source is the project's, wherever the caller happened to be standing")

	// The service still knows its own directory; that is for finding conventions
	// beside it, not for resolving what core already rebased.
	assert.Equal(t, filepath.FromSlash("evals"), serviceRelativeDir(svc))
}

// An absolute source is still taken as written, project root or not.
func TestAnAbsoluteSourceIsNotRerooted(t *testing.T) {
	// From TempDir so it carries a volume name on Windows, where a leading
	// separator alone is not an absolute path.
	absolute := filepath.Join(t.TempDir(), "rows.jsonl")

	assert.Equal(t, absolute,
		ResolveSource(filepath.Join(string(filepath.Separator), "work", "proj", "evals"), absolute))
}

// A configuration kept outside the project still finds its own files.
//
// The base is the project root even here, because core expresses an out-of-tree
// include relative to that root -- `../shared/evals/datasets/rows.jsonl` -- so
// joining it lands back outside. Taking the include's own directory as the base
// instead would apply the rebase twice.
func TestAnAbsoluteServiceRefStillFindsItsOwnFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(outside, "datasets"), 0o750))
	rows := filepath.Join(outside, "datasets", "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "azure.eval.yaml"), []byte(`datasets:
  - name: golden
    file: ./datasets/rows.jsonl
evals:
  - name: nightly
    dataset: golden
`), 0o600))

	svc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval"}
	props, err := structpb.NewStruct(map[string]any{
		"$ref": filepath.ToSlash(filepath.Join(outside, "azure.eval.yaml")),
	})
	require.NoError(t, err)
	svc.Config = props

	cfg, err := EvalConfigFromService(svc, root)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)

	resolved := ResolveSource(root, cfg.Datasets[0].File)
	assert.Equal(t, rows, resolved,
		"an out-of-tree include names files beside itself, not under the project")
	assert.FileExists(t, resolved)

	// baseDirUnder still places a service under the project, which is what
	// AgentInstructionsFromProject uses to find the optimizer's baseline.
	relativeSvc := &azdext.ServiceConfig{Name: "evals", Host: "azure.ai.eval", RelativePath: "evals"}
	flat := filepath.Join(string(filepath.Separator), "work", "proj")
	assert.Equal(t, filepath.Join(flat, "evals"), baseDirUnder(flat, relativeSvc))
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

// simulation: settings ride in the run's data source, so turning up the number
// of conversations or the turn budget changes nothing the service stores on the
// eval. Treating them as part of the definition would recreate an immutable
// eval at a new id on every tuning pass and strand each earlier run behind the
// id it was taken under.
func TestTuningASimulationDoesNotCostAnEvalItsRunHistory(t *testing.T) {
	simulated := func(model string, conversations, turns int) Eval {
		return Eval{
			Name:            "quality",
			Dataset:         "seeds",
			EvaluationLevel: "conversation",
			Target:          &Target{Type: "agent", Name: "my-agent"},
			Evaluators:      evalcore.EvaluatorList{{Evaluator: "builtin.task_completion"}},
			Simulation: &Simulation{
				Model:            model,
				NumConversations: conversations,
				MaxTurns:         turns,
			},
		}
	}

	base, err := FingerprintDefinition(simulated("gpt-4o", 2, 4))
	require.NoError(t, err)

	for _, tuned := range []struct {
		name string
		eval Eval
	}{
		{"a different simulator model", simulated("gpt-4o-mini", 2, 4)},
		{"more conversations", simulated("gpt-4o", 5, 4)},
		{"a longer turn budget", simulated("gpt-4o", 2, 20)},
	} {
		t.Run(tuned.name, func(t *testing.T) {
			after, err := FingerprintDefinition(tuned.eval)
			require.NoError(t, err)
			assert.Equal(t, base, after, "the eval the service holds did not change")
		})
	}

	// Presence is not a run-time setting. It decides whether the stored item
	// schema describes conversations or turn rows, so adding or removing the
	// block is a different eval and has to fork.
	turnLevel := simulated("gpt-4o", 2, 4)
	turnLevel.Simulation = nil
	plain, err := FingerprintDefinition(turnLevel)
	require.NoError(t, err)
	assert.NotEqual(t, base, plain, "a simulation describes a different stored dataset")
}

// Normalizing the simulation must not reach back into the caller's eval. The
// reconciler fingerprints a group it is still about to deploy, and a scrubbed
// model or turn count would deploy a simulation the project never declared.
func TestFingerprintingLeavesTheCallersSimulationIntact(t *testing.T) {
	group := Eval{
		Name:            "quality",
		Dataset:         "seeds",
		EvaluationLevel: "conversation",
		Target:          &Target{Type: "agent", Name: "my-agent"},
		Evaluators:      evalcore.EvaluatorList{{Evaluator: "builtin.task_completion"}},
		Simulation:      &Simulation{Model: "gpt-4o", NumConversations: 3, MaxTurns: 6},
	}

	_, err := FingerprintDefinition(group)
	require.NoError(t, err)

	require.NotNil(t, group.Simulation)
	assert.Equal(t, "gpt-4o", group.Simulation.Model)
	assert.Equal(t, 3, group.Simulation.NumConversations)
	assert.Equal(t, 6, group.Simulation.MaxTurns)
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
