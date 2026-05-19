// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

func TestCapabilityPromptChoicesMatchValidCapabilities(t *testing.T) {
	choices := capabilityPromptChoices()
	require.Len(t, choices, len(extensions.ValidCapabilities))

	for i, capability := range extensions.ValidCapabilities {
		require.Equal(t, string(capability), choices[i].Value)
		require.NotEmpty(t, choices[i].Label)
	}
}

func TestCapabilityLabel(t *testing.T) {
	require.Equal(t, "Custom Commands", capabilityLabel(extensions.CustomCommandCapability))
	require.Equal(t, "MCP Server", capabilityLabel(extensions.McpServerCapability))
	require.Equal(t, "Provisioning Provider", capabilityLabel(extensions.ProvisioningProviderCapability))
}

func TestNamespaceCommandPath(t *testing.T) {
	tests := []struct {
		namespace string
		want      string
	}{
		{namespace: "demo", want: "demo"},
		{namespace: "ai.project", want: "ai project"},
		{namespace: "company.team.tool", want: "company team tool"},
		{namespace: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			assert.Equal(t, tt.want, namespaceCommandPath(tt.namespace))
		})
	}
}

func TestFormatUsage(t *testing.T) {
	assert.Equal(t, "azd demo <command> [options]", formatUsage("demo"))
	assert.Equal(t, "azd ai project <command> [options]", formatUsage("ai.project"))
}

func TestValidateExtensionMetadata(t *testing.T) {
	tests := []struct {
		name                string
		schema              *models.ExtensionSchema
		wantWarningCount    int
		wantWarningContains []string
		wantErrorCount      int
		wantErrorContains   []string
	}{
		{
			name: "complete schema produces no warnings or errors",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Namespace:    "test",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			},
		},
		{
			name:             "empty schema reports all required-field errors",
			schema:           &models.ExtensionSchema{},
			wantWarningCount: 1,
			wantWarningContains: []string{
				"Missing 'usage' field in extension.yaml",
			},
			wantErrorCount: 5,
			wantErrorContains: []string{
				"Missing required field: id",
				"Missing required field: version",
				"Missing required field: capabilities",
				"Missing required field: displayName",
				"Missing required field: description",
			},
		},
		{
			name: "service target provider without providers emits warning",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Namespace:    "test",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
			},
			wantWarningCount: 1,
			wantWarningContains: []string{
				"Missing 'providers' field in extension.yaml",
				"service-target-provider",
			},
		},
		{
			name: "custom commands without namespace is a fatal error",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			},
			wantErrorCount: 1,
			wantErrorContains: []string{
				"Missing 'namespace' field in extension.yaml",
				"custom-commands",
			},
		},
		{
			name: "extension pack without capabilities or usage is valid",
			schema: &models.ExtensionSchema{
				Id:          "test.pack",
				Version:     "0.0.1",
				DisplayName: "Test Pack",
				Description: "A test extension pack",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.extension", Version: "latest"},
				},
			},
		},
		{
			name: "extension with dependencies and executable metadata still requires capabilities",
			schema: &models.ExtensionSchema{
				Id:          "test.extension",
				Version:     "0.0.1",
				DisplayName: "Test Extension",
				Description: "A test extension",
				Namespace:   "test",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.dependency", Version: "latest"},
				},
			},
			wantWarningCount: 1,
			wantWarningContains: []string{
				"Missing 'usage' field in extension.yaml",
			},
			wantErrorCount: 1,
			wantErrorContains: []string{
				"Missing required field: capabilities",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, errs := validateExtensionMetadata(tt.schema)
			assert.Len(t, warnings, tt.wantWarningCount)
			for _, want := range tt.wantWarningContains {
				assert.True(
					t,
					slicesContainSubstring(warnings, want),
					"expected warning containing %q in %v", want, warnings,
				)
			}

			assert.Len(t, errs, tt.wantErrorCount)
			for _, want := range tt.wantErrorContains {
				assert.True(
					t,
					slicesContainSubstring(errs, want),
					"expected error containing %q in %v", want, errs,
				)
			}
		})
	}
}

func TestAddOrUpdateExtensionExtensionPack(t *testing.T) {
	registry := &extensions.Registry{}
	schema := &models.ExtensionSchema{
		Id:          "test.pack",
		Version:     "0.0.1",
		DisplayName: "Test Pack",
		Description: "A test extension pack",
		Dependencies: []extensions.ExtensionDependency{
			{Id: "test.extension", Version: "latest"},
		},
	}

	addOrUpdateExtension(registry, schema, map[string]extensions.ExtensionArtifact{})

	require.Len(t, registry.Extensions, 1)
	extension := registry.Extensions[0]
	assert.Equal(t, "test.pack", extension.Id)
	assert.Empty(t, extension.Namespace)
	require.Len(t, extension.Versions, 1)

	version := extension.Versions[0]
	assert.Equal(t, "0.0.1", version.Version)
	assert.Empty(t, version.Capabilities)
	assert.Empty(t, version.Artifacts)
	assert.Equal(t, schema.Dependencies, version.Dependencies)
}

