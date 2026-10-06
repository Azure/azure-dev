// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainSimulationModelNeedsNoConnection(t *testing.T) {
	for _, authored := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "authored"}[authored], func(t *testing.T) {
			h := newInitHarness(t, nil)
			if authored {
				dir := filepath.Join(h.dir, "evals")
				require.NoError(t, os.MkdirAll(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase),
					[]byte("evals:\n  - name: previous\n    simulation: {model: gpt-4o-mini}\n"), 0o600))
			}
			args := []string{"--name", "bare-model", "--conversation-mode", "simulation", "--target", "agent",
				"--dataset", "seeds", "--judge-model", "independent-judge", "--no-prompt"}
			if !authored {
				args = append(args, "--simulation-model", "gpt-4o-mini")
			}
			calls := 0
			_, err := executeConversationInitWithConnections(t,
				func(context.Context) ([]eval_api.Connection, error) {
					calls++
					return nil, errors.New("a bare model must not request connections")
				}, args...)
			require.NoError(t, err)
			assert.Zero(t, calls)
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			sim := cfg.Evals[len(cfg.Evals)-1].Simulation
			require.NotNil(t, sim)
			assert.Equal(t, "gpt-4o-mini", sim.Model)
			assert.Equal(t, 1, h.project.wiringAttempts())
		})
	}
}

func TestSimulationModelPreservesExplicitPriority(t *testing.T) {
	cmd := noPromptCmd(t, true)
	cmd.SetContext(t.Context())
	calls := 0
	action := &initAction{listModelConnections: func(context.Context) ([]eval_api.Connection, error) {
		calls++
		return []eval_api.Connection{{Name: "qualified", Type: modelConnectionType}}, nil
	}}
	for _, tc := range []struct {
		explicit string
		authored []string
		want     string
		calls    int
	}{
		{"plain-model", []string{"other/plain", "third/model"}, "plain-model", 0},
		{"qualified/model", []string{"plain-model"}, "qualified/model", 1},
	} {
		model, err := action.resolveSimulationModel(cmd, tc.explicit, tc.authored)
		require.NoError(t, err)
		assert.Equal(t, tc.want, model)
		assert.Equal(t, tc.calls, calls)
	}
}

func TestSimulationModelCancellationMakesNoConnectionRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd := noPromptCmd(t, true)
	cmd.SetContext(ctx)
	calls := 0
	action := &initAction{listModelConnections: func(context.Context) ([]eval_api.Connection, error) {
		calls++
		return nil, nil
	}}
	model, err := action.resolveSimulationModel(cmd, "plain-model", nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, model)
	assert.Zero(t, calls)
}

func TestSimulationProducerPreservesModelAndCounts(t *testing.T) {
	for _, model := range []string{"plain-model", "connection/model"} {
		source := eval_api.NewSimulationDataSource("agent", model, 3, 7)
		require.NotNil(t, source.ModelConfiguration)
		assert.Equal(t, model, source.ModelConfiguration.Model)
		require.NotNil(t, source.DefaultSimulationConfiguration)
		assert.Equal(t, 3, source.DefaultSimulationConfiguration.ConversationRepetitions)
		assert.Equal(t, 7, source.DefaultSimulationConfiguration.MaxNumTurns)
	}
}
