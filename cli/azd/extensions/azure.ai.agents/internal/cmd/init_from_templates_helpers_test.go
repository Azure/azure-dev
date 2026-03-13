// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		expected string
	}{
		{
			name:     "agent.yaml suffix",
			source:   "https://github.com/org/repo/blob/main/samples/echo-agent/agent.yaml",
			expected: TemplateTypeAgent,
		},
		{
			name:     "agent.manifest.yaml suffix",
			source:   "https://github.com/org/repo/blob/main/samples/echo-agent/agent.manifest.yaml",
			expected: TemplateTypeAgent,
		},
		{
			name:     "bare agent.yaml",
			source:   "agent.yaml",
			expected: TemplateTypeAgent,
		},
		{
			name:     "bare agent.manifest.yaml",
			source:   "agent.manifest.yaml",
			expected: TemplateTypeAgent,
		},
		{
			name:     "case insensitive agent.yaml",
			source:   "https://github.com/org/repo/blob/main/Agent.YAML",
			expected: TemplateTypeAgent,
		},
		{
			name:     "case insensitive agent.manifest.yaml",
			source:   "https://github.com/org/repo/blob/main/Agent.Manifest.YAML",
			expected: TemplateTypeAgent,
		},
		{
			name:     "github repo slug",
			source:   "Azure-Samples/my-agent-template",
			expected: TemplateTypeAzd,
		},
		{
			name:     "github repo URL",
			source:   "https://github.com/Azure-Samples/my-agent-template",
			expected: TemplateTypeAzd,
		},
		{
			name:     "empty source",
			source:   "",
			expected: TemplateTypeAzd,
		},
		{
			name:     "yaml file that is not agent.yaml",
			source:   "https://github.com/org/repo/blob/main/config.yaml",
			expected: TemplateTypeAzd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			template := &AgentTemplate{Source: tt.source}
			require.Equal(t, tt.expected, template.EffectiveType())
		})
	}
}

func TestFetchAgentTemplates(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		templates := []AgentTemplate{
			{
				Title:     "Echo Agent",
				Language:  "python",
				Framework: "Agent Framework",
				Source:    "https://github.com/org/repo/blob/main/echo-agent/agent.yaml",
			},
			{
				Title:     "Calculator Agent",
				Language:  "csharp",
				Framework: "LangGraph",
				Source:    "Azure-Samples/calculator-agent",
			},
		}

		data, err := json.Marshal(templates)
		require.NoError(t, err)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}))
		defer server.Close()

		// Use a custom URL by overriding the HTTP client to redirect
		result, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, "Echo Agent", result[0].Title)
		require.Equal(t, "python", result[0].Language)
		require.Equal(t, "Calculator Agent", result[1].Title)
		require.Equal(t, "csharp", result[1].Language)
	})

	t.Run("HTTP error", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		_, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "HTTP 500")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()

		_, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse agent templates")
	})

	t.Run("empty array", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer server.Close()

		result, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.NoError(t, err)
		require.Empty(t, result)
	})
}

// fetchAgentTemplatesFromURL is a test helper that fetches templates from a custom URL.
func fetchAgentTemplatesFromURL(
	ctx context.Context,
	httpClient *http.Client,
	url string,
) ([]AgentTemplate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch agent templates: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var templates []AgentTemplate
	if err := json.Unmarshal(body, &templates); err != nil {
		return nil, fmt.Errorf("failed to parse agent templates: %w", err)
	}

	return templates, nil
}

func TestFindAgentManifest(t *testing.T) {
	t.Parallel()

	t.Run("finds agent.yaml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifestPath := filepath.Join(dir, "agent.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte("name: test"), 0600))

		found, err := findAgentManifest(dir)
		require.NoError(t, err)
		require.Equal(t, manifestPath, found)
	})

	t.Run("finds agent.manifest.yaml at root", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifestPath := filepath.Join(dir, "agent.manifest.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte("name: test"), 0600))

		found, err := findAgentManifest(dir)
		require.NoError(t, err)
		require.Equal(t, manifestPath, found)
	})

	t.Run("finds agent.yaml in subdirectory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		subDir := filepath.Join(dir, "src", "my-agent")
		require.NoError(t, os.MkdirAll(subDir, 0700))
		manifestPath := filepath.Join(subDir, "agent.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte("name: test"), 0600))

		found, err := findAgentManifest(dir)
		require.NoError(t, err)
		require.Equal(t, manifestPath, found)
	})

	t.Run("returns empty when no manifest exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Create some other files
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: test"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("key: val"), 0600))

		found, err := findAgentManifest(dir)
		require.NoError(t, err)
		require.Empty(t, found)
	})

	t.Run("ignores non-agent yaml files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: test"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("key: val"), 0600))

		found, err := findAgentManifest(dir)
		require.NoError(t, err)
		require.Empty(t, found)
	})
}
