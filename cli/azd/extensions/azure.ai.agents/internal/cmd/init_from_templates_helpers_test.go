// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
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

func TestIsFeatured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tags     []string
		expected bool
	}{
		{name: "tagged featured", tags: []string{"featured", "Responses Protocol"}, expected: true},
		{name: "not tagged featured", tags: []string{"MCP", "Responses Protocol"}, expected: false},
		{name: "nil tags", tags: nil, expected: false},
		{name: "empty tags", tags: []string{}, expected: false},
		{name: "featured only", tags: []string{"featured"}, expected: true},
		{name: "example tag is not featured", tags: []string{"example"}, expected: false},
		{name: "template tag is not featured", tags: []string{"template"}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl := &AgentTemplate{ExtensionTags: tt.tags}
			require.Equal(t, tt.expected, tmpl.isFeatured())
		})
	}
}

func TestIsRecommended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tags     []string
		expected bool
	}{
		{name: "tagged recommended", tags: []string{"featured", "recommended"}, expected: true},
		{name: "not tagged recommended", tags: []string{"featured"}, expected: false},
		{name: "nil tags", tags: nil, expected: false},
		{name: "empty tags", tags: []string{}, expected: false},
		{name: "recommended without featured", tags: []string{"recommended"}, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl := &AgentTemplate{ExtensionTags: tt.tags}
			require.Equal(t, tt.expected, tmpl.isRecommended())
		})
	}
}

func TestPartitionFeatured(t *testing.T) {
	t.Parallel()

	templates := []AgentTemplate{
		{Title: "MCP Tools Agent", ExtensionTags: []string{"MCP"}},
		{Title: "Basic Agent", ExtensionTags: []string{"featured"}},
		{Title: "Workflow Agent", ExtensionTags: []string{"workflows"}},
		{Title: "Hello World", ExtensionTags: []string{"featured"}},
	}

	featured, rest := partitionFeatured(templates)

	require.Len(t, featured, 2)
	require.Equal(t, "Basic Agent", featured[0].Title)
	require.Equal(t, "Hello World", featured[1].Title)

	require.Len(t, rest, 2)
	require.Equal(t, "MCP Tools Agent", rest[0].Title)
	require.Equal(t, "Workflow Agent", rest[1].Title)
}

func TestPartitionFeaturedAllFeatured(t *testing.T) {
	t.Parallel()

	templates := []AgentTemplate{
		{Title: "B Agent", ExtensionTags: []string{"featured"}},
		{Title: "A Agent", ExtensionTags: []string{"featured"}},
	}

	featured, rest := partitionFeatured(templates)

	require.Len(t, featured, 2)
	require.Equal(t, "A Agent", featured[0].Title)
	require.Equal(t, "B Agent", featured[1].Title)
	require.Empty(t, rest)
}

func TestPartitionFeaturedEmpty(t *testing.T) {
	t.Parallel()

	featured, rest := partitionFeatured(nil)
	require.Empty(t, featured)
	require.Empty(t, rest)

	featured2, rest2 := partitionFeatured([]AgentTemplate{})
	require.Empty(t, featured2)
	require.Empty(t, rest2)
}

func TestPartitionFeaturedNoneFeatured(t *testing.T) {
	t.Parallel()

	templates := []AgentTemplate{
		{Title: "MCP Tools Agent", ExtensionTags: []string{"MCP"}},
		{Title: "Workflow Agent", ExtensionTags: []string{"workflows"}},
	}

	featured, rest := partitionFeatured(templates)

	require.Empty(t, featured)
	require.Len(t, rest, 2)
}

func TestFindRecommendedIndex(t *testing.T) {
	t.Parallel()

	t.Run("finds recommended tag", func(t *testing.T) {
		t.Parallel()
		templates := []AgentTemplate{
			{Title: "Hello World", ExtensionTags: []string{"featured"}},
			{Title: "Basic Agent", ExtensionTags: []string{"featured", "recommended"}},
			{Title: "MCP Agent", ExtensionTags: []string{"featured"}},
		}
		require.Equal(t, int32(1), findRecommendedIndex(templates))
	})

	t.Run("returns first when multiple recommended", func(t *testing.T) {
		t.Parallel()
		templates := []AgentTemplate{
			{Title: "Hello World", ExtensionTags: []string{"featured"}},
			{Title: "Agent A", ExtensionTags: []string{"featured", "recommended"}},
			{Title: "Agent B", ExtensionTags: []string{"featured", "recommended"}},
		}
		require.Equal(t, int32(1), findRecommendedIndex(templates))
	})

	t.Run("returns 0 when no recommended tag", func(t *testing.T) {
		t.Parallel()
		templates := []AgentTemplate{
			{Title: "Hello World", ExtensionTags: []string{"featured"}},
			{Title: "Basic Agent", ExtensionTags: []string{"featured"}},
		}
		require.Equal(t, int32(0), findRecommendedIndex(templates))
	})

	t.Run("returns 0 for empty list", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, int32(0), findRecommendedIndex(nil))
		require.Equal(t, int32(0), findRecommendedIndex([]AgentTemplate{}))
	})
}

