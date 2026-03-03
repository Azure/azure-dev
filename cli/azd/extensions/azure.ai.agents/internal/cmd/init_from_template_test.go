// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTemplateUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		expectedUrl    string
		expectedBranch string
	}{
		{
			name:           "full GitHub URL",
			input:          "https://github.com/owner/repo",
			expectedUrl:    "https://github.com/owner/repo",
			expectedBranch: "",
		},
		{
			name:           "full GitHub URL with trailing slash",
			input:          "https://github.com/owner/repo/",
			expectedUrl:    "https://github.com/owner/repo/",
			expectedBranch: "",
		},
		{
			name:           "GitHub URL with tree/branch",
			input:          "https://github.com/owner/repo/tree/main",
			expectedUrl:    "https://github.com/owner/repo",
			expectedBranch: "main",
		},
		{
			name:           "GitHub URL with tree/branch trailing slash",
			input:          "https://github.com/owner/repo/tree/main/",
			expectedUrl:    "https://github.com/owner/repo",
			expectedBranch: "main",
		},
		{
			name:           "GitHub URL with tree/feature-branch",
			input:          "https://github.com/owner/repo/tree/feature/my-branch",
			expectedUrl:    "https://github.com/owner/repo",
			expectedBranch: "feature/my-branch",
		},
		{
			name:           "owner/repo format",
			input:          "myorg/my-template",
			expectedUrl:    "https://github.com/myorg/my-template",
			expectedBranch: "",
		},
		{
			name:           "bare repo name",
			input:          "azd-ai-starter-basic",
			expectedUrl:    "https://github.com/Azure-Samples/azd-ai-starter-basic",
			expectedBranch: "",
		},
		{
			name:           "http URL",
			input:          "http://github.com/owner/repo",
			expectedUrl:    "http://github.com/owner/repo",
			expectedBranch: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, branch := resolveTemplateUrl(tt.input)
			if url != tt.expectedUrl {
				t.Errorf("resolveTemplateUrl(%q) url = %q, want %q", tt.input, url, tt.expectedUrl)
			}
			if branch != tt.expectedBranch {
				t.Errorf("resolveTemplateUrl(%q) branch = %q, want %q", tt.input, branch, tt.expectedBranch)
			}
		})
	}
}

func TestExtractRepoSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full HTTPS URL",
			input:    "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS URL with trailing slash",
			input:    "https://github.com/owner/repo/",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS URL with extra path",
			input:    "https://github.com/owner/repo/tree/main",
			expected: "owner/repo",
		},
		{
			name:     "HTTP URL",
			input:    "http://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "non-GitHub URL",
			input:    "https://gitlab.com/owner/repo",
			expected: "",
		},
		{
			name:     "just owner no repo",
			input:    "https://github.com/owner",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRepoSlug(tt.input)
			if result != tt.expected {
				t.Errorf("extractRepoSlug(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFindAgentYaml(t *testing.T) {
	t.Parallel()

	t.Run("finds agent.yaml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		agentPath := filepath.Join(dir, "agent.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.yml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		agentPath := filepath.Join(dir, "agent.yml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.yaml in src subdirectory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src", "my-agent")
		os.MkdirAll(srcDir, 0755)
		agentPath := filepath.Join(srcDir, "agent.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.yaml in nested directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		nestedDir := filepath.Join(dir, "deep", "nested")
		os.MkdirAll(nestedDir, 0755)
		agentPath := filepath.Join(nestedDir, "agent.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("prefers root over src subdirectory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Create both root and src/agent.yaml
		rootPath := filepath.Join(dir, "agent.yaml")
		os.WriteFile(rootPath, []byte("name: root"), 0644)

		srcDir := filepath.Join(dir, "src", "my-agent")
		os.MkdirAll(srcDir, 0755)
		os.WriteFile(filepath.Join(srcDir, "agent.yaml"), []byte("name: nested"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != rootPath {
			t.Errorf("findAgentYaml() = %q, want root %q", result, rootPath)
		}
	})

	t.Run("returns error when not found", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		_, err := findAgentYaml(dir)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("finds agent.manifest.yaml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		agentPath := filepath.Join(dir, "agent.manifest.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.manifest.yml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		agentPath := filepath.Join(dir, "agent.manifest.yml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.manifest.yaml in src subdirectory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src", "my-agent")
		os.MkdirAll(srcDir, 0755)
		agentPath := filepath.Join(srcDir, "agent.manifest.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("finds agent.manifest.yaml in nested directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		nestedDir := filepath.Join(dir, "deep", "nested")
		os.MkdirAll(nestedDir, 0755)
		agentPath := filepath.Join(nestedDir, "agent.manifest.yaml")
		os.WriteFile(agentPath, []byte("name: test"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want %q", result, agentPath)
		}
	})

	t.Run("prefers agent.yaml over agent.manifest.yaml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		manifestPath := filepath.Join(dir, "agent.manifest.yaml")
		os.WriteFile(manifestPath, []byte("name: manifest"), 0644)

		agentPath := filepath.Join(dir, "agent.yaml")
		os.WriteFile(agentPath, []byte("name: agent"), 0644)

		result, err := findAgentYaml(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != agentPath {
			t.Errorf("findAgentYaml() = %q, want agent.yaml %q", result, agentPath)
		}
	})
}
