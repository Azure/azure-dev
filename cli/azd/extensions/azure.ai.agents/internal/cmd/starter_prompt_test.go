// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderStarterPrompt_SubstitutesProjectPath(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{
		ProjectPath: "/home/user/my-app",
	})
	require.NoError(t, err)
	assert.Contains(t, got, "/home/user/my-app",
		"starter prompt must substitute the ProjectPath into the body")
}

func TestRenderStarterPrompt_IncludesFoundryProjectId(t *testing.T) {
	projectId := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	got, err := renderStarterPrompt(StarterPromptVars{
		ProjectPath:      "/home/user/my-app",
		FoundryProjectId: projectId,
	})
	require.NoError(t, err)
	assert.Contains(t, got, projectId,
		"starter prompt must include the Foundry project resource ID when provided")
}

func TestRenderStarterPrompt_IncludesModelDeployment(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{
		ProjectPath:     "/home/user/my-app",
		ModelDeployment: "gpt-4o-deployment",
	})
	require.NoError(t, err)
	assert.Contains(t, got, "gpt-4o-deployment",
		"starter prompt must include the model deployment name when provided")
	assert.Contains(t, got, "If a model deployment is needed",
		"starter prompt must include the conditional model deployment instruction")
}

func TestRenderStarterPrompt_OmitsFoundryBlocksWhenEmpty(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{ProjectPath: "/home/user/my-app"})
	require.NoError(t, err)
	assert.NotContains(t, got, "Use Foundry project",
		"starter prompt must not mention a Foundry project when FoundryProjectId is empty")
	assert.NotContains(t, got, "If a model deployment is needed",
		"starter prompt must not mention model deployment when ModelDeployment is empty")
}

func TestRenderStarterPrompt_HasNoTrailingWhitespace(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{ProjectPath: "/x"})
	require.NoError(t, err)
	assert.Equal(t, got, strings.TrimRight(got, " \t\n"), "output should not end with whitespace")
}

// TestRenderStarterPrompt_IncludesCoreInstructions pins the contracts the prompt
// MUST carry: tell the agent to use `azd ai`, and the ask-first contract so the user
// knows they will be consulted before billing-impacting steps.
func TestRenderStarterPrompt_IncludesCoreInstructions(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{ProjectPath: "/x"})
	require.NoError(t, err)

	wantPhrases := []string{"azd ai", "Ask me"}
	for _, want := range wantPhrases {
		assert.Contains(t, got, want, "starter prompt must mention %q", want)
	}

	assert.NotContains(t, got, "--no-prompt --from-code",
		"starter prompt must NOT instruct the coding agent to chain --from-code after --no-prompt")
}

// TestRenderStarterPrompt_IsBrief pins the word-count cap. The ARM resource ID from
// FoundryProjectId can be long, so the cap is checked on the baseline (no optional fields).
func TestRenderStarterPrompt_IsBrief(t *testing.T) {
	got, err := renderStarterPrompt(StarterPromptVars{ProjectPath: "/x"})
	require.NoError(t, err)
	words := len(strings.Fields(got))
	assert.LessOrEqual(t, words, 60,
		"starter prompt (baseline, no optional fields) should be brief; got %d words", words)
}

type mapClipboardEnv map[string]string

func (m mapClipboardEnv) Lookup(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func TestCopyToClipboard_SkipsOnCI(t *testing.T) {
	calls := 0
	write := func(string) error {
		calls++
		return nil
	}
	out := copyToClipboardWith("hello", write, mapClipboardEnv{"CI": "true"})
	assert.Equal(t, ClipboardSkipped, out)
	assert.Equal(t, 0, calls, "clipboard write must not be attempted in CI")
}

func TestCopyToClipboard_SkipsOnTermDumb(t *testing.T) {
	calls := 0
	write := func(string) error {
		calls++
		return nil
	}
	out := copyToClipboardWith("hello", write, mapClipboardEnv{"TERM": "dumb"})
	assert.Equal(t, ClipboardSkipped, out)
	assert.Equal(t, 0, calls)
}

func TestCopyToClipboard_SkipsOnSSH(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_TTY"} {
		t.Run(key, func(t *testing.T) {
			out := copyToClipboardWith(
				"hello",
				func(string) error { t.Fatal("write should not be called"); return nil },
				mapClipboardEnv{key: "1.2.3.4 22 5.6.7.8 22"})
			assert.Equal(t, ClipboardSkipped, out)
		})
	}
}

func TestCopyToClipboard_ReturnsFailedOnWriteError(t *testing.T) {
	// Non-headless env (provide DISPLAY on Linux, leave it untouched
	// on other OSes) -> we attempt the write -> write errors ->
	// outcome is Failed.
	env := mapClipboardEnv{"DISPLAY": ":0"}
	write := func(string) error { return errors.New("no clipboard available") }
	out := copyToClipboardWith("hello", write, env)
	assert.Equal(t, ClipboardFailed, out)
}

func TestCopyToClipboard_CopiesWhenHealthy(t *testing.T) {
	env := mapClipboardEnv{"DISPLAY": ":0"}
	var captured string
	write := func(s string) error {
		captured = s
		return nil
	}
	out := copyToClipboardWith("hello", write, env)
	assert.Equal(t, ClipboardCopied, out)
	assert.Equal(t, "hello", captured)
}

func TestPrintStarterPrompt_IncludesHeaderAndBody(t *testing.T) {
	var buf bytes.Buffer
	printStarterPrompt(&buf, "BODY-MARKER")
	got := buf.String()
	assert.Contains(t, got, "Starter prompt for your AI agent:")
	assert.Contains(t, got, "BODY-MARKER")
}
