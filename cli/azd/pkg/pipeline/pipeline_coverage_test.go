// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// generatePipelineDefinition — template rendering + file writing
// ---------------------------------------------------------------------------

func Test_generatePipelineDefinition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		props         projectProperties
		wantSubstr    []string // strings that MUST appear in generated YAML
		notWantSubstr []string // strings that must NOT appear
	}{
		{
			name: "github bicep federated",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
			},
			wantSubstr: []string{
				"main",
			},
		},
		{
			name: "github terraform federated adds AZURE_LOCATION and AZURE_ENV_NAME",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderTerraform,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
			},
			wantSubstr: []string{
				"AZURE_LOCATION",
				"AZURE_ENV_NAME",
			},
		},
		{
			name: "github terraform client-credentials adds AZURE_CLIENT_SECRET",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderTerraform,
				BranchName:    "main",
				AuthType:      AuthTypeClientCredentials,
			},
			wantSubstr: []string{
				"AZURE_CLIENT_SECRET",
				"AZURE_LOCATION",
				"AZURE_ENV_NAME",
			},
		},
		{
			name: "github with variables and secrets",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
				Variables:     []string{"MY_VAR"},
				Secrets:       []string{"MY_SECRET"},
			},
			wantSubstr: []string{
				"MY_VAR",
				"MY_SECRET",
			},
		},
		{
			name: "github with app host sets dotnet install",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
				HasAppHost:    true,
			},
			wantSubstr: []string{
				"dotnet",
			},
		},
		{
			name: "github with alpha features",
			props: projectProperties{
				CiProvider:            ciProviderGitHubActions,
				InfraProvider:         infraProviderBicep,
				BranchName:            "main",
				AuthType:              AuthTypeFederated,
				RequiredAlphaFeatures: []string{"feature1"},
			},
			wantSubstr: []string{
				"feature1",
			},
		},
		{
			name: "github with provider parameters adds env vars",
			props: projectProperties{
				CiProvider:    ciProviderGitHubActions,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
				providerParameters: []provisioning.Parameter{
					{
						Name:          "dbPass",
						Secret:        true,
						EnvVarMapping: []string{"DB_PASSWORD"},
					},
					{
						Name:          "region",
						Secret:        false,
						EnvVarMapping: []string{"CUSTOM_REGION"},
					},
				},
			},
			wantSubstr: []string{
				"DB_PASSWORD",
				"CUSTOM_REGION",
			},
		},
		{
			name: "azdo bicep federated",
			props: projectProperties{
				CiProvider:    ciProviderAzureDevOps,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
			},
			wantSubstr: []string{
				"main",
			},
		},
		{
			name: "azdo terraform federated",
			props: projectProperties{
				CiProvider:    ciProviderAzureDevOps,
				InfraProvider: infraProviderTerraform,
				BranchName:    "develop",
				AuthType:      AuthTypeFederated,
			},
			wantSubstr: []string{
				"develop",
				"AZURE_LOCATION",
				"AZURE_ENV_NAME",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, "azure-dev.yml")

			err := generatePipelineDefinition(outPath, tt.props)
			require.NoError(t, err)

			data, err := os.ReadFile(outPath)
			require.NoError(t, err)
			content := string(data)

			assert.NotEmpty(t, content)
			for _, sub := range tt.wantSubstr {
				assert.Contains(t, content, sub,
					"expected %q in generated YAML", sub)
			}
			for _, sub := range tt.notWantSubstr {
				assert.NotContains(t, content, sub,
					"did not expect %q in generated YAML", sub)
			}
		})
	}
}

