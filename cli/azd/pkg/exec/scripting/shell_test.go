// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateShell(t *testing.T) {
	tests := []struct {
		shell   string
		wantErr bool
	}{
		{"", false},
		{"bash", false},
		{"sh", false},
		{"zsh", false},
		{"pwsh", false},
		{"powershell", false},
		{"cmd", false},
		{"BASH", false},
		{"Pwsh", false},
		{"CMD", false},
		{"python", true},
		{"invalid", true},
		{"node", true},
	}

	for _, tt := range tests {
		name := tt.shell
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			err := ValidateShell(tt.shell)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDetectShellFromFile(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"script.sh", "bash"},
		{"script.bash", "bash"},
		{"script.zsh", "zsh"},
		{"script.ps1", "pwsh"},
		{"script.cmd", "cmd"},
		{"script.bat", "cmd"},
		{"SCRIPT.SH", "bash"},
		{"SCRIPT.PS1", "pwsh"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := DetectShellFromFile(tt.file)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectShellFromFile_UnknownExtension(t *testing.T) {
	got := DetectShellFromFile("script.txt")
	want := "bash"
	if runtime.GOOS == osWindows {
		want = "powershell"
	}
	assert.Equal(t, want, got)
}

func TestDetectShellFromFile_NoExtension(t *testing.T) {
	got := DetectShellFromFile("Makefile")
	want := "bash"
	if runtime.GOOS == osWindows {
		want = "powershell"
	}
	assert.Equal(t, want, got)
}

func TestDefaultShell(t *testing.T) {
	got := DefaultShell()
	if runtime.GOOS == osWindows {
		assert.Equal(t, "powershell", got)
	} else {
		assert.Equal(t, "bash", got)
	}
}
