// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
)

func TestResolveInitHarness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		harnessFlag    string
		impliedHarness string
		expected       string
		expectErr      bool
	}{
		{
			name:     "no flag and no implied harness scaffolds a plain prompt agent",
			expected: "",
		},
		{
			// A manifest's `harness:` block arrives here as an implied value.
			name:           "implied harness is honored",
			impliedHarness: agent_api.ManagedAgentHarnessGitHubCopilot,
			expected:       agent_api.ManagedAgentHarnessGitHubCopilot,
		},
		{
			name:        "explicit harness is accepted case-insensitively",
			harnessFlag: "GitHub_Copilot_Preview",
			expected:    agent_api.ManagedAgentHarnessGitHubCopilot,
		},
		{
			name:           "none opts out of an implied harness",
			harnessFlag:    " none ",
			impliedHarness: agent_api.ManagedAgentHarnessGitHubCopilot,
			expected:       "",
		},
		{
			name:           "explicit harness overrides a harness-less context",
			harnessFlag:    agent_api.ManagedAgentHarnessGitHubCopilot,
			impliedHarness: "",
			expected:       agent_api.ManagedAgentHarnessGitHubCopilot,
		},
		{
			name:        "unknown harness is rejected",
			harnessFlag: "bogus",
			expectErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness, err := resolveInitHarness(tc.harnessFlag, tc.impliedHarness)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, harness)
		})
	}
}

// TestWarnPromptAgentPreview verifies the preview callout renders its
// emphasized segment intact. It is unconditional: every prompt-agent init
// funnels through the one call site, so the notice reaches --kind prompt and
// manifest adoption.
func TestWarnPromptAgentPreview(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	warnPromptAgentPreview(buf)

	// The emphasized phrase is a separately colored segment, so assert
	// it survives concatenation intact rather than being split.
	require.Contains(t, buf.String(), "preview feature of the azd CLI experience")
}

func TestEffectiveType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		templateType string
		expected     string
	}{
		{
			name:     "legacy agent.yaml suffix is unsupported",
			source:   "https://github.com/org/repo/blob/main/samples/echo-agent/agent.yaml",
			expected: "",
		},
		{
			name:     "legacy agent.manifest.yaml suffix is unsupported",
			source:   "https://github.com/org/repo/blob/main/samples/echo-agent/agent.manifest.yaml",
			expected: "",
		},
		{
			name:     "bare agent.yaml is unsupported",
			source:   "agent.yaml",
			expected: "",
		},
		{
			name:     "bare agent.manifest.yaml is unsupported",
			source:   "agent.manifest.yaml",
			expected: "",
		},
		{
			name:     "case insensitive agent.yaml is unsupported",
			source:   "https://github.com/org/repo/blob/main/Agent.YAML",
			expected: "",
		},
		{
			name:     "case insensitive agent.manifest.yaml is unsupported",
			source:   "https://github.com/org/repo/blob/main/Agent.Manifest.YAML",
			expected: "",
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
			name:     "empty source is unsupported",
			source:   "",
			expected: "",
		},
		{
			name:     "unrecognized yaml file is unsupported",
			source:   "https://github.com/org/repo/blob/main/config.yaml",
			expected: "",
		},
		{
			name:         "azure.yaml with extension.ai.agent templateType",
			source:       "https://github.com/org/repo/blob/main/samples/basic/azure.yaml",
			templateType: "extension.ai.agent",
			expected:     TemplateTypeAzureYaml,
		},
		{
			name: "known catalog unified azure.yaml",
			source: "https://github.com/microsoft-foundry/foundry-samples/blob/main/" +
				"samples/python/hosted-agents/agent-framework/hello-world/azure.yaml",
			templateType: "extension.ai.agent",
			expected:     TemplateTypeAzureYaml,
		},
		{
			name:         "azure.yml with extension.ai.agent templateType",
			source:       "https://github.com/org/repo/blob/main/samples/basic/azure.yml",
			templateType: "extension.ai.agent",
			expected:     TemplateTypeAzureYaml,
		},
		{
			name:         "bare azure.yaml with extension.ai.agent templateType",
			source:       "azure.yaml",
			templateType: "extension.ai.agent",
			expected:     TemplateTypeAzureYaml,
		},
		{
			name:         "azure.yaml URL with query and fragment",
			source:       "https://github.com/org/repo/blob/main/samples/basic/azure.yaml?plain=1#L1",
			templateType: "extension.ai.agent",
			expected:     TemplateTypeAzureYaml,
		},
		{
			name:         "azure.yaml without extension.ai.agent templateType is unsupported",
			source:       "https://github.com/org/repo/blob/main/samples/basic/azure.yaml",
			templateType: "",
			expected:     "",
		},
		{
			name:         "azure.yaml with different templateType is unsupported",
			source:       "https://github.com/org/repo/blob/main/samples/basic/azure.yaml",
			templateType: "extension.something.else",
			expected:     "",
		},
		{
			name:     "github repo URL with git suffix",
			source:   "https://github.com/Azure-Samples/my-agent-template.git",
			expected: TemplateTypeAzd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			template := &AgentTemplate{Source: tt.source, TemplateType: tt.templateType}
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

func TestPromptInitMode_NoPromptNonEmptyDirUsesCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')\n"), 0600)
	require.NoError(t, err)

	mode, err := promptInitMode(t.Context(), nil, true)

	require.NoError(t, err)
	require.Equal(t, initModeFromCode, mode)
}

func TestPromptInitMode_NoPromptEmptyDirUsesTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	mode, err := promptInitMode(t.Context(), nil, true)

	require.NoError(t, err)
	require.Equal(t, initModeTemplate, mode)
}