func Test_generatePipelineDefinition_invalidProvider(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "azure-dev.yml")

	err := generatePipelineDefinition(outPath, projectProperties{
		CiProvider: ciProviderType("bogus"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing embedded file")
}

func Test_generatePipelineDefinition_invalidPath(t *testing.T) {
	t.Parallel()

	// Writing to a directory that doesn't exist
	badPath := filepath.Join(t.TempDir(), "nonexistent", "deep", "azure-dev.yml")

	err := generatePipelineDefinition(badPath, projectProperties{
		CiProvider:    ciProviderGitHubActions,
		InfraProvider: infraProviderBicep,
		BranchName:    "main",
		AuthType:      AuthTypeFederated,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating file")
}

// ---------------------------------------------------------------------------
// parseAzDoRemote — additional edge cases
// ---------------------------------------------------------------------------

func Test_parseAzDoRemote_additionalEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		project string
		repo    string
		nonStd  bool
	}{
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "git URL with only _git/ but no project",
			input:   "/_git/repo",
			wantErr: true,
		},
		{
			name:    "git URL with _git/ ending slash in project part",
			input:   "https://dev.azure.com/org//_git/repo",
			wantErr: true,
		},
		{
			name:    "plain HTTPS URL without _git",
			input:   "https://dev.azure.com/org/project",
			wantErr: true,
		},
		{
			name:    "git@ URL with dev.azure.com host",
			input:   "git@ssh.dev.azure.com:v3/org/project/repo",
			project: "project",
			repo:    "repo",
			nonStd:  false,
		},
		{
			name:    "HTTPS URL with trailing slash on repo",
			input:   "https://dev.azure.com/org/project/_git/repo/",
			project: "project",
			repo:    "repo/",
			nonStd:  false,
		},
		{
			name:    "URL with spaces in org (encoded)",
			input:   "https://dev.azure.com/my%20org/project/_git/repo",
			project: "project",
			repo:    "repo",
			nonStd:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := parseAzDoRemote(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.project, result.Project)
			assert.Equal(t, tt.repo, result.RepositoryName)
			assert.Equal(t, tt.nonStd, result.IsNonStandardHost)
		})
	}
}

// ---------------------------------------------------------------------------
// credentialNameSanitizer — additional edge cases
// ---------------------------------------------------------------------------

func Test_credentialNameSanitizer_additionalEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"all special chars", "!@#$%^&*()", "----------"},
		{"unicode chars", "café/naïve", "caf--na-ve"},
		{"long repo slug", strings.Repeat("a/", 50) + "b", strings.Repeat("a-", 50) + "b"},
		{"consecutive slashes", "org//repo", "org--repo"},
		{"spaces", "my org/my repo", "my-org-my-repo"},
		{"colons and equals", "a:b=c", "a-b-c"},
		{"mixed safe and unsafe", "A-Z_0.9/test", "A-Z_0-9-test"},
		{"numbers preserved", "123-456_789", "123-456_789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := credentialNameSanitizer.ReplaceAllString(tt.input, "-")
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// gitHubActionsEnablingChoice.String() — panic on invalid
// ---------------------------------------------------------------------------

func Test_gitHubActionsEnablingChoice_String_valid(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, manualChoice.String())
	assert.NotEmpty(t, cancelChoice.String())
	assert.Contains(t, manualChoice.String(), "manually enabled")
	assert.Contains(t, cancelChoice.String(), "Exit without")
}

func Test_gitHubActionsEnablingChoice_String_panicOnInvalid(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		_ = gitHubActionsEnablingChoice(99).String()
	})
}

// ---------------------------------------------------------------------------
// escapeValuesForPipeline — additional edge cases
// ---------------------------------------------------------------------------

func Test_escapeValuesForPipeline_additionalEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			"empty map",
			map[string]string{},
			map[string]string{},
		},
		{
			"plain values unchanged",
			map[string]string{"key": "value"},
			map[string]string{"key": "value"},
		},
		{
			"JSON array escaping",
			map[string]string{"key": `["api://guid"]`},
			map[string]string{"key": `[\"api://guid\"]`},
		},
		{
			"backslash escaping",
			map[string]string{"key": `path\to\file`},
			map[string]string{"key": `path\\to\\file`},
		},
		{
			"double quotes escaped",
			map[string]string{"key": `say "hello"`},
			map[string]string{"key": `say \"hello\"`},
		},
		{
			"newline in value",
			map[string]string{"key": "line1\nline2"},
			map[string]string{"key": `line1\nline2`},
		},
		{
			"tab in value",
			map[string]string{"key": "col1\tcol2"},
			map[string]string{"key": `col1\tcol2`},
		},
		{
			"unicode is preserved",
			map[string]string{"key": "日本語"},
			map[string]string{"key": "日本語"},
		},
		{
			"multiple keys",
			map[string]string{"a": `"x"`, "b": "plain"},
			map[string]string{"a": `\"x\"`, "b": "plain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Clone input to avoid test pollution
			m := make(map[string]string, len(tt.input))
			maps.Copy(m, tt.input)

			escapeValuesForPipeline(m)
			assert.Equal(t, tt.expected, m)
		})
	}
}

// ---------------------------------------------------------------------------
// mergeProjectVariablesAndSecrets — UsingEnvVarMapping path
// ---------------------------------------------------------------------------