func TestFetchAgentTemplates(t *testing.T) {
	t.Parallel()

	t.Run("success filters by templateType", func(t *testing.T) {
		t.Parallel()

		// Manifest mixes gallery entries (no templateType / wrong templateType)
		// with agent-init entries. Only the latter should survive.
		manifest := []map[string]any{
			{
				"title":              "Echo Agent",
				"languages":          []string{"python"},
				"extensionFramework": "Agent Framework",
				"source":             "https://github.com/org/repo/blob/main/echo-agent/agent.yaml",
				"templateType":       "extension.ai.agent",
			},
			{
				"title":              "Calculator Agent",
				"languages":          []string{"dotnetCsharp"},
				"extensionFramework": "LangGraph",
				"source":             "Azure-Samples/calculator-agent",
				"templateType":       "extension.ai.agent",
			},
			{
				"title":     "Some gallery template",
				"languages": []string{"python"},
				"source":    "Azure-Samples/some-template",
				// no templateType -> standard awesome-azd gallery entry
			},
			{
				"title":        "Future extension category",
				"languages":    []string{"python"},
				"source":       "Azure-Samples/some-other-extension",
				"templateType": "extension.something.else",
			},
		}

		data, err := json.Marshal(manifest)
		require.NoError(t, err)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}))
		defer server.Close()

		result, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, "Echo Agent", result[0].Title)
		require.Equal(t, []string{"python"}, result[0].Languages)
		require.Equal(t, "Agent Framework", result[0].ExtensionFramework)
		require.Equal(t, "extension.ai.agent", result[0].TemplateType)
		require.Equal(t, "Calculator Agent", result[1].Title)
		require.Equal(t, []string{"dotnetCsharp"}, result[1].Languages)
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

	t.Run("manifest with only gallery entries returns error", func(t *testing.T) {
		t.Parallel()

		manifest := []map[string]any{
			{
				"title":     "Some gallery template",
				"languages": []string{"python"},
				"source":    "Azure-Samples/some-template",
			},
		}
		data, err := json.Marshal(manifest)
		require.NoError(t, err)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}))
		defer server.Close()

		result, err := fetchAgentTemplatesFromURL(t.Context(), server.Client(), server.URL)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "extension.ai.agent")
		require.Contains(t, err.Error(), "1 entries")
	})
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

func TestDirIsEmpty(t *testing.T) {
	t.Parallel()

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		empty, err := dirIsEmpty(dir)
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("directory with files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print()"), 0600))

		empty, err := dirIsEmpty(dir)
		require.NoError(t, err)
		require.False(t, empty)
	})

	t.Run("directory with only subdirectories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0700))

		empty, err := dirIsEmpty(dir)
		require.NoError(t, err)
		require.False(t, empty)
	})
}

func TestDetectLocalManifest(t *testing.T) {
	t.Parallel()

	// Valid agent manifest content (has template with kind + name)
	validManifest := `name: test-agent
template:
  kind: hosted
  name: test-agent
  protocols:
    - protocol: responses
      version: v1
`

	t.Run("no manifest files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print()"), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("valid agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), result)
	})

	t.Run("valid agent.manifest.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.manifest.yaml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.manifest.yaml"), result)
	})

	t.Run("both files prefers agent.manifest.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(validManifest), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.manifest.yaml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.manifest.yaml"), result)
	})

	t.Run("does not search subdirectories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		subDir := filepath.Join(dir, "src")
		require.NoError(t, os.MkdirAll(subDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(subDir, "agent.yaml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("invalid YAML content is skipped", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("not: valid: yaml: ["), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("YAML without template is skipped", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("foo: bar\n"), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("falls back to agent.yaml when manifest.yaml is invalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.manifest.yaml"), []byte("foo: bar\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), result)
	})

	t.Run("detects agent.yml variant", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yml"), result)
	})

	t.Run("detects agent.manifest.yml variant", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.manifest.yml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.manifest.yml"), result)
	})

	t.Run("prefers yaml over yml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(validManifest), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yml"), []byte(validManifest), 0600))

		result, err := detectLocalManifest(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), result)
	})
}
