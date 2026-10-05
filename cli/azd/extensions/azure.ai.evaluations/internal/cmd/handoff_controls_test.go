// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var handoffControls = []struct {
	name  string
	value rune
}{
	{"NUL", '\x00'},
	{"SOH", '\x01'},
	{"BEL", '\a'},
	{"TAB", '\t'},
	{"LF", '\n'},
	{"VT", '\v'},
	{"FF", '\f'},
	{"CR", '\r'},
	{"ESC", '\x1b'},
	{"US", '\x1f'},
	{"DEL", '\x7f'},
	{"C1 start", '\u0080'},
	{"NEL", '\u0085'},
	{"CSI", '\u009b'},
	{"C1 end", '\u009f'},
}

func assertEscapedHandoffValue(t *testing.T, text, value string) {
	t.Helper()
	assert.NotContains(t, text, value, "the original control-bearing value must never reach output")
	escaped := strconv.Quote(value)
	require.Contains(t, text, escaped, "manual guidance must preserve the exact value in escaped form")
	decoded, err := strconv.Unquote(escaped)
	require.NoError(t, err)
	assert.Equal(t, value, decoded)
	assert.Equal(t, -1, strings.IndexFunc(text, func(r rune) bool {
		return r != '\n' && unicode.IsControl(r)
	}), "only the renderer's line breaks may remain in terminal output")
	assert.NotContains(t, text, "VALUE_NEEDS_QUOTING", "manual guidance must not substitute a different value")
}

func TestGenerationHandoffEscapesControlsInEveryInput(t *testing.T) {
	for _, field := range []string{"path", "target", "dataset", "evaluation-level", "evaluator"} {
		for _, control := range handoffControls {
			t.Run(field+"/"+control.name, func(t *testing.T) {
				value := "before" + string(control.value) + "after"
				outcomes := bothGenerated()
				var configPath string
				switch field {
				case "path":
					configPath = value
				case "target":
					outcomes[0].plan.Agent = value
				case "dataset":
					outcomes[0].ref.Name = value
				case "evaluation-level":
					outcomes[0].plan.EvaluationLevel = value
				case "evaluator":
					outcomes[1].ref.Name = value
				}
				require.Empty(t, initHandoff(outcomes, configPath))
				var out bytes.Buffer
				writeGenerationCompleted(&out, outcomes, configPath)
				text := out.String()
				assertEscapedHandoffValue(t, text, value)
				assert.NotContains(t, text, "Next: azd ai eval init")
				assert.Contains(t, text, "No copyable command")
				assert.Contains(t, text, "Generation completed")
				assert.Contains(t, text, "dataset job: datagen-1")
				assert.Contains(t, text, "evaluator job: evaluatorgen-1")
			})
		}
	}
}

func TestInitCreateHandoffEscapesControlsInNameAndPath(t *testing.T) {
	for _, field := range []string{"name", "path"} {
		for _, control := range handoffControls {
			t.Run(field+"/"+control.name, func(t *testing.T) {
				value := "before" + string(control.value) + "after"
				s := scaffold{eval: &project.Eval{Name: "quality"}, configLocation: "team evals/custom.yml"}
				if field == "name" {
					s.eval.Name = value
				} else {
					s.configLocation = value
					assert.Empty(t, s.withPath("azd ai eval create quality"))
				}
				assert.Empty(t, s.targetedCreate())
				assert.Empty(t, s.nextSteps())
				text := messages.InitCreateManualInputs(s.evalName(), s.nextStepConfigLocation())
				assertEscapedHandoffValue(t, text, value)
				assert.Contains(t, text, "no copyable command")
				assert.NotContains(t, text, "Next: azd ai eval create")
			})
		}
	}
}

func TestHandoffCommandsPreserveSafeUnicode(t *testing.T) {
	name := "caf\u00e9-\u8a55\u4fa1"
	path := "team \u8a55\u4fa1/custom.yml"
	outcomes := bothGenerated()
	outcomes[0].plan.Agent = name
	outcomes[0].ref.Name = name + "-data"
	outcomes[1].ref.Name = name + "-rubric"
	var out bytes.Buffer
	writeGenerationCompleted(&out, outcomes, path)
	assert.Contains(t, out.String(), fmt.Sprintf(
		"Next: azd ai eval init --target %s --source dataset --dataset %s-data "+
			"--evaluation-level turn --evaluator builtin.task_completion --evaluator %s-rubric --path %q\n",
		name, name, name, path))
	assert.NotContains(t, out.String(), "No copyable command")
	s := scaffold{eval: &project.Eval{Name: name}, configLocation: path}
	parsed, location := parsedCreateNextStep(t, s.targetedCreate())
	assert.Equal(t, []string{name}, parsed)
	assert.Equal(t, path, location)
}