func Test_mergeProjectVariablesAndSecrets_usingEnvVarMapping(t *testing.T) {
	t.Parallel()

	t.Run("single env var with UsingEnvVarMapping true", func(t *testing.T) {
		t.Parallel()
		params := []provisioning.Parameter{
			{
				Name:               "dbConn",
				Value:              "connstr",
				Secret:             true,
				LocalPrompt:        false,
				UsingEnvVarMapping: true,
				EnvVarMapping:      []string{"DB_CONN"},
			},
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "connstr", secrets["DB_CONN"])
		assert.Empty(t, vars)
	})

	t.Run("non-prompt non-envVarMapping skips param", func(t *testing.T) {
		t.Parallel()
		params := []provisioning.Parameter{
			{
				Name:               "ignored",
				Value:              "val",
				Secret:             false,
				LocalPrompt:        false,
				UsingEnvVarMapping: false,
				EnvVarMapping:      []string{"IGNORED_VAR"},
			},
		}
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, map[string]string{})
		require.NoError(t, err)
		assert.Empty(t, vars)
		assert.Empty(t, secrets)
	})

	t.Run("project variables override from env", func(t *testing.T) {
		t.Parallel()
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			[]string{"PROJ_VAR"}, []string{"PROJ_SECRET"},
			map[string]string{}, map[string]string{},
			nil,
			map[string]string{
				"PROJ_VAR":    "var-val",
				"PROJ_SECRET": "secret-val",
				"OTHER":       "ignored",
			})
		require.NoError(t, err)
		assert.Equal(t, "var-val", vars["PROJ_VAR"])
		assert.Equal(t, "secret-val", secrets["PROJ_SECRET"])
		_, hasOther := vars["OTHER"]
		assert.False(t, hasOther)
	})

	t.Run("empty env values are skipped", func(t *testing.T) {
		t.Parallel()
		vars, _, err := mergeProjectVariablesAndSecrets(
			[]string{"PROJ_VAR"}, nil,
			map[string]string{}, map[string]string{},
			nil,
			map[string]string{"PROJ_VAR": ""})
		require.NoError(t, err)
		_, hasVar := vars["PROJ_VAR"]
		assert.False(t, hasVar)
	})

	t.Run("initial variables are preserved", func(t *testing.T) {
		t.Parallel()
		vars, secrets, err := mergeProjectVariablesAndSecrets(
			nil, nil,
			map[string]string{"INIT_VAR": "init-v"},
			map[string]string{"INIT_SECRET": "init-s"},
			nil, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "init-v", vars["INIT_VAR"])
		assert.Equal(t, "init-s", secrets["INIT_SECRET"])
	})
}

// ---------------------------------------------------------------------------
// toCiProviderType / toInfraProviderType — additional edge cases
// ---------------------------------------------------------------------------

func Test_toCiProviderType_additionalCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    ciProviderType
		wantErr bool
	}{
		{gitHubCode, ciProviderGitHubActions, false},
		{azdoCode, ciProviderAzureDevOps, false},
		{"GITHUB", "", true},
		{"GitHub", "", true},
		{"", "", true},
		{"jenkins", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := toCiProviderType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_toInfraProviderType_additionalCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    infraProviderType
		wantErr bool
	}{
		{"bicep", infraProviderBicep, false},
		{"terraform", infraProviderTerraform, false},
		{"", infraProviderUndefined, false},
		{"Bicep", "", true},
		{"TERRAFORM", "", true},
		{"pulumi", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := toInfraProviderType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// generateFilePaths — additional cases
// ---------------------------------------------------------------------------

func Test_generateFilePaths_additionalCases(t *testing.T) {
	t.Parallel()

	t.Run("empty directories", func(t *testing.T) {
		t.Parallel()
		result := generateFilePaths([]string{}, []string{"a.yml"})
		assert.Empty(t, result)
	})

	t.Run("empty file names", func(t *testing.T) {
		t.Parallel()
		result := generateFilePaths([]string{"dir"}, []string{})
		assert.Empty(t, result)
	})

	t.Run("multiple dirs and files", func(t *testing.T) {
		t.Parallel()
		result := generateFilePaths(
			[]string{"d1", "d2"},
			[]string{"f1.yml", "f2.yml"})
		assert.Equal(t, 4, len(result))
		assert.Contains(t, result, filepath.Join("d1", "f1.yml"))
		assert.Contains(t, result, filepath.Join("d1", "f2.yml"))
		assert.Contains(t, result, filepath.Join("d2", "f1.yml"))
		assert.Contains(t, result, filepath.Join("d2", "f2.yml"))
	})
}

// ---------------------------------------------------------------------------
// hasPipelineFile — additional cases
// ---------------------------------------------------------------------------

func Test_hasPipelineFile_additionalCases(t *testing.T) {
	t.Parallel()

	t.Run("returns false for empty directory", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		assert.False(t, hasPipelineFile(ciProviderGitHubActions, tmpDir))
		assert.False(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
	})

	t.Run("returns true when github file exists", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(ghDir, "azure-dev.yml"),
			[]byte("test"), 0600))

		assert.True(t, hasPipelineFile(ciProviderGitHubActions, tmpDir))
		assert.False(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
	})

	t.Run("returns true when azdo file exists", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		azdoDir := filepath.Join(tmpDir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(azdoDir, "azure-dev.yml"),
			[]byte("test"), 0600))

		assert.True(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
		assert.False(t, hasPipelineFile(ciProviderGitHubActions, tmpDir))
	})

	t.Run("returns true when yaml extension is used", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(ghDir, "azure-dev.yaml"),
			[]byte("test"), 0600))

		assert.True(t, hasPipelineFile(ciProviderGitHubActions, tmpDir))
	})

	t.Run("returns true when azdo alt dir is used", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		altDir := filepath.Join(tmpDir, ".azuredevops", "pipelines")
		require.NoError(t, os.MkdirAll(altDir, os.ModePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(altDir, "azure-dev.yml"),
			[]byte("test"), 0600))

		assert.True(t, hasPipelineFile(ciProviderAzureDevOps, tmpDir))
	})
}

