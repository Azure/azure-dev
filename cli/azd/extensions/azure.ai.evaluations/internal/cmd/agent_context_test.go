// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generation spec names an instructions file relative to itself, not to the
// working directory, so `generate --config <elsewhere>` reads the same file the
// author sees next to the spec.
func TestAgentContextInstructions_ResolvesRelativeToTheSpec(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "evals")
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "agent"), 0o755))

	body := "Answer only from the product catalog."
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "agent", "instructions.md"), []byte("  "+body+"\n"), 0o600))

	cfg := &project.GenerateConfig{}
	cfg.Agent.Context.Instructions = "./agent/instructions.md"

	got, err := agentContextInstructions(cfg, filepath.Join(specDir, "eval_generate.yaml"))
	require.NoError(t, err)
	assert.Equal(t, body, got, "the file's contents should be used, trimmed")
}

// `init` writes the instructions path before that file exists. Treating the
// gap as an error would break the flow init itself scaffolds.
func TestAgentContextInstructions_MissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	cfg := &project.GenerateConfig{}
	cfg.Agent.Context.Instructions = "./agent/instructions.md"

	got, err := agentContextInstructions(cfg, filepath.Join(dir, "eval_generate.yaml"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAgentContextInstructions_UnsetIsEmpty(t *testing.T) {
	got, err := agentContextInstructions(&project.GenerateConfig{}, "eval_generate.yaml")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// Only the newest version is read, and an agent with no published version must
// not panic the caller.
func TestAgentInstructions(t *testing.T) {
	var agent eval_api.Agent
	require.NoError(t, json.Unmarshal([]byte(`{
      "name": "support",
      "versions": { "latest": { "version": "2", "definition": {
        "model": "gpt-5-mini",
        "instructions": "  You are a support assistant.\n" } } }
    }`), &agent))
	assert.Equal(t, "You are a support assistant.", agent.Instructions())

	var empty eval_api.Agent
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","versions":{}}`), &empty))
	assert.Empty(t, empty.Instructions(), "an agent with no published version has no instructions")

	var nilAgent *eval_api.Agent
	assert.Empty(t, nilAgent.Instructions())
}

// Dataset generation has no model of its own; it runs against the judge model
// the spec declares.
func TestGenerationModel(t *testing.T) {
	cfg := &project.GenerateConfig{}
	assert.Empty(t, generationModel(cfg), "no rubric means no model to borrow")

	cfg.Generate.Rubric = &project.RubricSpec{Model: "gpt-4.1-nano"}
	assert.Equal(t, "gpt-4.1-nano", generationModel(cfg))
}

// `tools` is accepted and ignored, so it has to be called out — the same
// reasoning as the trace fields it now shares a warning with.
func TestWarnIgnoredFields_CoversTools(t *testing.T) {
	cases := []struct {
		name  string
		build func(*project.GenerateConfig)
		want  []string
		quiet bool
	}{
		{
			name:  "nothing set stays silent",
			build: func(*project.GenerateConfig) {},
			quiet: true,
		},
		{
			name:  "tools alone",
			build: func(c *project.GenerateConfig) { c.Agent.Context.Tools = "./agent/tools.json" },
			want:  []string{"agent.context.tools", "has no effect"},
		},
		{
			name: "tools and a trace field agree in number",
			build: func(c *project.GenerateConfig) {
				c.Agent.Context.Tools = "./agent/tools.json"
				c.Agent.Context.Traces = &project.TraceSpec{Source: "app-insights"}
			},
			want: []string{"agent.context.traces.source", "agent.context.tools", "have no effect"},
		},
		{
			name: "a window alone is honored, so no warning",
			build: func(c *project.GenerateConfig) {
				c.Agent.Context.Traces = &project.TraceSpec{Window: "7d"}
			},
			quiet: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &project.GenerateConfig{}
			tc.build(cfg)

			var buf bytes.Buffer
			warnIgnoredTraceFields(cfg, &buf)

			if tc.quiet {
				assert.Empty(t, buf.String())
				return
			}
			for _, want := range tc.want {
				assert.Contains(t, buf.String(), want)
			}
		})
	}
}

// init scaffolds only the context fields that are read.
func TestInitScaffold_OmitsToolsButKeepsInstructions(t *testing.T) {
	cfg := buildGenerateScaffold("support-agent", "support-agent-quality", "gpt-4.1-nano")
	assert.Equal(t, "./agent/instructions.md", cfg.Agent.Context.Instructions)
	assert.Empty(t, cfg.Agent.Context.Tools,
		"scaffolding a field nothing reads would warn on every default init")
}