func TestIsExtensionPack(t *testing.T) {
	tests := []struct {
		name string
		in   *models.ExtensionSchema
		want bool
	}{
		{
			name: "dependency-only manifest is pack",
			in: &models.ExtensionSchema{
				Id: "test.pack",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.extension", Version: "latest"},
				},
			},
			want: true,
		},
		{
			name: "dependencies with capabilities is executable extension",
			in: &models.ExtensionSchema{
				Id:           "test.extension",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.dependency", Version: "latest"},
				},
			},
		},
		{
			name: "dependencies with namespace is executable extension",
			in: &models.ExtensionSchema{
				Id:        "test.extension",
				Namespace: "test",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.dependency", Version: "latest"},
				},
			},
		},
		{
			name: "dependencies with language is executable extension",
			in: &models.ExtensionSchema{
				Id:       "test.extension",
				Language: "go",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.dependency", Version: "latest"},
				},
			},
		},
		{
			name: "dependencies with entry point is executable extension",
			in: &models.ExtensionSchema{
				Id:         "test.extension",
				EntryPoint: "test-extension",
				Dependencies: []extensions.ExtensionDependency{
					{Id: "test.dependency", Version: "latest"},
				},
			},
		},
		{
			name: "no dependencies is not pack",
			in:   &models.ExtensionSchema{Id: "test.extension"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isExtensionPack(tt.in))
		})
	}
}