// ---------------------------------------------------------------------------
// pipelineProviderFiles map — structural validation
// ---------------------------------------------------------------------------

func Test_pipelineProviderFiles_structure(t *testing.T) {
	t.Parallel()

	for _, provider := range []ciProviderType{
		ciProviderGitHubActions,
		ciProviderAzureDevOps,
	} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			info, ok := pipelineProviderFiles[provider]
			require.True(t, ok, "provider %s missing from map", provider)
			assert.NotEmpty(t, info.RootDirectories)
			assert.NotEmpty(t, info.PipelineDirectories)
			assert.NotEmpty(t, info.Files)
			assert.NotEmpty(t, info.DefaultFile)
			assert.NotEmpty(t, info.DisplayName)
		})
	}
}

// ---------------------------------------------------------------------------
// Provider Name() methods — verify display name constants
// ---------------------------------------------------------------------------

func Test_ProviderName_GitHub(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	// Create azure.yaml so the manager can load project settings
	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, manager)

	assert.Equal(t, "GitHub", manager.CiProviderName())
	assert.Equal(t, "GitHub", manager.ScmProviderName())
}

func Test_ProviderName_AzDo(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	// Create azure.yaml so the manager can load project settings
	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, manager)

	assert.Equal(t, "Azure DevOps", manager.CiProviderName())
	assert.Equal(t, "Azure DevOps", manager.ScmProviderName())
}

// ---------------------------------------------------------------------------
// PipelineManager.requiredTools — GitHub providers return ghCli tool
// ---------------------------------------------------------------------------

func Test_PipelineManager_requiredTools_GitHub(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.requiredTools(ctx)
	require.NoError(t, err)
	// GitHub providers each return ghCli — expect >=1 tool
	assert.NotEmpty(t, tools)
}

func Test_PipelineManager_requiredTools_AzDo(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.requiredTools(ctx)
	require.NoError(t, err)
	// AzDo providers return empty required tools
	assert.Empty(t, tools)
}

// ---------------------------------------------------------------------------
// PipelineManager.preConfigureCheck — invalid auth type validation
// ---------------------------------------------------------------------------

func Test_PipelineManager_preConfigureCheck_invalidAuthType(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	args := &PipelineManagerArgs{
		PipelineAuthTypeName: "invalid-auth-type",
	}
	manager, err := createPipelineManager(
		mockContext, azdContext, nil, args)
	require.NoError(t, err)

	_, err = manager.preConfigureCheck(
		ctx, provisioning.Options{}, tempDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"pipeline authentication type 'invalid-auth-type' is not valid")
}

// ---------------------------------------------------------------------------
// PipelineManager direct provider Name() — verify GitHub display names
// ---------------------------------------------------------------------------

func Test_GitHubScmProvider_Name(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
	assert.Equal(t, "GitHub", manager.scmProvider.Name())
}

func Test_GitHubCiProvider_Name(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
	assert.Equal(t, "GitHub", manager.ciProvider.Name())
}

func Test_AzdoScmProvider_Name(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
	assert.Equal(t, "Azure DevOps", manager.scmProvider.Name())
}

func Test_AzdoCiProvider_Name(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
	assert.Equal(t, "Azure DevOps", manager.ciProvider.Name())
}

// ---------------------------------------------------------------------------
// Provider requiredTools — verify returned tool slices
// ---------------------------------------------------------------------------

func Test_GitHubScmProvider_requiredTools(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.scmProvider.requiredTools(ctx)
	require.NoError(t, err)
	// GitHub SCM provider requires the gh CLI tool
	assert.Len(t, tools, 1)
}