func TestPromptInitMode_ShowsVoiceChoiceByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	prompts := &helpersPromptServer{selectIndex: 0}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)

	mode, err := promptInitMode(t.Context(), azdClient, false)

	require.NoError(t, err)
	require.Equal(t, initModeTemplate, mode)
	require.NotNil(t, prompts.lastSelect)
	require.Len(t, prompts.lastSelect.Options.Choices, 2)
	require.Equal(t, "Create a prompt voice agent", prompts.lastSelect.Options.Choices[1].Label)
}

func TestPromptInitMode_SelectsVoiceChoice(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	prompts := &helpersPromptServer{selectIndex: 1}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)

	mode, err := promptInitMode(t.Context(), azdClient, false)

	require.NoError(t, err)
	require.Equal(t, initModeVoice, mode)
	require.NotNil(t, prompts.lastSelect)
	require.Len(t, prompts.lastSelect.Options.Choices, 2)
	require.Equal(t, "Create a prompt voice agent", prompts.lastSelect.Options.Choices[1].Label)
}

func TestPromptInitMode_NonEmptyDirectoryChoices(t *testing.T) {
	for _, tt := range []struct {
		mode  string
		index int32
	}{
		{initModeFromCode, 0},
		{initModeTemplate, 1},
		{initModeVoice, 2},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')\n"), 0600))
			prompts := &helpersPromptServer{selectIndex: tt.index}
			client := newHelpersTestAzdClient(t, &helpersProjectServer{}, prompts)

			mode, err := promptInitMode(t.Context(), client, false)
			require.NoError(t, err)
			require.Equal(t, tt.mode, mode)
			require.NotNil(t, prompts.lastSelect)
			options := prompts.lastSelect.Options
			require.Len(t, options.Choices, 3)
			require.Equal(t, "Use the code in the current directory", options.Choices[0].Label)
			require.Equal(t, "Start new from a template", options.Choices[1].Label)
			require.Equal(t, "Create a prompt voice agent", options.Choices[2].Label)
			require.NotNil(t, options.SelectedIndex)
			require.Zero(t, *options.SelectedIndex)
		})
	}
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

		// Manifest mixes gallery entries, supported agent-init entries, and a
		// legacy agent manifest. Only supported agent-init sources survive.
		manifest := []map[string]any{
			{
				"title":              "Echo Agent",
				"languages":          []string{"python"},
				"extensionFramework": "Agent Framework",
				"source":             "https://github.com/org/repo/blob/main/echo-agent/azure.yaml",
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
			{
				"title":        "Legacy agent manifest",
				"languages":    []string{"python"},
				"source":       "https://github.com/org/repo/blob/main/legacy/agent.yaml",
				"templateType": "extension.ai.agent",
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

	t.Run("manifest with only legacy agent sources returns error", func(t *testing.T) {
		t.Parallel()

		manifest := []map[string]any{
			{
				"title":        "Legacy agent manifest",
				"languages":    []string{"python"},
				"source":       "https://github.com/org/repo/blob/main/legacy/agent.manifest.yaml",
				"templateType": "extension.ai.agent",
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
		require.Contains(t, err.Error(), "none used a supported azure.yaml or full repository source")
	})

	t.Run("catalog validation error redacts URL credentials", func(t *testing.T) {
		t.Parallel()

		manifest := []map[string]any{
			{
				"title":        "Legacy agent manifest",
				"source":       "agent.yaml",
				"templateType": "extension.ai.agent",
			},
		}
		data, err := json.Marshal(manifest)
		require.NoError(t, err)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}))
		defer server.Close()

		catalogURL := "http://catalog-user:catalog-password@" +
			strings.TrimPrefix(server.URL, "http://") + "/templates?sig=secret#fragment"
		_, err = fetchAgentTemplatesFromURL(t.Context(), server.Client(), catalogURL)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "catalog-user")
		require.NotContains(t, err.Error(), "catalog-password")
		require.NotContains(t, err.Error(), "sig=secret")
		require.NotContains(t, err.Error(), "fragment")
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
