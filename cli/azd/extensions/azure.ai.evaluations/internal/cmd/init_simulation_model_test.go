// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type simulationChoiceServer struct {
	conversationPromptServer
	choiceMu sync.Mutex
	choice   *int32
	err      error
	options  []*azdext.SelectOptions
}

func (s *simulationChoiceServer) Select(
	ctx context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	if req.GetOptions().GetMessage() != messages.SelectSimulationModelPrompt() {
		return s.conversationPromptServer.Select(ctx, req)
	}
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	s.options = append(s.options, req.GetOptions())
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.SelectResponse{Value: s.choice}, nil
}

func TestResolveSimulationModelReusesOnlyQualifiedAuthoredReferences(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authored   []string
		explicit   string
		want       string
		wantError  string
		wantOutput string
	}{
		{"one", []string{"connection/simulator"}, "", "connection/simulator", "", "locally authored"},
		{"duplicate", []string{"connection/simulator", "connection/simulator"}, "", "connection/simulator", "", ""},
		{"bare and malformed skipped", []string{"judge", "bad/name/extra", "connection/simulator"},
			"", "connection/simulator", "", ""},
		{"zero", nil, "", "", "--simulation-model", ""},
		{"raw deployment alone", []string{"judge"}, "", "", "--simulation-model", ""},
		{"multiple", []string{"second/model", "first/model"}, "", "", "--simulation-model", ""},
		{"distinct connections", []string{"first/model", "second/model"}, "", "", "ambiguous", ""},
		{"explicit wins", []string{"second/model", "first/model"}, " override/chosen ",
			"override/chosen", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := noPromptCmd(t, true)
			var out bytes.Buffer
			cmd.SetOut(&out)
			model, err := resolveSimulationModel(cmd, tc.explicit, tc.authored)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Empty(t, out.String())
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, model)
				if tc.wantOutput != "" {
					assert.Contains(t, out.String(), tc.wantOutput)
					assert.Contains(t, out.String(), "unverified")
				}
			}
		})
	}
}

func TestResolveSimulationModelPickerAndFreeText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		choice   *int32
		err      error
		want     string
		wantText bool
	}{
		{"first", new(int32(0)), nil, "a/model", false},
		{"second", new(int32(1)), nil, "z/model", false},
		{"another", new(int32(2)), nil, "connection/simulator", true},
		{"missing", nil, nil, "", false},
		{"negative", new(int32(-1)), nil, "", false},
		{"out of range", new(int32(3)), nil, "", false},
		{"cancel", nil, context.Canceled, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &simulationChoiceServer{choice: tc.choice, err: tc.err}
			h := newInitHarness(t, nil, prompts)
			before := initFileSnapshot(t, h.dir)
			cmd := newInitCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetContext(t.Context())
			model, err := resolveSimulationModel(cmd, "", []string{"z/model", "a/model", "z/model"})
			if tc.want == "" {
				require.Error(t, err)
				if tc.err != nil {
					assert.ErrorContains(t, err, tc.err.Error())
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, model)
			}
			prompts.choiceMu.Lock()
			require.Len(t, prompts.options, 1)
			options := prompts.options[0]
			prompts.choiceMu.Unlock()
			require.Len(t, options.Choices, 3)
			assert.Equal(t, "a/model", options.Choices[0].Value)
			assert.Equal(t, "z/model", options.Choices[1].Value)
			assert.Equal(t, "Enter another name", options.Choices[2].Label)
			prompts.mu.Lock()
			if tc.wantText {
				require.Len(t, prompts.models, 1)
				assert.Contains(t, prompts.models[0].HelpMessage, "unverified")
				assert.Contains(t, out.String(), "unverified until service validation")
			} else {
				assert.Empty(t, prompts.models)
			}
			prompts.mu.Unlock()
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			assert.Zero(t, h.project.wiringAttempts())
		})
	}
}

func TestInitSimulationModelDiscoveryBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		models   string
		explicit string
		cancel   bool
		want     string
		wantErr  bool
	}{
		{"one", "connection/simulator", "", false, "connection/simulator", false},
		{"explicit overrides", "first/model,second/model", "override/model", false, "override/model", false},
		{"ambiguous", "first/model,second/model", "", false, "", true},
		{"raw judge is not eligible", "judge", "", false, "", true},
		{"confirmation cancellation", "connection/simulator", "", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &conversationPromptServer{decision: scaffoldCancel}
			h := newInitHarness(t, nil, prompts)
			dir := filepath.Join(h.dir, "evals")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			path := filepath.Join(dir, project.EvalConfigBase)
			var body strings.Builder
			body.WriteString("evals:\n")
			for i, model := range strings.Split(tc.models, ",") {
				body.WriteString(fmt.Sprintf(
					"  - name: prior-%d\n    simulation: {model: %s, future_setting: keep}\n", i, model))
			}
			body.WriteString("  - name: unrelated\n    $ref: ./missing.yaml\n")
			require.NoError(t, os.WriteFile(path, []byte(body.String()), 0o600))
			before := initFileSnapshot(t, h.dir)
			args := []string{"--name", "new-quality", "--conversation-mode", "simulation",
				"--target", "agent", "--dataset", "registered", "--judge-model", "judge"}
			if !tc.cancel {
				args = append(args, "--no-prompt", "--output", "json")
			}
			if tc.explicit != "" {
				args = append(args, "--simulation-model", tc.explicit)
			}
			text, err := executeConversationInitWithConnections(t, func(context.Context) ([]eval_api.Connection, error) {
				return []eval_api.Connection{
					{Name: "connection", Type: modelConnectionType},
					{Name: "override", Type: modelConnectionType},
				}, nil
			}, args...)
			if tc.wantErr {
				require.ErrorContains(t, err, "--simulation-model")
				assert.Empty(t, text)
			} else {
				require.NoError(t, err)
			}
			if tc.wantErr || tc.cancel {
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
				assert.Zero(t, h.project.wiringAttempts())
			} else {
				authored, err := project.ReadAuthoredConfig(path)
				require.NoError(t, err)
				entry, ok := authored.Entry(project.SectionEvals, "new-quality")
				require.True(t, ok)
				assert.Equal(t, tc.want, entry.SimulationModel)
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Contains(t, string(raw), "future_setting: keep")
				assert.Contains(t, string(raw), "$ref: ./missing.yaml")
				assert.Contains(t, string(raw), "model: judge")
				assert.NotContains(t, text, "locally authored", "JSON stdout must remain one document")
				assert.True(t, json.Valid([]byte(text)), "JSON stdout must remain one document")
			}
		})
	}
}