func Test_GitHubCiProvider_requiredTools(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.ciProvider.requiredTools(ctx)
	require.NoError(t, err)
	// GitHub CI provider requires the gh CLI tool
	assert.Len(t, tools, 1)
}

func Test_AzdoScmProvider_requiredTools(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.scmProvider.requiredTools(ctx)
	require.NoError(t, err)
	// AzDo SCM provider has no required external tools
	assert.Empty(t, tools)
}

func Test_AzdoCiProvider_requiredTools(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	tools, err := manager.ciProvider.requiredTools(ctx)
	require.NoError(t, err)
	// AzDo CI provider has no required external tools
	assert.Empty(t, tools)
}

// ---------------------------------------------------------------------------
// AzdoCiProvider.preConfigureCheck — federated auth returns error
// ---------------------------------------------------------------------------

func Test_AzdoCiProvider_preConfigureCheck_federatedAuthError(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	// AzDo CI provider explicitly rejects federated auth
	_, err = manager.ciProvider.preConfigureCheck(
		ctx,
		PipelineManagerArgs{PipelineAuthTypeName: string(AuthTypeFederated)},
		provisioning.Options{},
		tempDir,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAuthNotSupported)
	assert.Contains(t, err.Error(), "does not support federated")
}

// ---------------------------------------------------------------------------
// GitHubScmProvider.gitRepoDetails — basic remote parsing
// ---------------------------------------------------------------------------

func Test_GitHubScmProvider_gitRepoDetails(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	ghScm, ok := manager.scmProvider.(*GitHubScmProvider)
	require.True(t, ok)

	details, err := ghScm.gitRepoDetails(ctx, "https://github.com/azure/azure-dev")
	require.NoError(t, err)
	assert.Equal(t, "azure", details.owner)
	assert.Equal(t, "azure-dev", details.repoName)
	assert.Equal(t, "https://github.com/azure/azure-dev", details.remote)
}

// ---------------------------------------------------------------------------
// preventGitPush — both providers, nil return
// ---------------------------------------------------------------------------

func Test_AzdoScmProvider_preventGitPush(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

	manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
	require.NoError(t, err)

	azdoScm, ok := manager.scmProvider.(*AzdoScmProvider)
	require.True(t, ok)

	prevented, err := azdoScm.preventGitPush(ctx, nil, "origin", "main")
	require.NoError(t, err)
	assert.False(t, prevented)
}

// ---------------------------------------------------------------------------
// helper: resetContext_forTest — creates new pipeline manager helpers
// (uses existing test helpers from pipeline_manager_test.go)
// ---------------------------------------------------------------------------

func helperSetupManager(
	t *testing.T, provider ciProviderType,
) (*PipelineManager, *mocks.MockContext) {
	t.Helper()
	tempDir := t.TempDir()
	ctx := context.Background()
	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockCtx := resetContext(tempDir, ctx)

	resetAzureYaml(t, filepath.Join(tempDir, "azure.yaml"))
	deleteYamlFiles(t, tempDir)
	simulateUserInteraction(mockCtx, provider, true)

	manager, err := createPipelineManager(mockCtx, azdContext, nil, nil)
	require.NoError(t, err)
	return manager, mockCtx
}

// ---------------------------------------------------------------------------
// PipelineManager.SetParameters — sets configOptions via mock manager
// ---------------------------------------------------------------------------

func Test_PipelineManager_SetParameters_multipleParams(t *testing.T) {
	t.Parallel()
	manager, _ := helperSetupManager(t, ciProviderGitHubActions)

	params := []provisioning.Parameter{
		{Name: "param1", Value: "val1"},
		{Name: "param2", Value: "val2"},
	}
	manager.SetParameters(params)

	require.NotNil(t, manager.configOptions)
	assert.Len(t, manager.configOptions.providerParameters, 2)
	assert.Equal(t, "param1", manager.configOptions.providerParameters[0].Name)
}

// ---------------------------------------------------------------------------
// PipelineManager.SetParameters — nil configOptions gets initialized
// ---------------------------------------------------------------------------

func Test_PipelineManager_SetParameters_nilConfigOptions(t *testing.T) {
	t.Parallel()
	manager, _ := helperSetupManager(t, ciProviderGitHubActions)

	// Force nil configOptions (it may be set by the constructor)
	manager.configOptions = nil
	manager.SetParameters([]provisioning.Parameter{{Name: "p1", Value: "v1"}})

	require.NotNil(t, manager.configOptions)
	assert.Len(t, manager.configOptions.providerParameters, 1)
}
