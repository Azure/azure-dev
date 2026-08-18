// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeDataset drops a JSONL file beside a config that registers it, and
// returns the config path. An eval's dataset: is a catalog name, not a path, so
// the declaration is what makes the rows reachable.
func writeDataset(t *testing.T, rows string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "datasets"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "datasets", "d.jsonl"), []byte(rows), 0o600))
	configPath := filepath.Join(dir, "eval.yaml")
	config := "datasets:\n  - name: d\n    source: ./datasets/d.jsonl\n"
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	return configPath
}

const oneRow = `{"query":"q","ground_truth":"a"}` + "\n"

// A trace-backed eval is the first hero scenario, and it is the one shape that
// carries no dataset at all: the service gathers the rows itself. It used to be
// refused for "not naming a target agent", which is the whole point of it.
func TestBuildRunDataSource_Traces(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name: "trace-eval",
		Source: &project.SourceDecl{
			Type:          project.SourceTypeTraces,
			AgentName:     "support-agent",
			LookbackHours: 24,
			MaxTraces:     500,
		},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.NoError(t, err)
	// The legacy azure_ai_traces shape discarded agent_version and start_time
	// without saying so, and re-imposed its own lookback.
	assert.Equal(t, eval_api.EvalRunDataSourceTypeTracePreview, ds.Type)
	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, "agent_filter", ds.TraceSource.Type)
	assert.Equal(t, "support-agent", ds.TraceSource.AgentName)
	assert.Equal(t, 500, ds.TraceSource.MaxTraces)
	// lookback_hours is still honoured, as the window's start bound. Asserted
	// as a distance from now, because a merely non-zero start is also what a
	// lookback that reached forwards would produce.
	assert.InDelta(t, time.Now().Add(-24*time.Hour).Unix(), ds.TraceSource.StartTime, 60)
	assert.Zero(t, ds.TraceSource.EndTime, "an open end means up to now")
	// Nothing is invoked and nothing local is sent.
	assert.Nil(t, ds.Target)
	assert.Nil(t, ds.Source)
}

// Pinning the version is the whole reason the preview shape is used: without
// it a redeployed agent is graded on whichever version the service picked.
func TestBuildRunDataSource_TracesPinsTheAgentVersion(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name: "trace-eval",
		Source: &project.SourceDecl{
			Type:         project.SourceTypeTraces,
			AgentName:    "support-agent",
			AgentVersion: "2",
		},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.NoError(t, err)
	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, "2", ds.TraceSource.AgentVersion)
}

// An explicit window travels intact, in seconds and in UTC. The epochs are
// spelled out because a drift into local time, or into milliseconds, would
// still produce a window the service accepts and grades the wrong span of.
func TestBuildRunDataSource_TracesCarriesAnExplicitWindow(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name: "trace-eval",
		Source: &project.SourceDecl{
			Type:      project.SourceTypeTraces,
			AgentName: "support-agent",
			StartTime: "2026-08-01T00:00:00Z",
			EndTime:   "2026-08-02T00:00:00Z",
		},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.NoError(t, err)
	assert.Equal(t, int64(1785542400), ds.TraceSource.StartTime)
	assert.Equal(t, int64(1785628800), ds.TraceSource.EndTime)
}

// A window nobody can read, or one that holds nothing, is refused here rather
// than by a service that answers with no rows and no reason.
//
// Every input the configuration refuses has to be refused here too. The two
// used to be separate checks and had drifted on six inputs, each accepted by
// one door and refused by the other, so which rules applied depended on how the
// eval was reached.
func TestBuildRunDataSource_TracesRefusesEveryWindowTheConfigWould(t *testing.T) {
	ec := &evalContext{}
	build := func(source *project.SourceDecl) error {
		source.Type = project.SourceTypeTraces
		source.AgentName = "a"
		_, err := ec.buildRunDataSource(context.Background(),
			&project.Eval{Name: "trace-eval", Source: source}, "", 0)
		return err
	}

	cases := []struct {
		name    string
		source  project.SourceDecl
		wantErr string
	}{
		{"start that is not a time", project.SourceDecl{StartTime: "yesterday"}, "not a time"},
		{"start at year one", project.SourceDecl{StartTime: "0001-01-01T00:00:00Z"}, "traces were recorded at"},
		{"start at the unix epoch", project.SourceDecl{StartTime: "1970-01-01T00:00:00Z"}, "traces were recorded at"},
		{"negative lookback", project.SourceDecl{LookbackHours: -24}, "cannot be negative"},
		{"lookback past the bound", project.SourceDecl{LookbackHours: project.MaxLookbackHours + 1}, "beyond the"},
		{"negative cap", project.SourceDecl{MaxTraces: -5}, "source.max_traces"},
		{
			"window declared twice over",
			project.SourceDecl{StartTime: "2026-08-01T00:00:00Z", LookbackHours: 24},
			"keep one",
		},
		{
			"end before start",
			project.SourceDecl{StartTime: "2026-08-02T00:00:00Z", EndTime: "2026-08-01T00:00:00Z"},
			"holds no traces",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := build(&tc.source)
			require.Error(t, err)
			// The eval is named, not the agent: the reader has to know which
			// entry to edit, and a file can declare several evals over one agent.
			assert.Contains(t, err.Error(), `eval "trace-eval"`)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// agent_name under source: is a filter, but an eval that names a target and
// leaves the filter off still means "this agent's traces".
func TestBuildRunDataSource_TracesFallsBackToTargetName(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name:   "trace-eval",
		Source: &project.SourceDecl{Type: project.SourceTypeTraces},
		Target: &project.Target{Type: project.TargetTypeAgent, Name: "support-agent"},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.NoError(t, err)
	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, "support-agent", ds.TraceSource.AgentName)
}

// With neither, the run cannot say whose conversations to read, and saying so
// is more use than letting the service return nothing.
func TestBuildRunDataSource_TracesWithoutAnAgentIsRefused(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name:   "trace-eval",
		Source: &project.SourceDecl{Type: project.SourceTypeTraces},
	}

	_, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "source.agent_name")
}