func TestValidatePublishOptionsExtensionPack(t *testing.T) {
	tests := []struct {
		name          string
		extensionPack bool
		flags         *publishFlags
		wantErr       string
	}{
		{
			name:          "extension pack without artifact flags is valid",
			extensionPack: true,
			flags:         &publishFlags{},
		},
		{
			name:          "extension pack rejects repository",
			extensionPack: true,
			flags:         &publishFlags{repository: "owner/repo"},
			wantErr:       "omit --repo",
		},
		{
			name:          "extension pack rejects artifacts",
			extensionPack: true,
			flags:         &publishFlags{artifacts: []string{"./artifacts/*.zip"}},
			wantErr:       "omit --artifacts",
		},
		{
			name:          "executable extension allows repository and artifacts",
			extensionPack: false,
			flags: &publishFlags{
				repository: "owner/repo",
				artifacts:  []string{"./artifacts/*.zip"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePublishOptions(tt.extensionPack, tt.flags)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePublishAssets(t *testing.T) {
	tests := []struct {
		name          string
		extensionPack bool
		assetCount    int
		wantState     ux.TaskState
		wantErr       string
	}{
		{
			name:       "executable extension with assets succeeds",
			assetCount: 1,
			wantState:  ux.Success,
		},
		{
			name:          "extension pack without assets skips",
			extensionPack: true,
			wantState:     ux.Skipped,
		},
		{
			name:      "executable extension without assets errors",
			wantState: ux.Error,
			wantErr:   "no artifacts found for this extension version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := validatePublishAssets(tt.extensionPack, tt.assetCount)
			assert.Equal(t, tt.wantState, state)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestCollectExtensionMetadataFromFlagsTags(t *testing.T) {
	metadata, err := collectExtensionMetadataFromFlags(&initFlags{
		id:           "test.extension",
		name:         "Test Extension",
		capabilities: []string{string(extensions.CustomCommandCapability)},
		language:     "go",
		tags:         []string{"alpha, beta", "gamma"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, metadata.Tags)
}

func TestCollectExtensionMetadataFromFlagsInvalidTags(t *testing.T) {
	_, err := collectExtensionMetadataFromFlags(&initFlags{
		id:           "test.extension",
		name:         "Test Extension",
		capabilities: []string{string(extensions.CustomCommandCapability)},
		language:     "go",
		tags:         []string{"valid", "ba\nd"},
	})

	require.ErrorContains(t, err, "control characters")
}

func TestValidateExtensionNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		wantErr   bool
	}{
		{name: "single segment", namespace: "demo"},
		{name: "two segments", namespace: "ai.project"},
		{name: "three segments with digits", namespace: "company1.team2.tool3"},
		{name: "hyphenated segment", namespace: "coding-agent"},
		{name: "hyphenated nested segment", namespace: "azure.coding-agent"},
		{name: "empty", namespace: "", wantErr: true},
		{name: "consecutive dots", namespace: "a..b", wantErr: true},
		{name: "leading dot", namespace: ".demo", wantErr: true},
		{name: "trailing dot", namespace: "demo.", wantErr: true},
		{name: "uppercase", namespace: "Demo", wantErr: true},
		{name: "underscore", namespace: "demo.tool_name", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateExtensionNamespace(tt.namespace)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseTags(t *testing.T) {
	tags, err := parseTags("alpha, beta,,gamma")
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, tags)

	// Boundary: exactly maxExtensionTags must succeed.
	boundary := make([]string, maxExtensionTags)
	for i := range maxExtensionTags {
		boundary[i] = fmt.Sprintf("tag%d", i)
	}
	tags, err = parseTags(strings.Join(boundary, ","))
	require.NoError(t, err)
	assert.Len(t, tags, maxExtensionTags)

	_, err = parseTags(strings.Join(append(boundary, "overflow"), ","))
	require.ErrorContains(t, err, "too many tags")

	_, err = parseTags(strings.Repeat("a", maxExtensionTagLength+1))
	require.ErrorContains(t, err, "too long")

	_, err = parseTags("valid,ba\nd")
	require.ErrorContains(t, err, "control characters")
}

func TestWriteCollectedWarnings(t *testing.T) {
	var buf bytes.Buffer
	writeCollectedWarnings(&buf, []string{"first warning", "second warning"})

	output := buf.String()
	assert.Contains(t, output, "Validation warnings:")
	assert.NotContains(t, output, "(!) Warning")
	assert.Contains(t, output, "  - first warning")
	assert.Contains(t, output, "  - second warning")
}

func TestValidationWarningSummary(t *testing.T) {
	assert.Equal(t, "1 validation warning", validationWarningSummary([]string{"first"}))
	assert.Equal(t, "2 validation warnings", validationWarningSummary([]string{"first", "second"}))
}

func TestSubprocessErrorTail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace only", in: "   \n\n\t\n", want: ""},
		{
			name: "ERROR line wins over later content",
			in: "Installing extension\n" +
				"ERROR: namespace 'test.ext' conflicts with 'ext.agent'\n" +
				"Suggestion: uninstall first",
			want: ": namespace 'test.ext' conflicts with 'ext.agent'",
		},
		{
			name: "empty ERROR line falls back to later content",
			in:   "ERROR:\nSuggestion: retry",
			want: ": Suggestion: retry",
		},
		{
			name: "falls back to last non-empty line",
			in:   "first\nsecond\n\n",
			want: ": second",
		},
		{
			name: "strips ANSI escapes",
			in:   "\x1b[31mERROR:\x1b[0m boom",
			want: ": boom",
		},
		{
			name: "strips OSC hyperlinks",
			in:   "\x1b]8;;file:///tmp/out\x07/tmp/out\x1b]8;;\x07",
			want: ": /tmp/out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, subprocessErrorTail([]byte(tt.in)))
		})
	}
}

func TestIsDirNonEmpty(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		nonEmpty, err := isDirNonEmpty(dir)
		require.NoError(t, err)
		assert.False(t, nonEmpty)
	})

	t.Run("non-empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o600))

		nonEmpty, err := isDirNonEmpty(dir)
		require.NoError(t, err)
		assert.True(t, nonEmpty)
	})

	t.Run("missing path", func(t *testing.T) {
		t.Parallel()
		_, err := isDirNonEmpty(filepath.Join(t.TempDir(), "does-not-exist"))
		assert.Error(t, err)
	})
}

func TestTemplateGoStringQuotesDescription(t *testing.T) {
	t.Parallel()

	const tmplSrc = `Short: {{strconvQuote .Description}},`
	tmpl, err := template.New("test").Funcs(templateFuncs).Parse(tmplSrc)
	require.NoError(t, err)

	tests := []struct {
		name        string
		description string
		want        string
	}{
		{name: "plain", description: "A test extension", want: `Short: "A test extension",`},
		{
			name:        "embedded double quotes",
			description: `says "hi"`,
			want:        `Short: "says \"hi\"",`,
		},
		{
			name:        "backslash",
			description: `a\b`,
			want:        `Short: "a\\b",`,
		},
		{
			name:        "newline and tab",
			description: "line1\nline2\t!",
			want:        `Short: "line1\nline2\t!",`,
		},
		{
			name:        "trailing backslash injection attempt",
			description: `boom",}{`,
			want:        `Short: "boom\",}{",`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, tmpl.Execute(&buf, struct{ Description string }{tt.description}))
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func slicesContainSubstring(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}

	return false
}
