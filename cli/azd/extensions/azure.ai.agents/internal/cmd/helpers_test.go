// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestDetectStartupCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    []string // files to create in a temp directory
		expected string
	}{
		{
			name:     "python with pyproject.toml and main.py",
			files:    []string{"pyproject.toml", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with pyproject.toml but no main.py",
			files:    []string{"pyproject.toml"},
			expected: "",
		},
		{
			name:     "python with requirements.txt and main.py",
			files:    []string{"requirements.txt", "main.py"},
			expected: "python main.py",
		},
		{
			name:     "python with requirements.txt but no main.py",
			files:    []string{"requirements.txt"},
			expected: "",
		},
		{
			name:     "python with main.py only",
			files:    []string{"main.py"},
			expected: "python main.py",
		},
		{
			name:     "dotnet with csproj",
			files:    []string{"MyAgent.csproj"},
			expected: "dotnet run",
		},
		{
			name:     "node with package.json",
			files:    []string{"package.json"},
			expected: "npm start",
		},
		{
			name:     "unknown project type",
			files:    []string{"README.md"},
			expected: "",
		},
		{
			name:     "empty directory",
			files:    nil,
			expected: "",
		},
		{
			name:     "pyproject.toml takes precedence over package.json",
			files:    []string{"pyproject.toml", "main.py", "package.json"},
			expected: "python main.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			got := detectStartupCommand(dir)
			if got != tt.expected {
				t.Errorf("detectStartupCommand() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectProjectType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		files        []string
		wantLanguage string
		wantStartCmd string
	}{
		{
			name:         "python detected from pyproject.toml with main.py",
			files:        []string{"pyproject.toml", "main.py"},
			wantLanguage: "python",
			wantStartCmd: "python main.py",
		},
		{
			name:         "python detected but no start cmd without entry point",
			files:        []string{"pyproject.toml"},
			wantLanguage: "python",
			wantStartCmd: "",
		},
		{
			name:         "dotnet detected from csproj",
			files:        []string{"Agent.csproj"},
			wantLanguage: "dotnet",
			wantStartCmd: "dotnet run",
		},
		{
			name:         "node detected from package.json",
			files:        []string{"package.json"},
			wantLanguage: "node",
			wantStartCmd: "npm start",
		},
		{
			name:         "unknown when no markers",
			files:        []string{"Dockerfile"},
			wantLanguage: "unknown",
			wantStartCmd: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
					t.Fatalf("failed to create test file %s: %v", f, err)
				}
			}

			pt := detectProjectType(dir)
			if pt.Language != tt.wantLanguage {
				t.Errorf("Language = %q, want %q", pt.Language, tt.wantLanguage)
			}
			if pt.StartCmd != tt.wantStartCmd {
				t.Errorf("StartCmd = %q, want %q", pt.StartCmd, tt.wantStartCmd)
			}
		})
	}
}

func TestToServiceKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple name", input: "myagent", want: "MYAGENT"},
		{name: "with dashes", input: "my-agent-svc", want: "MY_AGENT_SVC"},
		{name: "with spaces", input: "my agent svc", want: "MY_AGENT_SVC"},
		{name: "mixed dashes and spaces", input: "my-agent svc", want: "MY_AGENT_SVC"},
		{name: "already uppercase", input: "MY_AGENT", want: "MY_AGENT"},
		{name: "empty string", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := toServiceKey(tt.input)
			if got != tt.want {
				t.Errorf("toServiceKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateSessionID(t *testing.T) {
	t.Parallel()

	id := generateSessionID()

	if len(id) != 25 {
		t.Errorf("expected length 25, got %d", len(id))
	}

	validChars := regexp.MustCompile(`^[a-z0-9]+$`)
	if !validChars.MatchString(id) {
		t.Errorf("session ID contains invalid characters: %q", id)
	}

	// Two calls should produce different IDs (probabilistic, but collision is vanishingly unlikely)
	id2 := generateSessionID()
	if id == id2 {
		t.Errorf("two consecutive calls produced the same ID: %q", id)
	}
}
