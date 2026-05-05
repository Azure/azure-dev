// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestPrintNext_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintNext(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty suggestions, got %q", buf.String())
	}
}

func TestPrintNext_Single(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	var buf bytes.Buffer
	PrintNext(&buf, []Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally"},
	})

	got := buf.String()
	wantSubstrings := []string{
		"\nNext:  azd ai agent run",
		"-- start the agent locally",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}
}

func TestPrintNext_Multi_AlignsCommands(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	var buf bytes.Buffer
	PrintNext(&buf, []Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally"},
		{Command: "azd ai agent invoke --local \"Hello!\"", Description: "test it in another terminal"},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 non-empty lines, got %d:\n%s", len(lines), buf.String())
	}

	// Second line must be indented to align under the first.
	if !strings.HasPrefix(lines[1], "       ") {
		t.Errorf("expected second line to be indented under Next:, got %q", lines[1])
	}

	// "  -- " marker positions must align across lines (use LastIndex to
	// avoid matching "--local" or other CLI flag tokens in the command).
	pos1 := strings.LastIndex(lines[0], "  -- ")
	pos2 := strings.LastIndex(lines[1], "  -- ")
	if pos1 != pos2 {
		t.Errorf("expected -- markers to align, got positions %d vs %d:\n%s", pos1, pos2, buf.String())
	}
}

func TestPrintNext_NoDescription(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	var buf bytes.Buffer
	PrintNext(&buf, []Suggestion{
		{Command: "azd provision"},
	})

	got := buf.String()
	if strings.Contains(got, "--") {
		t.Errorf("expected no -- marker when description is empty, got:\n%s", got)
	}
	if !strings.Contains(got, "Next:  azd provision") {
		t.Errorf("expected command in output, got:\n%s", got)
	}
}

func TestState_PrimaryAgent(t *testing.T) {
	tests := []struct {
		name     string
		services []ServiceState
		want     bool // whether PrimaryAgent returns non-nil
	}{
		{name: "nil services", services: nil, want: false},
		{name: "single", services: []ServiceState{{ServiceName: "a"}}, want: true},
		{name: "multiple", services: []ServiceState{{ServiceName: "a"}, {ServiceName: "b"}}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &State{AgentServices: tc.services}
			got := s.PrimaryAgent() != nil
			if got != tc.want {
				t.Errorf("PrimaryAgent() non-nil = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestState_HasMultipleAgents(t *testing.T) {
	if (&State{}).HasMultipleAgents() {
		t.Error("empty state should not have multiple agents")
	}
	if (&State{AgentServices: []ServiceState{{}}}).HasMultipleAgents() {
		t.Error("single service should not be multiple")
	}
	if !(&State{AgentServices: []ServiceState{{}, {}}}).HasMultipleAgents() {
		t.Error("two services should be multiple")
	}
}

func TestFormatNextForNote(t *testing.T) {
suggestions := []Suggestion{
{Command: "azd ai agent show foo", Description: "inspect"},
{Command: "azd ai agent invoke <payload>", Description: "test it"},
}

t.Run("no hint: omits trailing line", func(t *testing.T) {
got := FormatNextForNote(suggestions, "")
if got == "" {
t.Fatal("expected non-empty Next: block")
}
if strings.Contains(got, "README.md") {
t.Errorf("expected no README hint, got:\n%s", got)
}
if strings.Contains(got, "See ") {
t.Errorf("hint marker leaked into output:\n%s", got)
}
})

t.Run("blank hint also omits trailing line", func(t *testing.T) {
got := FormatNextForNote(suggestions, "   ")
if strings.Contains(got, "See ") {
t.Errorf("whitespace hint should be ignored, got:\n%s", got)
}
})

t.Run("with hint: appends dim line", func(t *testing.T) {
got := FormatNextForNote(suggestions, "See src/foo/README.md for a sample payload.")
if !strings.Contains(got, "src/foo/README.md") {
t.Errorf("expected hint to be present, got:\n%s", got)
}
})

t.Run("subsequent lines are indented for note rendering", func(t *testing.T) {
got := FormatNextForNote(suggestions, "")
lines := strings.Split(got, "\n")
if len(lines) < 2 {
t.Fatalf("expected >=2 lines, got %d", len(lines))
}
// First line should NOT be indented (artifact renderer prefixes it).
if strings.HasPrefix(lines[0], "    ") {
t.Errorf("first line should not be pre-indented: %q", lines[0])
}
// Subsequent lines MUST be indented with 4 spaces.
for i := 1; i < len(lines); i++ {
if !strings.HasPrefix(lines[i], "    ") {
t.Errorf("line %d not indented: %q", i, lines[i])
}
}
})

t.Run("empty suggestions and empty hint: empty string", func(t *testing.T) {
if got := FormatNextForNote(nil, ""); got != "" {
t.Errorf("expected empty, got %q", got)
}
})
}
