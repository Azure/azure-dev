// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

func TestExtractTargetFrameworkVersion(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"net8.0", `<TargetFramework>net8.0</TargetFramework>`, 8},
		{"net9.0", `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`, 9},
		{"net10.0", `<Project><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`, 10},
		{"no target framework", `<Project><PropertyGroup></PropertyGroup></Project>`, 0},
		{"empty", "", 0},
		{"netstandard", `<TargetFramework>netstandard2.0</TargetFramework>`, 0},
		{"netcoreapp3.1", `<TargetFramework>netcoreapp3.1</TargetFramework>`, 0},
		{"net8.0-windows", `<TargetFramework>net8.0-windows</TargetFramework>`, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTargetFrameworkVersion(tt.content)
			if got != tt.want {
				t.Errorf("extractTargetFrameworkVersion() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidateDotnetRuntimeVsCsproj_Mismatch(t *testing.T) {
	dir := t.TempDir()
	csproj := `<Project><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`
	err := os.WriteFile(filepath.Join(dir, "MyAgent.csproj"), []byte(csproj), 0600)
	if err != nil {
		t.Fatal(err)
	}

	output, _ := captureStdout(t, func() error {
		validateDotnetRuntimeVsCsproj(dir, "dotnet_9")
		return nil
	})
	if !strings.Contains(output, "ERROR") {
		t.Errorf("expected ERROR in output, got: %s", output)
	}
}

func TestValidateDotnetRuntimeVsCsproj_Match(t *testing.T) {
	dir := t.TempDir()
	csproj := `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`
	err := os.WriteFile(filepath.Join(dir, "MyAgent.csproj"), []byte(csproj), 0600)
	if err != nil {
		t.Fatal(err)
	}

	output, _ := captureStdout(t, func() error {
		validateDotnetRuntimeVsCsproj(dir, "dotnet_9")
		return nil
	})
	if !strings.Contains(output, "OK") {
		t.Errorf("expected OK in output, got: %s", output)
	}
}

func TestValidateDotnetRuntimeVsCsproj_HigherRuntime(t *testing.T) {
	dir := t.TempDir()
	csproj := `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`
	err := os.WriteFile(filepath.Join(dir, "MyAgent.csproj"), []byte(csproj), 0600)
	if err != nil {
		t.Fatal(err)
	}

	// dotnet_10 runtime with net9.0 target — should pass (OK), no error
	validateDotnetRuntimeVsCsproj(dir, "dotnet_10")
}

func TestValidateDotnetRuntimeVsCsproj_NoCsproj(t *testing.T) {
	dir := t.TempDir()
	// No .csproj file — should skip silently without panic
	validateDotnetRuntimeVsCsproj(dir, "dotnet_9")
}

func TestValidateDotnetRuntimeVsCsproj_UnreadableDir(t *testing.T) {
	// Non-existent directory — should print ERROR without panic
	validateDotnetRuntimeVsCsproj("/nonexistent/path/xyz", "dotnet_9")
}

func TestValidateDotnetRuntimeVsCsproj_PythonSkipped(t *testing.T) {
	// Python runtime should be skipped entirely
	validateDotnetRuntimeVsCsproj("/any/path", "python_3_12")
}

func TestValidatePostInit_NilCodeConfig(t *testing.T) {
	// Should not panic with nil codeConfig
	validatePostInit("/any/path", nil)
}

func TestValidatePostInit_DotnetTriggersValidation(t *testing.T) {
	dir := t.TempDir()
	csproj := `<Project><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`
	err := os.WriteFile(filepath.Join(dir, "Test.csproj"), []byte(csproj), 0600)
	if err != nil {
		t.Fatal(err)
	}

	// Should trigger validation and print ERROR (non-blocking)
	codeConfig := &agent_yaml.CodeConfiguration{
		Runtime:    "dotnet_9",
		EntryPoint: "Test.dll",
	}
	validatePostInit(dir, codeConfig)
}

func TestValidatePostInit_PythonSkipsValidation(t *testing.T) {
	// Python code config should not trigger dotnet validation
	codeConfig := &agent_yaml.CodeConfiguration{
		Runtime:    "python_3_12",
		EntryPoint: "main.py",
	}
	validatePostInit("/any/path", codeConfig)
}