// Stored responses travel as rows carrying ids, with a data_mapping telling the
// service which field holds one.
func TestBuildRunDataSource_Responses(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name: "replay",
		Source: &project.SourceDecl{
			Type:        project.SourceTypeResponses,
			ResponseIDs: []string{"resp_1", "resp_2"},
			MaxTurns:    3,
		},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.NoError(t, err)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeResponses, ds.Type)
	require.NotNil(t, ds.ItemGenerationParams)
	assert.Equal(t, 3, ds.ItemGenerationParams.MaxNumTurns)
	assert.Equal(t,
		map[string]string{"response_id": "{{item.response_id}}"},
		ds.ItemGenerationParams.DataMapping)
	require.NotNil(t, ds.ItemGenerationParams.Source)
	assert.Len(t, ds.ItemGenerationParams.Source.Content, 2)
}

func TestBuildRunDataSource_ResponsesWithoutIDsIsRefused(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name:   "replay",
		Source: &project.SourceDecl{Type: project.SourceTypeResponses},
	}

	_, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "source.response_ids")
}

// No target means the rows already hold both sides of the exchange, so the run
// scores them as they stand rather than invoking anything.
func TestBuildRunDataSource_NoTargetScoresTheDatasetAsItStands(t *testing.T) {
	ec := &evalContext{}
	configPath := writeDataset(t, oneRow)
	group := &project.Eval{Name: "recorded", Dataset: "d"}

	ds, err := ec.buildRunDataSource(context.Background(), group, configPath, 0)

	require.NoError(t, err)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeJSONL, ds.Type)
	assert.Nil(t, ds.Target)
	require.NotNil(t, ds.Source)
	assert.Len(t, ds.Source.Content, 1)
}

// A model target was accepted by config validation and then sent as
// azure_ai_agent, so the run failed against a resource that does not exist.
func TestBuildRunDataSource_ModelTargetIsSentAsAModel(t *testing.T) {
	ec := &evalContext{}
	configPath := writeDataset(t, oneRow)
	group := &project.Eval{
		Name:    "model-eval",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeModel, Name: "gpt-4o-mini"},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, configPath, 0)

	require.NoError(t, err)
	require.NotNil(t, ds.Target)
	assert.Equal(t, "azure_ai_model", ds.Target.Type)
	assert.Equal(t, "gpt-4o-mini", ds.Target.Model)
	assert.Empty(t, ds.Target.Name, "a model target is addressed by deployment, not by agent name")
}

func TestBuildRunDataSource_AgentTarget(t *testing.T) {
	ec := &evalContext{}
	configPath := writeDataset(t, oneRow)
	group := &project.Eval{
		Name:    "agent-eval",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeAgent, Name: "support-agent"},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, configPath, 0)

	require.NoError(t, err)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeAgentTarget, ds.Type)
	require.NotNil(t, ds.Target)
	assert.Equal(t, "azure_ai_agent", ds.Target.Type)
	assert.Equal(t, "support-agent", ds.Target.Name)
}

// An eval with neither a dataset nor a source: has no rows from anywhere, and
// the error has to name both ways out rather than only the dataset.
func TestBuildRunDataSource_NoRowsFromAnywhere(t *testing.T) {
	ec := &evalContext{}

	_, err := ec.buildRunDataSource(context.Background(), &project.Eval{Name: "empty"}, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dataset:")
	assert.Contains(t, err.Error(), "source:")
}

// A misspelled source.type used to fall through to the dataset path, which
// scored the wrong rows and then blamed the eval for declaring no source.
// Config validation catches it first, but a run reached by id has no config to
// have been validated.
func TestBuildRunDataSource_UnknownSourceTypeIsRefused(t *testing.T) {
	ec := &evalContext{}
	group := &project.Eval{
		Name:    "typo",
		Dataset: "d",
		Source:  &project.SourceDecl{Type: "trace"},
	}

	_, err := ec.buildRunDataSource(context.Background(), group, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "trace")
	assert.NotContains(t, err.Error(), "references no dataset",
		"a declared source must not be reported as no source at all")
}

// --max-samples has to mean the same thing wherever the rows come from.
func TestBuildRunDataSource_MaxSamplesCapsLocalRows(t *testing.T) {
	ec := &evalContext{}
	configPath := writeDataset(t, oneRow+oneRow+oneRow)
	group := &project.Eval{
		Name:    "capped",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeAgent, Name: "a"},
	}

	ds, err := ec.buildRunDataSource(context.Background(), group, configPath, 2)

	require.NoError(t, err)
	require.NotNil(t, ds.Source)
	assert.Len(t, ds.Source.Content, 2)
}
