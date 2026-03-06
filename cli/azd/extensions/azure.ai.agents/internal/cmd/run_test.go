// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestParseCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple command",
			input: "python main.py",
			want:  []string{"python", "main.py"},
		},
		{
			name:  "single word",
			input: "python",
			want:  []string{"python"},
		},
		{
			name:  "double-quoted argument",
			input: `python "my script.py"`,
			want:  []string{"python", "my script.py"},
		},
		{
			name:  "single-quoted argument",
			input: `python 'my script.py'`,
			want:  []string{"python", "my script.py"},
		},
		{
			name:  "multiple arguments",
			input: "dotnet run --project MyAgent.csproj",
			want:  []string{"dotnet", "run", "--project", "MyAgent.csproj"},
		},
		{
			name:  "extra spaces",
			input: "  python   main.py   --verbose  ",
			want:  []string{"python", "main.py", "--verbose"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "only spaces",
			input: "   ",
			want:  nil,
		},
		{
			name:  "quoted string with spaces in middle",
			input: `cmd "arg one" "arg two"`,
			want:  []string{"cmd", "arg one", "arg two"},
		},
		{
			name:  "mixed quotes",
			input: `python "my app.py" --flag 'value with spaces'`,
			want:  []string{"python", "my app.py", "--flag", "value with spaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseCommand(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseCommand(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveVenvCommand(t *testing.T) {
	t.Parallel()

	t.Run("no venv directory passes through unchanged", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		input := []string{"python", "main.py"}
		got := resolveVenvCommand(dir, input)
		if !slices.Equal(got, []string{"python", "main.py"}) {
			t.Errorf("expected passthrough, got %v", got)
		}
	})

	t.Run("python resolved to venv python", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"python", "main.py"}
		got := resolveVenvCommand(dir, input)

		wantPython := venvPython(filepath.Join(dir, ".venv"))
		if got[0] != wantPython {
			t.Errorf("got[0] = %q, want %q", got[0], wantPython)
		}
		if got[1] != "main.py" {
			t.Errorf("got[1] = %q, want %q", got[1], "main.py")
		}
	})

	t.Run("python3 resolved to venv python", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"python3", "main.py"}
		got := resolveVenvCommand(dir, input)

		wantPython := venvPython(filepath.Join(dir, ".venv"))
		if got[0] != wantPython {
			t.Errorf("got[0] = %q, want %q", got[0], wantPython)
		}
	})

	t.Run("non-python command with binary in venv", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		venvDir := createVenv(t, dir)

		// Create a fake binary in the venv bin dir
		binDir := venvBinDir(venvDir)
		fakeBin := filepath.Join(binDir, "myrunner")
		if err := os.WriteFile(fakeBin, []byte(""), 0755); err != nil {
			t.Fatal(err)
		}

		input := []string{"myrunner", "--serve"}
		got := resolveVenvCommand(dir, input)
		if got[0] != fakeBin {
			t.Errorf("got[0] = %q, want %q", got[0], fakeBin)
		}
	})

	t.Run("non-python command without binary in venv stays unchanged", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"node", "index.js"}
		got := resolveVenvCommand(dir, input)
		if !slices.Equal(got, []string{"node", "index.js"}) {
			t.Errorf("expected passthrough, got %v", got)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		got := resolveVenvCommand(dir, nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// createVenv sets up a minimal .venv directory structure for testing.
// Returns the path to the .venv directory.
func createVenv(t *testing.T, projectDir string) string {
	t.Helper()
	venvDir := filepath.Join(projectDir, ".venv")

	var binDir string
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	} else {
		binDir = filepath.Join(venvDir, "bin")
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a fake python executable
	pythonName := "python"
	if runtime.GOOS == "windows" {
		pythonName = "python.exe"
	}
	if err := os.WriteFile(filepath.Join(binDir, pythonName), []byte(""), 0755); err != nil {
		t.Fatal(err)
	}

	return venvDir
}
