// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package shellutil

import (
	"runtime"
	"testing"
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
		t.Run(tt.shell, func(t *testing.T) {
			err := ValidateShell(tt.shell)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateShell(%q) error = %v, wantErr %v", tt.shell, err, tt.wantErr)
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
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := DetectShellFromFile(tt.file)
			if got != tt.want {
				t.Errorf("DetectShellFromFile(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

func TestDetectShellFromFile_Default(t *testing.T) {
	got := DetectShellFromFile("script.txt")
	want := "bash"
	if runtime.GOOS == "windows" {
		want = "powershell"
	}
	if got != want {
		t.Errorf("DetectShellFromFile(script.txt) = %q, want %q", got, want)
	}
}

func TestDetectShellFromFile_NoExtension(t *testing.T) {
	got := DetectShellFromFile("Makefile")
	want := "bash"
	if runtime.GOOS == "windows" {
		want = "powershell"
	}
	if got != want {
		t.Errorf("DetectShellFromFile(Makefile) = %q, want %q", got, want)
	}
}

func TestDefaultShell(t *testing.T) {
	got := DefaultShell()
	if runtime.GOOS == "windows" {
		if got != "powershell" {
			t.Errorf("DefaultShell() = %q, want %q", got, "powershell")
		}
	} else {
		if got != "bash" {
			t.Errorf("DefaultShell() = %q, want %q", got, "bash")
		}
	}
}
