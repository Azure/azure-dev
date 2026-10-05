// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestInitSimulationInvalidUnicodeFlagMakesNoRPCOrWrites(t *testing.T) {
	for _, model := range []string{"mo\x1bdel", "model\x7f", "mo\u0085del", "mo\u00a0del",
		"con\u2003nection/model", "connection/mo\u2028del", "\u00a0model", "model\u3000"} {
		t.Run(fmt.Sprintf("%q", model), func(t *testing.T) {
			calls := 0
			h := newInitHarnessWithOptions(t, nil, []grpc.ServerOption{
				grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
					handler grpc.UnaryHandler) (any, error) {
					calls++
					return handler(ctx, req)
				}),
			})
			before := initFileSnapshot(t, h.dir)
			text, err := executeConversationInitWithConnections(t,
				func(context.Context) ([]eval_api.Connection, error) {
					t.Error("invalid model requested the connection catalogue")
					return nil, nil
				}, "--name", "invalid-model", "--conversation-mode", "simulation", "--target", "agent",
				"--dataset", "seeds", "--judge-model", "judge", "--simulation-model", model, "--no-prompt")
			require.Error(t, err)
			assert.NotContains(t, text+err.Error(), model)
			assert.Zero(t, calls)
			assert.Zero(t, h.project.wiringAttempts())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}
}

func TestInvalidAuthoredSimulationReferenceIsNotOfferedOrPrinted(t *testing.T) {
	for _, model := range []string{"mo\x1bdel", "mo\u00a0del", "connection/mo\u2028del"} {
		t.Run(fmt.Sprintf("%q", model), func(t *testing.T) {
			h := newInitHarness(t, nil)
			dir := filepath.Join(h.dir, "evals")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase),
				[]byte(fmt.Sprintf("evals:\n  - name: prior\n    simulation: {model: %q}\n", model)), 0o600))
			before := initFileSnapshot(t, h.dir)
			calls := 0
			text, err := executeConversationInitWithConnections(t,
				func(context.Context) ([]eval_api.Connection, error) {
					calls++
					return nil, nil
				}, "--name", "invalid-authored", "--conversation-mode", "simulation", "--target", "agent",
				"--dataset", "seeds", "--judge-model", "judge", "--no-prompt")
			require.Error(t, err)
			assert.NotContains(t, text+err.Error(), model)
			assert.Zero(t, calls)
			assert.Zero(t, h.project.wiringAttempts())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}
}

func TestInvalidSimulationFreeTextIsRejectedBeforeOutput(t *testing.T) {
	for _, model := range []string{"mo\x1bdel", "mo\u00a0del", "connection/mo\u2028del", "\u00a0model", "model\u3000"} {
		t.Run(fmt.Sprintf("%q", model), func(t *testing.T) {
			cmd := noPromptCmd(t, false)
			var out bytes.Buffer
			cmd.SetOut(&out)
			prompts := &unsafeSimulationPrompt{model: model}
			h := newInitHarness(t, nil, prompts)
			before := initFileSnapshot(t, h.dir)
			_, err := resolveSimulationModel(cmd, "", nil)
			require.Error(t, err)
			assert.NotContains(t, out.String()+err.Error(), model)
			assert.Zero(t, h.project.wiringAttempts())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}

}

func TestUnicodeSimulationReferencesPreserveCommandSelection(t *testing.T) {
	for _, model := range []string{"mod\u00e8le", "\u63a5\u7d9a/\u6a21\u578b"} {
		t.Run(model, func(t *testing.T) {
			h := newInitHarness(t, nil)
			calls := 0
			_, err := executeConversationInitWithConnections(t,
				func(context.Context) ([]eval_api.Connection, error) {
					calls++
					return []eval_api.Connection{{Name: "\u63a5\u7d9a", Type: modelConnectionType}}, nil
				}, "--name", "unicode-model", "--conversation-mode", "simulation", "--target", "agent",
				"--dataset", "seeds", "--judge-model", "judge", "--simulation-model", model,
				"--num-conversations", "3", "--max-turns", "7", "--no-prompt")
			require.NoError(t, err)
			wantCalls := 0
			if model == "\u63a5\u7d9a/\u6a21\u578b" {
				wantCalls = 1
			}
			assert.Equal(t, wantCalls, calls)
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			assert.Equal(t, model, cfg.Evals[0].Simulation.Model)
			assert.Equal(t, 3, cfg.Evals[0].Simulation.NumConversations)
			assert.Equal(t, 7, cfg.Evals[0].Simulation.MaxTurns)
		})
	}
}

func TestSimulationConnectionChoicesRejectControls(t *testing.T) {
	assert.Equal(t, []string{"safe", "\u63a5\u7d9a"}, eligibleSimulationModelConnections([]eval_api.Connection{
		{Name: "safe", Type: modelConnectionType},
		{Name: "unsafe\x1b", Type: modelConnectionType},
		{Name: "unsafe\u00a0name", Type: modelConnectionType},
		{Name: "\u63a5\u7d9a", Type: modelConnectionType},
	}))
}

type unsafeSimulationPrompt struct {
	conversationPromptServer
	model string
}

func (s *unsafeSimulationPrompt) Prompt(
	_ context.Context, _ *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	return &azdext.PromptResponse{Value: s.model}, nil
}
