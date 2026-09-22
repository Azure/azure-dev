// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func simulationLoaderConfig(simulation map[string]any) map[string]any {
	return map[string]any{
		"datasets": []any{map[string]any{"name": "seeds"}},
		"evals": []any{map[string]any{
			"name": "quality", "dataset": "seeds", "evaluation_level": "conversation",
			"simulation": simulation,
			"target":     map[string]any{"type": "agent", "name": "agent"},
			"evaluators": []any{map[string]any{"evaluator": "builtin.task_completion"}},
		}},
	}
}

func TestSimulationProductionLoadersRejectExplicitZero(t *testing.T) {
	for _, field := range []string{"num_conversations", "max_turns"} {
		t.Run(field, func(t *testing.T) {
			want := "simulation." + field + " is 0"
			config := simulationLoaderConfig(map[string]any{"model": "simulator", field: 0})
			flow, err := json.Marshal(config)
			require.NoError(t, err)
			block := fmt.Sprintf("evals:\n  - name: quality\n    simulation:\n      model: simulator\n      %s: 0\n", field)
			merged := fmt.Sprintf("evals:\n  - name: quality\n    simulation:\n"+
				"      <<: &defaults {model: simulator, %s: 0}\n", field)
			for _, tc := range []struct {
				name string
				body string
			}{{"flow JSON", string(flow)}, {"block YAML", block}, {"merged YAML", merged}} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "azure.eval.yaml")
					require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
					_, err := LoadEvalConfig(path)
					require.ErrorContains(t, err, want)
					_, err = OpenEvalConfig(filepath.Dir(path))
					require.ErrorContains(t, err, want)
				})
			}
			_, err = EvalConfigFromService(serviceWith(t, config), "")
			require.ErrorContains(t, err, want, "inline services must use the same presence checks")
		})
	}
}

func TestSimulationProductionLoadersPreserveOmissionsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		simulation map[string]any
		count      int
		turns      int
	}{
		{"omitted", map[string]any{"model": "simulator"}, 0, 0},
		{"minimum", map[string]any{"model": "simulator", "num_conversations": 1, "max_turns": 1}, 1, 1},
		{"maximum", map[string]any{"model": "simulator", "num_conversations": 5, "max_turns": 20}, 5, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := simulationLoaderConfig(tc.simulation)
			raw, err := json.Marshal(config)
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "azure.eval.yaml")
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			fromFile, err := LoadEvalConfig(path)
			require.NoError(t, err)
			fromService, err := EvalConfigFromService(serviceWith(t, config), "")
			require.NoError(t, err)
			for _, cfg := range []*EvalConfig{fromFile, fromService} {
				require.Len(t, cfg.Evals, 1)
				sim := cfg.Evals[0].Simulation
				require.NotNil(t, sim)
				assert.Equal(t, tc.count, sim.NumConversations)
				assert.Equal(t, tc.turns, sim.MaxTurns)
				assert.NoError(t, cfg.Validate())
			}
		})
	}
}

func TestSimulationProductionDecoderRemainsStrict(t *testing.T) {
	body := "evals:\n  - name: quality\n    simulation:\n      model: simulator\n      max_turn: 2\n"
	_, err := DecodeEvalConfig([]byte(body), "azure.eval.yaml")
	require.ErrorContains(t, err, `unknown key "max_turn"`)
	assert.Contains(t, err.Error(), `did you mean "max_turns"`)
	assert.Contains(t, err.Error(), "line 5")

	_, err = EvalConfigFromService(serviceWith(t, simulationLoaderConfig(
		map[string]any{"model": "simulator", "max_turn": 2})), "")
	require.ErrorContains(t, err, `unknown key "max_turn"`)

	for _, field := range []string{"num_conversations", "max_turns"} {
		_, err := DecodeEvalConfig([]byte(strings.ReplaceAll(body, "max_turn: 2", field+": null")), "azure.eval.yaml")
		require.ErrorContains(t, err, "simulation."+field+" is 0")
	}
}
