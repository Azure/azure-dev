// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
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
			name:     "python with pyproject.toml",
			files:    []string{"pyproject.toml"},
			expected: "python main.py",
		},
		{
			name:     "python with requirements.txt",
			files:    []string{"requirements.txt"},
			expected: "python main.py",
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
			files:    []string{"pyproject.toml", "package.json"},
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
			name:         "python detected from pyproject.toml",
			files:        []string{"pyproject.toml"},
			wantLanguage: "python",
			wantStartCmd: "python main.py",
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
