// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	azdoGit "github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =====================================================================
// Mock providers for testing PipelineManager methods
// =====================================================================

type mockScmProvider struct {
	requiredToolsFn     func(ctx context.Context) ([]tools.ExternalTool, error)
	preConfigureCheckFn func(
		ctx context.Context, args PipelineManagerArgs, opts provisioning.Options, path string,
	) (bool, error)
	nameFn               func() string
	gitRepoDetailsFn     func(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error)
	configureGitRemoteFn func(
		ctx context.Context, repoPath string, remoteName string,
	) (string, error)
	preventGitPushFn func(
		ctx context.Context, gitRepo *gitRepositoryDetails,
		remoteName string, branchName string,
	) (bool, error)
	gitPushFn func(
		ctx context.Context, gitRepo *gitRepositoryDetails,
		remoteName string, branchName string,
	) error
}

func (m *mockScmProvider) requiredTools(ctx context.Context) ([]tools.ExternalTool, error) {
	if m.requiredToolsFn != nil {
		return m.requiredToolsFn(ctx)
	}
	return []tools.ExternalTool{}, nil
}

func (m *mockScmProvider) preConfigureCheck(
	ctx context.Context, args PipelineManagerArgs, opts provisioning.Options, path string,
) (bool, error) {
	if m.preConfigureCheckFn != nil {
		return m.preConfigureCheckFn(ctx, args, opts, path)
	}
	return false, nil
}

func (m *mockScmProvider) Name() string {
	if m.nameFn != nil {
		return m.nameFn()
	}
	return "mock-scm"
}

func (m *mockScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	if m.gitRepoDetailsFn != nil {
		return m.gitRepoDetailsFn(ctx, remoteUrl)
	}
	return &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		remote:   remoteUrl,
		url:      "https://example.com/test-owner/test-repo",
	}, nil
}

func (m *mockScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string) (string, error) {
	if m.configureGitRemoteFn != nil {
		return m.configureGitRemoteFn(ctx, repoPath, remoteName)
	}
	return "https://example.com/test-owner/test-repo.git", nil
}

func (m *mockScmProvider) preventGitPush(
	ctx context.Context, gitRepo *gitRepositoryDetails,
	remoteName string, branchName string,
) (bool, error) {
	if m.preventGitPushFn != nil {
		return m.preventGitPushFn(ctx, gitRepo, remoteName, branchName)
	}
	return false, nil
}

func (m *mockScmProvider) GitPush(
	ctx context.Context, gitRepo *gitRepositoryDetails,
	remoteName string, branchName string,
) error {
	if m.gitPushFn != nil {
		return m.gitPushFn(ctx, gitRepo, remoteName, branchName)
	}
	return nil
}

type mockCiProvider struct {
	requiredToolsFn     func(ctx context.Context) ([]tools.ExternalTool, error)
	preConfigureCheckFn func(
		ctx context.Context, args PipelineManagerArgs,
		opts provisioning.Options, path string,
	) (bool, error)
	nameFn              func() string
	configurePipelineFn func(
		ctx context.Context, repoDetails *gitRepositoryDetails,
		options *configurePipelineOptions,
	) (CiPipeline, error)
	configureConnectionFn func(
		ctx context.Context, gitRepo *gitRepositoryDetails,
		opts provisioning.Options, authConfig *authConfiguration,
		credOpts *CredentialOptions,
	) error
	credentialOptionsFn func(
		ctx context.Context, repoDetails *gitRepositoryDetails,
		infraOptions provisioning.Options, authType PipelineAuthType,
		credentials *entraid.AzureCredentials,
	) (*CredentialOptions, error)
}

func (m *mockCiProvider) requiredTools(ctx context.Context) ([]tools.ExternalTool, error) {
	if m.requiredToolsFn != nil {
		return m.requiredToolsFn(ctx)
	}
	return []tools.ExternalTool{}, nil
}

func (m *mockCiProvider) preConfigureCheck(
	ctx context.Context, args PipelineManagerArgs,
	opts provisioning.Options, path string,
) (bool, error) {
	if m.preConfigureCheckFn != nil {
		return m.preConfigureCheckFn(ctx, args, opts, path)
	}
	return false, nil
}

func (m *mockCiProvider) Name() string {
	if m.nameFn != nil {
		return m.nameFn()
	}
	return "mock-ci"
}

func (m *mockCiProvider) configurePipeline(
	ctx context.Context, repoDetails *gitRepositoryDetails,
	options *configurePipelineOptions,
) (CiPipeline, error) {
	if m.configurePipelineFn != nil {
		return m.configurePipelineFn(ctx, repoDetails, options)
	}
	return &workflow{repoDetails: repoDetails}, nil
}

func (m *mockCiProvider) configureConnection(
	ctx context.Context, gitRepo *gitRepositoryDetails,
	opts provisioning.Options, authConfig *authConfiguration,
	credOpts *CredentialOptions,
) error {
	if m.configureConnectionFn != nil {
		return m.configureConnectionFn(ctx, gitRepo, opts, authConfig, credOpts)
	}
	return nil
}

func (m *mockCiProvider) credentialOptions(
	ctx context.Context, repoDetails *gitRepositoryDetails,
	infraOptions provisioning.Options, authType PipelineAuthType,
	credentials *entraid.AzureCredentials,
) (*CredentialOptions, error) {
	if m.credentialOptionsFn != nil {
		return m.credentialOptionsFn(ctx, repoDetails, infraOptions, authType, credentials)
	}
	return &CredentialOptions{}, nil
}

// =====================================================================
// PipelineManager.requiredTools
// =====================================================================

func Test_PipelineManager_requiredTools_cov3(t *testing.T) {
	t.Parallel()

	t.Run("aggregates tools from both providers", func(t *testing.T) {
		t.Parallel()

		mockTool1 := &mockExternalTool{name: "tool1"}
		mockTool2 := &mockExternalTool{name: "tool2"}

		pm := &PipelineManager{
			scmProvider: &mockScmProvider{
				requiredToolsFn: func(_ context.Context) ([]tools.ExternalTool, error) {
					return []tools.ExternalTool{mockTool1}, nil
				},
			},
			ciProvider: &mockCiProvider{
				requiredToolsFn: func(_ context.Context) ([]tools.ExternalTool, error) {
					return []tools.ExternalTool{mockTool2}, nil
				},
			},
		}

		result, err := pm.requiredTools(t.Context())
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("returns error from scm provider", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			scmProvider: &mockScmProvider{
				requiredToolsFn: func(_ context.Context) ([]tools.ExternalTool, error) {
					return nil, errors.New("scm tool error")
				},
			},
			ciProvider: &mockCiProvider{},
		}

		_, err := pm.requiredTools(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scm tool error")
	})

	t.Run("returns error from ci provider", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			scmProvider: &mockScmProvider{},
			ciProvider: &mockCiProvider{
				requiredToolsFn: func(_ context.Context) ([]tools.ExternalTool, error) {
					return nil, errors.New("ci tool error")
				},
			},
		}

		_, err := pm.requiredTools(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ci tool error")
	})
}

type mockExternalTool struct {
	name string
}

func (m *mockExternalTool) CheckInstalled(_ context.Context) error { return nil }
func (m *mockExternalTool) InstallUrl() string                     { return "" }
func (m *mockExternalTool) Name() string                           { return m.name }

// =====================================================================
// PipelineManager.preConfigureCheck
// =====================================================================

func Test_PipelineManager_preConfigureCheck_cov3(t *testing.T) {
	t.Parallel()

	t.Run("invalid auth type returns error", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{
				PipelineAuthTypeName: "invalid-auth-type",
			},
			scmProvider: &mockScmProvider{},
			ciProvider:  &mockCiProvider{},
		}

		_, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid-auth-type")
		assert.Contains(t, err.Error(), "not valid")
	})

	t.Run("valid federated auth type succeeds", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{
				PipelineAuthTypeName: string(AuthTypeFederated),
			},
			scmProvider: &mockScmProvider{},
			ciProvider:  &mockCiProvider{},
		}

		updated, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.NoError(t, err)
		assert.False(t, updated)
	})

	t.Run("valid client-credentials auth type succeeds", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{
				PipelineAuthTypeName: string(AuthTypeClientCredentials),
			},
			scmProvider: &mockScmProvider{},
			ciProvider:  &mockCiProvider{},
		}

		updated, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.NoError(t, err)
		assert.False(t, updated)
	})

	t.Run("empty auth type succeeds", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args:        &PipelineManagerArgs{},
			scmProvider: &mockScmProvider{},
			ciProvider:  &mockCiProvider{},
		}

		updated, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.NoError(t, err)
		assert.False(t, updated)
	})

	t.Run("ci provider error propagates", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args:        &PipelineManagerArgs{},
			scmProvider: &mockScmProvider{},
			ciProvider: &mockCiProvider{
				preConfigureCheckFn: func(
					_ context.Context, _ PipelineManagerArgs,
					_ provisioning.Options, _ string,
				) (bool, error) {
					return false, errors.New("ci-check failed")
				},
				nameFn: func() string { return "test-ci" },
			},
		}

		_, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ci-check failed")
		assert.Contains(t, err.Error(), "test-ci")
	})

	t.Run("scm provider error propagates", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{},
			scmProvider: &mockScmProvider{
				preConfigureCheckFn: func(
					_ context.Context, _ PipelineManagerArgs,
					_ provisioning.Options, _ string,
				) (bool, error) {
					return false, errors.New("scm-check failed")
				},
				nameFn: func() string { return "test-scm" },
			},
			ciProvider: &mockCiProvider{},
		}

		_, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scm-check failed")
		assert.Contains(t, err.Error(), "test-scm")
	})

	t.Run("returns true when either provider updated config", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{},
			scmProvider: &mockScmProvider{
				preConfigureCheckFn: func(
					_ context.Context, _ PipelineManagerArgs,
					_ provisioning.Options, _ string,
				) (bool, error) {
					return true, nil
				},
			},
			ciProvider: &mockCiProvider{},
		}

		updated, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.NoError(t, err)
		assert.True(t, updated)
	})

	t.Run("whitespace-only auth type treated as empty", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			args: &PipelineManagerArgs{
				PipelineAuthTypeName: "  ",
			},
			scmProvider: &mockScmProvider{},
			ciProvider:  &mockCiProvider{},
		}

		_, err := pm.preConfigureCheck(t.Context(), provisioning.Options{}, "/fake/path")
		require.NoError(t, err)
	})
}

// =====================================================================
// PipelineManager.CiProviderName / ScmProviderName
// =====================================================================

func Test_PipelineManager_ProviderNames(t *testing.T) {
	t.Parallel()

	pm := &PipelineManager{
		ciProvider: &mockCiProvider{
			nameFn: func() string { return "my-ci" },
		},
		scmProvider: &mockScmProvider{
			nameFn: func() string { return "my-scm" },
		},
	}

	assert.Equal(t, "my-ci", pm.CiProviderName())
	assert.Equal(t, "my-scm", pm.ScmProviderName())
}

// =====================================================================
// PipelineManager.SetParameters
// =====================================================================

func Test_PipelineManager_SetParameters_cov3(t *testing.T) {
	t.Parallel()

	t.Run("sets parameters on nil configOptions", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{}
		params := []provisioning.Parameter{
			{Name: "param1", Value: "val1"},
		}
		pm.SetParameters(params)

		require.NotNil(t, pm.configOptions)
		assert.Equal(t, params, pm.configOptions.providerParameters)
	})

	t.Run("sets parameters on existing configOptions", func(t *testing.T) {
		t.Parallel()

		pm := &PipelineManager{
			configOptions: &configurePipelineOptions{
				secrets: map[string]string{"existing": "secret"},
			},
		}
		params := []provisioning.Parameter{
			{Name: "param2", Value: "val2"},
		}
		pm.SetParameters(params)

		assert.Equal(t, params, pm.configOptions.providerParameters)
		// existing fields preserved
		assert.Equal(t, "secret", pm.configOptions.secrets["existing"])
	})
}

// =====================================================================
// PipelineManager.savePipelineProviderToEnv
// =====================================================================

func Test_PipelineManager_savePipelineProviderToEnv(t *testing.T) {
	t.Parallel()

	t.Run("saves provider to env and calls envManager", func(t *testing.T) {
		t.Parallel()

		envManager := &mockenv.MockEnvManager{}
		env := environment.New("test")
		envManager.On("Save", mock.Anything, env).Return(nil)

		pm := &PipelineManager{
			envManager: envManager,
		}

		err := pm.savePipelineProviderToEnv(t.Context(), ciProviderGitHubActions, env)
		require.NoError(t, err)

		val := env.Dotenv()[envPersistedKey]
		assert.Equal(t, string(ciProviderGitHubActions), val)
		envManager.AssertCalled(t, "Save", mock.Anything, env)
	})

	t.Run("propagates envManager save error", func(t *testing.T) {
		t.Parallel()

		envManager := &mockenv.MockEnvManager{}
		env := environment.New("test")
		envManager.On("Save", mock.Anything, env).Return(errors.New("save failed"))

		pm := &PipelineManager{
			envManager: envManager,
		}

		err := pm.savePipelineProviderToEnv(t.Context(), ciProviderAzureDevOps, env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "save failed")
	})
}

// =====================================================================
// PipelineManager.ensureRemote
// =====================================================================

func Test_PipelineManager_ensureRemote(t *testing.T) {
	t.Parallel()

	t.Run("success path", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		// Mock git commands
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "remote get-url")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "https://github.com/test-owner/test-repo.git", ""), nil
		})
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "branch --show-current")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "main", ""), nil
		})

		pm := &PipelineManager{
			azdCtx: azdCtx,
			gitCli: git.NewCli(mockContext.CommandRunner),
			scmProvider: &mockScmProvider{
				gitRepoDetailsFn: func(_ context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
					return &gitRepositoryDetails{
						owner:    "test-owner",
						repoName: "test-repo",
						remote:   remoteUrl,
						url:      "https://github.com/test-owner/test-repo",
					}, nil
				},
			},
		}

		details, err := pm.ensureRemote(*mockContext.Context, tmpDir, "origin")
		require.NoError(t, err)
		assert.Equal(t, "test-owner", details.owner)
		assert.Equal(t, "test-repo", details.repoName)
		assert.Equal(t, "main", details.branch)
		assert.Equal(t, tmpDir, details.gitProjectPath)
	})

	t.Run("git remote url error propagates", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "remote get-url")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, errors.New("no such remote")
		})

		pm := &PipelineManager{
			azdCtx:      azdCtx,
			gitCli:      git.NewCli(mockContext.CommandRunner),
			scmProvider: &mockScmProvider{},
		}

		_, err := pm.ensureRemote(*mockContext.Context, tmpDir, "origin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get remote url")
	})

	t.Run("git branch error propagates", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "remote get-url")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "https://github.com/owner/repo.git", ""), nil
		})

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "branch --show-current")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, errors.New("detached HEAD")
		})

		pm := &PipelineManager{
			azdCtx:      azdCtx,
			gitCli:      git.NewCli(mockContext.CommandRunner),
			scmProvider: &mockScmProvider{},
		}

		_, err := pm.ensureRemote(*mockContext.Context, tmpDir, "origin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting current branch")
	})

	t.Run("scm provider gitRepoDetails error propagates", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "remote get-url")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "https://unknown.com/owner/repo.git", ""), nil
		})
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "branch --show-current")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "main", ""), nil
		})

		pm := &PipelineManager{
			azdCtx: azdCtx,
			gitCli: git.NewCli(mockContext.CommandRunner),
			scmProvider: &mockScmProvider{
				gitRepoDetailsFn: func(_ context.Context, _ string) (*gitRepositoryDetails, error) {
					return nil, errors.New("unsupported remote host")
				},
			},
		}

		_, err := pm.ensureRemote(*mockContext.Context, tmpDir, "origin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported remote host")
	})
}

// =====================================================================
// PipelineManager.checkAndPromptForProviderFiles
// =====================================================================

func Test_PipelineManager_checkAndPromptForProviderFiles(t *testing.T) {
	t.Parallel()

	t.Run("files already present - returns nil", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("trigger: none"), 0600))

		console := mockinput.NewMockConsole()
		pm := &PipelineManager{
			console: console,
		}

		err := pm.checkAndPromptForProviderFiles(t.Context(), projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tmpDir,
		})
		require.NoError(t, err)
	})

	t.Run("azdo provider - no files - returns error", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		// Create empty directories
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".azdo", "pipelines"), os.ModePerm))

		console := mockinput.NewMockConsole()
		// When prompted to create file, say no
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(false)

		pm := &PipelineManager{
			console: console,
		}

		err := pm.checkAndPromptForProviderFiles(t.Context(), projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
			RepoRoot:      tmpDir,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Azure DevOps")
		assert.Contains(t, err.Error(), "no pipeline files")
	})

	t.Run("github provider - prompt creates file then succeeds", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		console := mockinput.NewMockConsole()
		// Confirm creation
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to add it now")
		}).Respond(true)

		pm := &PipelineManager{
			console: console,
		}

		err := pm.checkAndPromptForProviderFiles(t.Context(), projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
			RepoRoot:      tmpDir,
		})
		require.NoError(t, err)

		// Verify file was created
		createdFile := filepath.Join(tmpDir, ".github", "workflows", "azure-dev.yml")
		assert.FileExists(t, createdFile)
	})

	t.Run("github provider - prompt declined - empty dirs - shows message", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(false)

		pm := &PipelineManager{
			console: console,
		}

		// Should not error for GitHub (unlike AzDo) even with empty dirs
		err := pm.checkAndPromptForProviderFiles(t.Context(), projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
			RepoRoot:      tmpDir,
		})
		// For GitHub, when no files exist AND user declines, it just shows a message, no error
		require.NoError(t, err)
	})
}

// =====================================================================
// PipelineManager.determineProvider
// =====================================================================

func Test_PipelineManager_determineProvider(t *testing.T) {
	t.Parallel()

	t.Run("only github yaml - selects github", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))

		pm := &PipelineManager{
			console: mockinput.NewMockConsole(),
		}

		provider, err := pm.determineProvider(t.Context(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("only azdo yaml - selects azdo", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		azdoDir := filepath.Join(tmpDir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(azdoDir, "azure-dev.yml"), []byte("trigger: main"), 0600))

		pm := &PipelineManager{
			console: mockinput.NewMockConsole(),
		}

		provider, err := pm.determineProvider(t.Context(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})

	t.Run("both yaml files - prompts user for github (index 0)", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		// Create both
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))
		azdoDir := filepath.Join(tmpDir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(azdoDir, "azure-dev.yml"), []byte("trigger: main"), 0600))

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select a provider")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, nil // GitHub
		})

		pm := &PipelineManager{
			console: console,
		}

		provider, err := pm.determineProvider(t.Context(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("neither yaml file - prompts user for azdo (index 1)", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select a provider")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 1, nil // AzDo
		})

		pm := &PipelineManager{
			console: console,
		}

		provider, err := pm.determineProvider(t.Context(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})

	t.Run("prompt error propagates", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return true
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, errors.New("user cancelled")
		})

		pm := &PipelineManager{
			console: console,
		}

		_, err := pm.determineProvider(t.Context(), tmpDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user cancelled")
	})
}

// =====================================================================
// PipelineManager.promptForProvider
// =====================================================================

func Test_PipelineManager_promptForProvider(t *testing.T) {
	t.Parallel()

	t.Run("selects github at index 0", func(t *testing.T) {
		t.Parallel()

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select a provider")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, nil
		})

		pm := &PipelineManager{console: console}
		provider, err := pm.promptForProvider(t.Context())
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("selects azdo at index 1", func(t *testing.T) {
		t.Parallel()

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return true
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 1, nil
		})

		pm := &PipelineManager{console: console}
		provider, err := pm.promptForProvider(t.Context())
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})
}

// =====================================================================
// PipelineManager.initialize with IoC
// =====================================================================

func Test_PipelineManager_initialize(t *testing.T) {
	t.Parallel()

	t.Run("override with github resolves providers", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		// Create azure.yaml in project dir
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "azure.yaml"), []byte("name: test\n"), 0600))

		env := environment.New("test-env")
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, env).Return(nil)

		// Mock git repo root
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "rev-parse --show-toplevel")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, tmpDir, ""), nil
		})

		scmProvider := &mockScmProvider{nameFn: func() string { return "GitHub" }}
		ciProvider := &mockCiProvider{nameFn: func() string { return "GitHub" }}

		container := ioc.NewNestedContainer(nil)
		container.MustRegisterNamedSingleton("github-scm", func() ScmProvider { return scmProvider })
		container.MustRegisterNamedSingleton("github-ci", func() CiProvider { return ciProvider })

		pm := &PipelineManager{
			azdCtx:         azdCtx,
			env:            env,
			envManager:     envManager,
			gitCli:         git.NewCli(mockContext.CommandRunner),
			serviceLocator: container,
		}

		err := pm.initialize(*mockContext.Context, "github")
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, pm.ciProviderType)
		assert.Equal(t, "GitHub", pm.CiProviderName())
		assert.Equal(t, "GitHub", pm.ScmProviderName())
	})

	t.Run("override with azdo resolves providers", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "azure.yaml"), []byte("name: test\n"), 0600))

		env := environment.New("test-env")
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, env).Return(nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "rev-parse --show-toplevel")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, tmpDir, ""), nil
		})

		scmProvider := &mockScmProvider{nameFn: func() string { return "Azure DevOps" }}
		ciProvider := &mockCiProvider{nameFn: func() string { return "Azure DevOps" }}

		container := ioc.NewNestedContainer(nil)
		container.MustRegisterNamedSingleton("azdo-scm", func() ScmProvider { return scmProvider })
		container.MustRegisterNamedSingleton("azdo-ci", func() CiProvider { return ciProvider })

		pm := &PipelineManager{
			azdCtx:         azdCtx,
			env:            env,
			envManager:     envManager,
			gitCli:         git.NewCli(mockContext.CommandRunner),
			serviceLocator: container,
		}

		err := pm.initialize(*mockContext.Context, "azdo")
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, pm.ciProviderType)
	})

	t.Run("invalid override returns error", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		tmpDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "rev-parse --show-toplevel")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, tmpDir, ""), nil
		})

		pm := &PipelineManager{
			azdCtx: azdCtx,
			gitCli: git.NewCli(mockContext.CommandRunner),
		}

		err := pm.initialize(*mockContext.Context, "INVALID")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid ci provider type")
	})
}

// =====================================================================
// PipelineManager.resolveProviderAndDetermine
// =====================================================================

func Test_PipelineManager_resolveProviderAndDetermine(t *testing.T) {
	t.Parallel()

	t.Run("uses azure.yaml pipeline.provider when set", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "azure.yaml"),
			[]byte("name: test\npipeline:\n  provider: github\n"), 0600))

		env := environment.New("test-env")

		pm := &PipelineManager{
			env:     env,
			console: mockinput.NewMockConsole(),
		}

		provider, err := pm.resolveProviderAndDetermine(t.Context(), filepath.Join(tmpDir, "azure.yaml"), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("uses persisted env var when azure.yaml has no provider", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "azure.yaml"),
			[]byte("name: test\n"), 0600))

		env := environment.NewWithValues("test-env", map[string]string{
			envPersistedKey: "azdo",
		})

		pm := &PipelineManager{
			env:     env,
			console: mockinput.NewMockConsole(),
		}

		provider, err := pm.resolveProviderAndDetermine(t.Context(), filepath.Join(tmpDir, "azure.yaml"), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})

	t.Run("falls back to determineProvider when no config", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "azure.yaml"),
			[]byte("name: test\n"), 0600))

		// Create github yaml to make determineProvider pick it
		ghDir := filepath.Join(tmpDir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))

		env := environment.New("test-env")
		pm := &PipelineManager{
			env:     env,
			console: mockinput.NewMockConsole(),
		}

		provider, err := pm.resolveProviderAndDetermine(t.Context(), filepath.Join(tmpDir, "azure.yaml"), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})
}

// =====================================================================
// GitHub provider: preventGitPush
// =====================================================================

func Test_GitHubScmProvider_preventGitPush(t *testing.T) {
	t.Parallel()

	t.Run("new repo always returns false", func(t *testing.T) {
		t.Parallel()

		provider := &GitHubScmProvider{
			newGitHubRepoCreated: true,
		}

		prevent, err := provider.preventGitPush(t.Context(), &gitRepositoryDetails{
			owner:          "test",
			repoName:       "repo",
			gitProjectPath: t.TempDir(),
		}, "origin", "main")
		require.NoError(t, err)
		assert.False(t, prevent)
	})
}

// =====================================================================
// GitHub provider: GitPush
// =====================================================================

func Test_GitHubScmProvider_GitPush(t *testing.T) {
	t.Parallel()

	t.Run("calls git push upstream", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		pushed := false
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "push")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			pushed = true
			return exec.NewRunResult(0, "", ""), nil
		})

		provider := &GitHubScmProvider{
			gitCli: git.NewCli(mockContext.CommandRunner),
		}

		err := provider.GitPush(*mockContext.Context, &gitRepositoryDetails{
			gitProjectPath: t.TempDir(),
		}, "origin", "main")
		require.NoError(t, err)
		assert.True(t, pushed)
	})
}

// =====================================================================
// GitHub provider: configureGitRemote
// =====================================================================

func Test_GitHubScmProvider_configureGitRemote(t *testing.T) {
	t.Parallel()

	t.Run("select error propagates", func(t *testing.T) {
		t.Parallel()

		console := mockinput.NewMockConsole()
		console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "configure your git remote")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, errors.New("user cancelled")
		})

		provider := &GitHubScmProvider{
			console: console,
		}

		_, err := provider.configureGitRemote(t.Context(), t.TempDir(), "origin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user cancelled")
	})
}

// =====================================================================
// GitHub provider: ensureGitHubLogin
// =====================================================================

func Test_ensureGitHubLogin_alreadyLoggedIn(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	// Mock gh --version
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, fmt.Sprintf("gh version %s", github.Version), ""), nil
	})

	// Mock gh auth status -> logged in
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	updated, err := ensureGitHubLogin(*mockContext.Context, "", ghCli, gitCli, github.GitHubHostName, mockContext.Console)
	require.NoError(t, err)
	assert.False(t, updated)
}

// =====================================================================
// GitHub provider: GitHubScmProvider preConfigureCheck
// =====================================================================

func Test_GitHubScmProvider_preConfigureCheck(t *testing.T) {
	t.Parallel()

	t.Run("success when already logged in", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(context.Background())
		setupGithubCliMocksForCov3(mockContext)

		provider := &GitHubScmProvider{
			ghCli:  github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli: git.NewCli(mockContext.CommandRunner),
		}

		updated, err := provider.preConfigureCheck(
			*mockContext.Context, PipelineManagerArgs{}, provisioning.Options{}, "")
		require.NoError(t, err)
		assert.False(t, updated)
	})
}

func setupGithubCliMocksForCov3(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, fmt.Sprintf("gh version %s", github.Version), ""), nil
	})
}

// =====================================================================
// escapeValuesForPipeline - edge cases
// =====================================================================

func Test_escapeValuesForPipeline_jsonSpecialChars(t *testing.T) {
	t.Parallel()

	t.Run("escapes json brackets", func(t *testing.T) {
		t.Parallel()

		values := map[string]string{
			"KEY": `["api://guid"]`,
		}
		escapeValuesForPipeline(values)
		assert.Equal(t, `[\"api://guid\"]`, values["KEY"])
	})

	t.Run("escapes backslash", func(t *testing.T) {
		t.Parallel()

		values := map[string]string{
			"KEY": `path\to\file`,
		}
		escapeValuesForPipeline(values)
		assert.Equal(t, `path\\to\\file`, values["KEY"])
	})

	t.Run("escapes embedded quotes", func(t *testing.T) {
		t.Parallel()

		values := map[string]string{
			"KEY": `say "hello"`,
		}
		escapeValuesForPipeline(values)
		assert.Equal(t, `say \"hello\"`, values["KEY"])
	})

	t.Run("empty map does not panic", func(t *testing.T) {
		t.Parallel()

		values := map[string]string{}
		assert.NotPanics(t, func() {
			escapeValuesForPipeline(values)
		})
	})

	t.Run("plain value is unchanged", func(t *testing.T) {
		t.Parallel()

		values := map[string]string{
			"SIMPLE": "simple-value",
		}
		escapeValuesForPipeline(values)
		assert.Equal(t, "simple-value", values["SIMPLE"])
	})
}

// =====================================================================
// mergeProjectVariablesAndSecrets - additional edge cases
// =====================================================================

func Test_mergeProjectVariablesAndSecrets_escapeApplied(t *testing.T) {
	t.Parallel()

	t.Run("values are escaped for pipeline", func(t *testing.T) {
		t.Parallel()

		env := map[string]string{
			"MY_VAR": `["api://guid"]`,
		}
		vars, _, err := mergeProjectVariablesAndSecrets(
			[]string{"MY_VAR"}, nil,
			map[string]string{}, map[string]string{},
			nil, env)
		require.NoError(t, err)
		// The value should be escaped (brackets with escaped quotes)
		assert.Equal(t, `[\"api://guid\"]`, vars["MY_VAR"])
	})

	t.Run("secrets are escaped for pipeline", func(t *testing.T) {
		t.Parallel()

		env := map[string]string{
			"MY_SEC": `value with "quotes"`,
		}
		_, secrets, err := mergeProjectVariablesAndSecrets(
			nil, []string{"MY_SEC"},
			map[string]string{}, map[string]string{},
			nil, env)
		require.NoError(t, err)
		assert.Equal(t, `value with \"quotes\"`, secrets["MY_SEC"])
	})
}

// =====================================================================
// mergeProjectVariablesAndSecrets - provider params with nil Value
// =====================================================================

func Test_mergeProjectVariablesAndSecrets_nilParamValue(t *testing.T) {
	t.Parallel()

	t.Run("single env var with nil value uses env lookup", func(t *testing.T) {
		t.Parallel()

		params := []provisioning.Parameter{
			{
				Name:          "nullVal",
				Value:         nil,
				Secret:        false,
				LocalPrompt:   false,
				EnvVarMapping: []string{"FROM_ENV"},
			},
		}
		env := map[string]string{
			"FROM_ENV": "envValue",
		}
		vars, _, err := mergeProjectVariablesAndSecrets(
			nil, nil, map[string]string{}, map[string]string{},
			params, env)
		require.NoError(t, err)
		assert.Equal(t, "envValue", vars["FROM_ENV"])
	})
}

// =====================================================================
// generatePipelineDefinition - provider parameter env var injection
// =====================================================================

func Test_generatePipelineDefinition_providerParams(t *testing.T) {
	t.Parallel()

	t.Run("provider param secrets and variables appear in output", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		outPath := filepath.Join(tmpDir, "azure-dev.yml")

		err := generatePipelineDefinition(outPath, projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
			providerParameters: []provisioning.Parameter{
				{
					Name:          "mySecret",
					Secret:        true,
					EnvVarMapping: []string{"SECRET_VAR"},
				},
				{
					Name:          "myVariable",
					Secret:        false,
					EnvVarMapping: []string{"NORMAL_VAR"},
				},
			},
		})
		require.NoError(t, err)

		data, err := os.ReadFile(outPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "SECRET_VAR")
		assert.Contains(t, content, "NORMAL_VAR")
	})
}

// =====================================================================
// generatePipelineDefinition - compose alpha feature
// =====================================================================

func Test_generatePipelineDefinition_alphaFeatures(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "azure-dev.yml")

	err := generatePipelineDefinition(outPath, projectProperties{
		CiProvider:            ciProviderGitHubActions,
		InfraProvider:         infraProviderBicep,
		BranchName:            "main",
		AuthType:              AuthTypeFederated,
		RequiredAlphaFeatures: []string{"compose", "experimental"},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "compose")
	assert.Contains(t, content, "experimental")
}

// =====================================================================
// generatePipelineDefinition - Azure DevOps templates
// =====================================================================

func Test_generatePipelineDefinition_azdoTemplates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		props      projectProperties
		wantSubstr []string
	}{
		{
			name: "azdo with app host and terraform client creds",
			props: projectProperties{
				CiProvider:    ciProviderAzureDevOps,
				InfraProvider: infraProviderTerraform,
				BranchName:    "release",
				AuthType:      AuthTypeClientCredentials,
				HasAppHost:    true,
			},
			wantSubstr: []string{
				"release",
				"AZURE_LOCATION",
				"AZURE_ENV_NAME",
				"AZURE_CLIENT_SECRET",
			},
		},
		{
			name: "azdo with variables and secrets",
			props: projectProperties{
				CiProvider:    ciProviderAzureDevOps,
				InfraProvider: infraProviderBicep,
				BranchName:    "main",
				AuthType:      AuthTypeFederated,
				Variables:     []string{"CUSTOM_VAR1"},
				Secrets:       []string{"CUSTOM_SECRET1"},
			},
			wantSubstr: []string{
				"CUSTOM_VAR1",
				"CUSTOM_SECRET1",
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

			for _, sub := range tt.wantSubstr {
				assert.Contains(t, content, sub,
					"expected %q in generated YAML for %s", sub, tt.name)
			}
		})
	}
}

// =====================================================================
// PipelineConfigResult / CredentialOptions struct field tests
// =====================================================================

func Test_PipelineConfigResult_fields(t *testing.T) {
	t.Parallel()

	result := &PipelineConfigResult{
		RepositoryLink: "https://github.com/test/repo",
		PipelineLink:   "https://github.com/test/repo/actions",
	}
	assert.Equal(t, "https://github.com/test/repo", result.RepositoryLink)
	assert.Equal(t, "https://github.com/test/repo/actions", result.PipelineLink)
}

func Test_CredentialOptions_fields(t *testing.T) {
	t.Parallel()

	opts := &CredentialOptions{
		EnableClientCredentials:    true,
		EnableFederatedCredentials: false,
		FederatedCredentialOptions: []*graphsdk.FederatedIdentityCredential{
			{
				Name:   "test-cred",
				Issuer: "https://token.actions.githubusercontent.com",
			},
		},
	}
	assert.True(t, opts.EnableClientCredentials)
	assert.False(t, opts.EnableFederatedCredentials)
	assert.Len(t, opts.FederatedCredentialOptions, 1)
}

// =====================================================================
// PipelineManagerArgs struct
// =====================================================================

func Test_PipelineManagerArgs_fields(t *testing.T) {
	t.Parallel()

	args := &PipelineManagerArgs{
		PipelineServicePrincipalId:   "sp-id",
		PipelineServicePrincipalName: "sp-name",
		PipelineRemoteName:           "origin",
		PipelineRoleNames:            []string{"Contributor"},
		PipelineProvider:             "github",
		PipelineAuthTypeName:         "federated",
		ServiceManagementReference:   "smr-id",
	}
	assert.Equal(t, "sp-id", args.PipelineServicePrincipalId)
	assert.Equal(t, "sp-name", args.PipelineServicePrincipalName)
	assert.Equal(t, "origin", args.PipelineRemoteName)
	assert.Equal(t, []string{"Contributor"}, args.PipelineRoleNames)
	assert.Equal(t, "github", args.PipelineProvider)
	assert.Equal(t, "federated", args.PipelineAuthTypeName)
	assert.Equal(t, "smr-id", args.ServiceManagementReference)
}

// =====================================================================
// authConfiguration struct
// =====================================================================

func Test_authConfiguration_fields(t *testing.T) {
	t.Parallel()

	orgId := "org-id-123"
	ac := &authConfiguration{
		AzureCredentials: &entraid.AzureCredentials{
			ClientId:       "client",
			TenantId:       "tenant",
			SubscriptionId: "sub",
		},
		sp: &graphsdk.ServicePrincipal{
			AppId:                  "app-id",
			DisplayName:            "test-sp",
			AppOwnerOrganizationId: &orgId,
		},
	}

	assert.Equal(t, "client", ac.ClientId)
	assert.Equal(t, "app-id", ac.sp.AppId)
}

// =====================================================================
// projectProperties struct
// =====================================================================

func Test_projectProperties_fields_cov3(t *testing.T) {
	t.Parallel()

	props := projectProperties{
		CiProvider:            ciProviderGitHubActions,
		InfraProvider:         infraProviderBicep,
		RepoRoot:              "/some/path",
		HasAppHost:            true,
		BranchName:            "dev",
		AuthType:              AuthTypeFederated,
		Variables:             []string{"V1"},
		Secrets:               []string{"S1"},
		RequiredAlphaFeatures: []string{"compose"},
	}

	assert.Equal(t, ciProviderGitHubActions, props.CiProvider)
	assert.Equal(t, infraProviderBicep, props.InfraProvider)
	assert.True(t, props.HasAppHost)
	assert.Equal(t, "dev", props.BranchName)
}

// =====================================================================
// gitRepositoryDetails struct
// =====================================================================

func Test_gitRepositoryDetails_fields(t *testing.T) {
	t.Parallel()

	details := &gitRepositoryDetails{
		owner:          "azure",
		repoName:       "azure-dev",
		gitProjectPath: "/path/to/project",
		pushStatus:     true,
		remote:         "git@github.com:azure/azure-dev.git",
		url:            "https://github.com/azure/azure-dev",
		branch:         "main",
		details:        "extra-details",
	}

	assert.Equal(t, "azure", details.owner)
	assert.Equal(t, "azure-dev", details.repoName)
	assert.True(t, details.pushStatus)
	assert.Equal(t, "main", details.branch)
	assert.Equal(t, "extra-details", details.details)
}

// =====================================================================
// configurePipelineOptions struct
// =====================================================================

func Test_configurePipelineOptions_fields(t *testing.T) {
	t.Parallel()

	opts := &configurePipelineOptions{
		provisioningProvider: &provisioning.Options{
			Provider: provisioning.Bicep,
		},
		secrets:            map[string]string{"SEC1": "val"},
		variables:          map[string]string{"VAR1": "val"},
		projectVariables:   []string{"PV1"},
		projectSecrets:     []string{"PS1"},
		providerParameters: []provisioning.Parameter{{Name: "p1"}},
	}

	assert.Equal(t, provisioning.Bicep, opts.provisioningProvider.Provider)
	assert.Len(t, opts.secrets, 1)
	assert.Len(t, opts.variables, 1)
}

// =====================================================================
// servicePrincipalResult struct
// =====================================================================

func Test_servicePrincipalResult_fields(t *testing.T) {
	t.Parallel()

	result := &servicePrincipalResult{
		appIdOrName:     "my-app",
		applicationName: "My Application",
		lookupKind:      lookupKindPrincipalId,
		servicePrincipal: &graphsdk.ServicePrincipal{
			AppId: "app-123",
		},
	}

	assert.Equal(t, "my-app", result.appIdOrName)
	assert.Equal(t, lookupKindPrincipalId, result.lookupKind)
	assert.NotNil(t, result.servicePrincipal)
}

// =====================================================================
// servicePrincipal - edge case: principal-id takes priority over name
// =====================================================================

func Test_servicePrincipal_priorityOrder(t *testing.T) {
	t.Parallel()

	t.Run("principal-id takes priority over name and env", func(t *testing.T) {
		t.Parallel()

		sp := &graphsdk.ServicePrincipal{
			AppId:       "app-from-id",
			DisplayName: "sp-from-id",
		}
		svc := &mockEntraIdService3{getSpResult: sp}

		result, err := servicePrincipal(t.Context(), "env-client", "sub-1",
			&PipelineManagerArgs{
				PipelineServicePrincipalId:   "explicit-id",
				PipelineServicePrincipalName: "explicit-name",
			}, svc)
		require.NoError(t, err)
		assert.Equal(t, lookupKindPrincipalId, result.lookupKind)
		assert.Equal(t, "app-from-id", result.appIdOrName)
	})

	t.Run("name takes priority over env when no id", func(t *testing.T) {
		t.Parallel()

		sp := &graphsdk.ServicePrincipal{
			AppId:       "app-from-name",
			DisplayName: "sp-from-name",
		}
		svc := &mockEntraIdService3{getSpResult: sp}

		result, err := servicePrincipal(t.Context(), "env-client", "sub-1",
			&PipelineManagerArgs{
				PipelineServicePrincipalName: "explicit-name",
			}, svc)
		require.NoError(t, err)
		assert.Equal(t, lookupKindPrincipleName, result.lookupKind)
	})
}

type mockEntraIdService3 struct {
	entraid.EntraIdService
	getSpResult *graphsdk.ServicePrincipal
	getSpErr    error
}

func (m *mockEntraIdService3) GetServicePrincipal(
	_ context.Context, _, _ string,
) (*graphsdk.ServicePrincipal, error) {
	return m.getSpResult, m.getSpErr
}

// =====================================================================
// AzDo provider: AzdoRepositoryDetails struct
// =====================================================================

func Test_AzdoRepositoryDetails_fields(t *testing.T) {
	t.Parallel()

	details := &AzdoRepositoryDetails{
		projectName: "project1",
		projectId:   "proj-id",
		repoId:      "repo-id",
		orgName:     "my-org",
		repoName:    "my-repo",
		repoWebUrl:  "https://dev.azure.com/org/project/_git/repo",
		remoteUrl:   "https://org@dev.azure.com/org/project/_git/repo",
		sshUrl:      "git@ssh.dev.azure.com:v3/org/project/repo",
	}

	assert.Equal(t, "project1", details.projectName)
	assert.Equal(t, "proj-id", details.projectId)
	assert.Equal(t, "repo-id", details.repoId)
	assert.Equal(t, "my-org", details.orgName)
}

// =====================================================================
// AzDo provider: getRepoDetails
// =====================================================================

func Test_AzdoScmProvider_getRepoDetails(t *testing.T) {
	t.Parallel()

	t.Run("initializes repoDetails when nil", func(t *testing.T) {
		t.Parallel()

		provider := &AzdoScmProvider{
			env: environment.New("test"),
		}

		details := provider.getRepoDetails()
		require.NotNil(t, details)
		assert.NotNil(t, provider.repoDetails)
	})

	t.Run("returns existing repoDetails", func(t *testing.T) {
		t.Parallel()

		existing := &AzdoRepositoryDetails{projectName: "existing"}
		provider := &AzdoScmProvider{
			env:         environment.New("test"),
			repoDetails: existing,
		}

		details := provider.getRepoDetails()
		assert.Equal(t, "existing", details.projectName)
	})
}

// =====================================================================
// AzDo provider: preventGitPush
// =====================================================================

func Test_AzdoScmProvider_preventGitPush_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoScmProvider{}
	prevent, err := provider.preventGitPush(t.Context(), &gitRepositoryDetails{}, "origin", "main")
	require.NoError(t, err)
	assert.False(t, prevent, "azdo never prevents git push")
}

// =====================================================================
// AzDo CI provider: preConfigureCheck with client-credentials
// =====================================================================

func Test_AzdoCiProvider_preConfigureCheck_clientCredentials(t *testing.T) {
	t.Parallel()

	t.Run("client-credentials with all env values preset returns no error", func(t *testing.T) {
		t.Parallel()

		testConsole := mockinput.NewMockConsole()

		// Set both PAT and org name in the dotenv so no prompts are needed.
		// EnsurePatExists calls os.Setenv which is process-global and non-deterministic
		// in parallel tests, so we avoid relying on the prompt path.
		env := environment.NewWithValues(
			"test-env",
			map[string]string{
				"AZURE_DEVOPS_EXT_PAT":            "testPAT12345",
				"AZURE_DEVOPS_ORG_NAME":           "fake_org",
				"AZURE_DEVOPS_PROJECT_NAME":       "project1",
				"AZURE_DEVOPS_PROJECT_ID":         "12345",
				"AZURE_DEVOPS_REPOSITORY_NAME":    "repo1",
				"AZURE_DEVOPS_REPOSITORY_ID":      "9876",
				"AZURE_DEVOPS_REPOSITORY_WEB_URL": "https://repo",
			},
		)

		provider := &AzdoCiProvider{
			Env:     env,
			console: testConsole,
		}

		updated, err := provider.preConfigureCheck(t.Context(), PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeClientCredentials),
		}, provisioning.Options{}, "")
		require.NoError(t, err)
		// Both PAT and org name found in env, so nothing was "updated" via prompt
		require.False(t, updated)
	})

	t.Run("federated auth type returns error", func(t *testing.T) {
		t.Parallel()

		testConsole := mockinput.NewMockConsole()

		env := environment.NewWithValues("test-env", map[string]string{
			"AZURE_DEVOPS_EXT_PAT":  "testPAT",
			"AZURE_DEVOPS_ORG_NAME": "myorg",
		})

		provider := &AzdoCiProvider{
			Env:     env,
			console: testConsole,
		}

		_, err := provider.preConfigureCheck(t.Context(), PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeFederated),
		}, provisioning.Options{}, "")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAuthNotSupported)
		require.Contains(t, err.Error(), "does not support federated")
	})
}

// =====================================================================
// AzDo CI provider: credentialOptions - client-credentials
// =====================================================================

func Test_AzdoCiProvider_credentialOptions_clientCreds(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}

	opts, err := provider.credentialOptions(t.Context(),
		&gitRepositoryDetails{},
		provisioning.Options{},
		AuthTypeClientCredentials,
		&entraid.AzureCredentials{})
	require.NoError(t, err)
	assert.True(t, opts.EnableClientCredentials)
	assert.False(t, opts.EnableFederatedCredentials)
}

// =====================================================================
// AzDo pipeline url construction
// =====================================================================

func Test_azdoPipeline_url_construction(t *testing.T) {
	t.Parallel()

	defId := 99
	defName := "build-pipeline"
	p := &pipeline{
		repoDetails: &AzdoRepositoryDetails{
			repoWebUrl: "https://dev.azure.com/myorg/myproject/_git/myrepo",
			buildDefinition: &build.BuildDefinition{
				Name: &defName,
				Id:   &defId,
			},
		},
	}

	assert.Equal(t, "build-pipeline", p.name())
	assert.Equal(t, "https://dev.azure.com/myorg/myproject/_build?definitionId=99", p.url())
}

// =====================================================================
// parseAzDoRemote - additional non-standard host tests
// =====================================================================

func Test_parseAzDoRemote_nonStandardHost(t *testing.T) {
	t.Parallel()

	t.Run("self-hosted with _git is non-standard", func(t *testing.T) {
		t.Parallel()

		result, err := parseAzDoRemote("https://devops.mycompany.com/Collection/MyProject/_git/MyRepo")
		require.NoError(t, err)
		assert.True(t, result.IsNonStandardHost)
		assert.Equal(t, "MyProject", result.Project)
		assert.Equal(t, "MyRepo", result.RepositoryName)
	})

	t.Run("git@ non-standard host fails", func(t *testing.T) {
		t.Parallel()

		_, err := parseAzDoRemote("git@devops.mycompany.com:v3/org/project/repo")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not an Azure DevOps")
	})

	t.Run("multiple _git substrings", func(t *testing.T) {
		t.Parallel()

		_, err := parseAzDoRemote("https://dev.azure.com/org/project/_git/repo/_git/extra")
		require.Error(t, err)
	})
}

// =====================================================================
// GitHub credentialOptions - additional edge cases
// =====================================================================

func Test_GitHubCiProvider_credentialOptions_branchSpecialChars(t *testing.T) {
	t.Parallel()

	provider := &GitHubCiProvider{}

	opts, err := provider.credentialOptions(t.Context(),
		&gitRepositoryDetails{
			owner:    "my.org",
			repoName: "my.repo",
			branch:   "feat/my-feature.v2",
		},
		provisioning.Options{},
		AuthTypeFederated,
		&entraid.AzureCredentials{})
	require.NoError(t, err)
	assert.True(t, opts.EnableFederatedCredentials)
	// Should have pull_request + feat/my-feature.v2 + main = 3
	require.Len(t, opts.FederatedCredentialOptions, 3)

	// Check credential names are sanitized (no dots or slashes)
	for _, cred := range opts.FederatedCredentialOptions {
		assert.NotContains(t, cred.Name, ".")
		assert.NotContains(t, cred.Name, "/")
	}
}

// =====================================================================
// GitHub CI provider: requiredTools
// =====================================================================

func Test_GitHubCiProvider_requiredTools_cov3(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	provider := &GitHubCiProvider{
		ghCli: github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
	}

	result, err := provider.requiredTools(t.Context())
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// =====================================================================
// GitHub SCM provider: requiredTools
// =====================================================================

func Test_GitHubScmProvider_requiredTools_cov3(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	provider := &GitHubScmProvider{
		ghCli: github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
	}

	result, err := provider.requiredTools(t.Context())
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// =====================================================================
// AzDo SCM provider: requiredTools (empty)
// =====================================================================

func Test_AzdoScmProvider_requiredTools_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoScmProvider{}
	result, err := provider.requiredTools(t.Context())
	require.NoError(t, err)
	assert.Empty(t, result)
}

// =====================================================================
// AzDo CI provider: requiredTools (empty)
// =====================================================================

func Test_AzdoCiProvider_requiredTools_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}
	result, err := provider.requiredTools(t.Context())
	require.NoError(t, err)
	assert.Empty(t, result)
}

// =====================================================================
// Error variable checks
// =====================================================================

func Test_ErrorVariables(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, ErrAuthNotSupported)
	assert.Contains(t, ErrAuthNotSupported.Error(), "not supported")

	assert.Equal(t, []string{"Contributor", "User Access Administrator"}, DefaultRoleNames)
}

// =====================================================================
// Env persisted key constant
// =====================================================================

func Test_EnvPersistedKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "AZD_PIPELINE_PROVIDER", envPersistedKey)
}

// =====================================================================
// resolveSmr - nil configs
// =====================================================================

func Test_resolveSmr_nilConfigHandling(t *testing.T) {
	t.Parallel()

	t.Run("empty arg with empty configs returns nil", func(t *testing.T) {
		t.Parallel()

		result := resolveSmr("", config.NewEmptyConfig(), config.NewEmptyConfig())
		assert.Nil(t, result)
	})

	t.Run("arg always takes priority", func(t *testing.T) {
		t.Parallel()

		projCfg := config.NewConfig(nil)
		_ = projCfg.Set("pipeline.config.applicationServiceManagementReference", "proj-val")
		userCfg := config.NewConfig(nil)
		_ = userCfg.Set("pipeline.config.applicationServiceManagementReference", "user-val")

		result := resolveSmr("arg-val", projCfg, userCfg)
		require.NotNil(t, result)
		assert.Equal(t, "arg-val", *result)
	})
}

// =====================================================================
// Azure DevOps CI provider: configureConnection - federated path
// =====================================================================

func Test_AzdoCiProvider_configureConnection_federated(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}

	err := provider.configureConnection(t.Context(),
		&gitRepositoryDetails{
			details: &AzdoRepositoryDetails{},
		},
		provisioning.Options{},
		&authConfiguration{
			AzureCredentials: &entraid.AzureCredentials{},
		},
		&CredentialOptions{
			EnableFederatedCredentials: true,
		})
	require.NoError(t, err)
}

// =====================================================================
// GitHub provider: Name() methods
// =====================================================================

func Test_GitHubProviders_Name(t *testing.T) {
	t.Parallel()

	scm := &GitHubScmProvider{}
	assert.Equal(t, "GitHub", scm.Name())

	ci := &GitHubCiProvider{}
	assert.Equal(t, "GitHub", ci.Name())
}

// =====================================================================
// AzDo provider: Name() methods
// =====================================================================

func Test_AzdoProviders_Name(t *testing.T) {
	t.Parallel()

	scm := &AzdoScmProvider{}
	assert.Equal(t, "Azure DevOps", scm.Name())

	ci := &AzdoCiProvider{}
	assert.Equal(t, "Azure DevOps", ci.Name())
}

// =====================================================================
// selectRemoteUrl
// =====================================================================
func Test_selectRemoteUrl_cov3(t *testing.T) {
	t.Parallel()

	repo := github.GhCliRepository{
		HttpsUrl:      "https://github.com/owner/repo.git",
		SshUrl:        "git@github.com:owner/repo.git",
		NameWithOwner: "owner/repo",
	}

	t.Run("https protocol", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "git_protocol")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "https", ""), nil
		})

		ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
		url, err := selectRemoteUrl(*mockContext.Context, ghCli, repo)
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/owner/repo.git", url)
	})

	t.Run("ssh protocol", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "git_protocol")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "ssh", ""), nil
		})

		ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
		url, err := selectRemoteUrl(*mockContext.Context, ghCli, repo)
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:owner/repo.git", url)
	})

	t.Run("error getting protocol", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "git_protocol")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "failed"), errors.New("command failed")
		})

		ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
		_, err := selectRemoteUrl(*mockContext.Context, ghCli, repo)
		require.Error(t, err)
	})
}

// =====================================================================
// getRemoteUrlFromPrompt
// =====================================================================
func Test_getRemoteUrlFromPrompt_cov3(t *testing.T) {
	t.Parallel()

	t.Run("valid github url", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("https://github.com/testowner/testrepo")

		url, err := getRemoteUrlFromPrompt(*mockContext.Context, "origin", mockContext.Console)
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/testowner/testrepo", url)
	})

	t.Run("error from prompt", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).RespondFn(
			func(options input.ConsoleOptions) (any, error) {
				return "", errors.New("user cancelled")
			},
		)

		_, err := getRemoteUrlFromPrompt(*mockContext.Context, "origin", mockContext.Console)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompting for remote url")
	})
}

// =====================================================================
// gitInsteadOfConfig
// =====================================================================
func Test_gitInsteadOfConfig_cov3(t *testing.T) {
	t.Parallel()

	details := &gitRepositoryDetails{
		details: &AzdoRepositoryDetails{
			orgName: "myorg",
		},
	}

	remoteAndPatUrl, originalUrl := gitInsteadOfConfig("my-pat-token", details)
	assert.Equal(t, fmt.Sprintf("url.https://my-pat-token@%s/", azdo.AzDoHostName), remoteAndPatUrl)
	assert.Equal(t, fmt.Sprintf("https://myorg@%s/", azdo.AzDoHostName), originalUrl)
}

// =====================================================================
// azdoPat
// =====================================================================
func Test_azdoPat_cov3(t *testing.T) {
	t.Parallel()

	t.Run("pat from env", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("test-env", map[string]string{
			azdo.AzDoPatName:            "stored-pat-value",
			azdo.AzDoEnvironmentOrgName: "myorg",
		})

		pat := azdoPat(*mockContext.Context, env, mockContext.Console)
		assert.Equal(t, "stored-pat-value", pat)
	})
}

// =====================================================================
// getCurrentGitBranch
// =====================================================================
func Test_getCurrentGitBranch_cov3(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "feature-branch\n", ""), nil
		})

		provider := &AzdoScmProvider{
			gitCli: git.NewCli(mockContext.CommandRunner),
		}

		branch, err := provider.getCurrentGitBranch(*mockContext.Context, "/some/path")
		require.NoError(t, err)
		assert.Equal(t, "feature-branch", branch)
	})

	t.Run("error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "branch")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "not a repo"), errors.New("not a git repo")
		})

		provider := &AzdoScmProvider{
			gitCli: git.NewCli(mockContext.CommandRunner),
		}

		_, err := provider.getCurrentGitBranch(*mockContext.Context, "/bad/path")
		require.Error(t, err)
	})
}

// =====================================================================
// hasPipelineFile
// =====================================================================
func Test_hasPipelineFile_cov3(t *testing.T) {
	t.Parallel()

	t.Run("github file exists", func(t *testing.T) {
		dir := t.TempDir()
		ghDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("trigger:"), 0600))

		assert.True(t, hasPipelineFile(ciProviderGitHubActions, dir))
	})

	t.Run("azdo file exists", func(t *testing.T) {
		dir := t.TempDir()
		azdoDir := filepath.Join(dir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(azdoDir, "azure-dev.yml"), []byte("trigger:"), 0600))

		assert.True(t, hasPipelineFile(ciProviderAzureDevOps, dir))
	})

	t.Run("no pipeline file", func(t *testing.T) {
		dir := t.TempDir()
		assert.False(t, hasPipelineFile(ciProviderGitHubActions, dir))
	})
}

// =====================================================================
// promptForServiceTreeId
// =====================================================================
func Test_promptForServiceTreeId_cov3(t *testing.T) {
	t.Parallel()

	t.Run("valid uuid first attempt", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		validUUID := "12345678-1234-1234-1234-123456789abc"
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond(validUUID)

		pm := &PipelineManager{console: mockContext.Console}
		result, err := pm.promptForServiceTreeId(*mockContext.Context, promptForServiceTreeIdOptions{})
		require.NoError(t, err)
		assert.Equal(t, validUUID, result)
	})

	t.Run("with previous invalid shows message", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		validUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond(validUUID)

		pm := &PipelineManager{console: mockContext.Console}
		result, err := pm.promptForServiceTreeId(*mockContext.Context, promptForServiceTreeIdOptions{
			PreviousWasInvalid: "bad-value was not a valid uuid",
		})
		require.NoError(t, err)
		assert.Equal(t, validUUID, result)
	})

	t.Run("prompt error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).RespondFn(
			func(options input.ConsoleOptions) (any, error) {
				return "", errors.New("cancelled")
			},
		)

		pm := &PipelineManager{console: mockContext.Console}
		_, err := pm.promptForServiceTreeId(*mockContext.Context, promptForServiceTreeIdOptions{})
		require.Error(t, err)
	})
}

// =====================================================================
// determineProvider
// =====================================================================
func Test_determineProvider_cov3(t *testing.T) {
	t.Parallel()

	t.Run("only github yaml", func(t *testing.T) {
		dir := t.TempDir()
		ghDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))

		mockContext := mocks.NewMockContext(context.Background())
		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.determineProvider(*mockContext.Context, dir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("only azdo yaml", func(t *testing.T) {
		dir := t.TempDir()
		azdoDir := filepath.Join(dir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(azdoDir, "azure-dev.yml"), []byte("trigger:"), 0600))

		mockContext := mocks.NewMockContext(context.Background())
		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.determineProvider(*mockContext.Context, dir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})

	t.Run("neither yaml prompts user for github", func(t *testing.T) {
		dir := t.TempDir()
		mockContext := mocks.NewMockContext(context.Background())
		// select index 0 = GitHub Actions
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.determineProvider(*mockContext.Context, dir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("both yaml prompts user for azdo", func(t *testing.T) {
		dir := t.TempDir()
		ghDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))
		azdoDir := filepath.Join(dir, ".azdo", "pipelines")
		require.NoError(t, os.MkdirAll(azdoDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(azdoDir, "azure-dev.yml"), []byte("trigger:"), 0600))

		mockContext := mocks.NewMockContext(context.Background())
		// select index 1 = Azure DevOps
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)

		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.determineProvider(*mockContext.Context, dir)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})
}

// =====================================================================
// promptForProvider
// =====================================================================
func Test_promptForProvider_cov3(t *testing.T) {
	t.Parallel()

	t.Run("select github", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.promptForProvider(*mockContext.Context)
		require.NoError(t, err)
		assert.Equal(t, ciProviderGitHubActions, provider)
	})

	t.Run("select azdo", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)

		pm := &PipelineManager{console: mockContext.Console}
		provider, err := pm.promptForProvider(*mockContext.Context)
		require.NoError(t, err)
		assert.Equal(t, ciProviderAzureDevOps, provider)
	})

	t.Run("prompt error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).RespondFn(
			func(options input.ConsoleOptions) (any, error) {
				return 0, errors.New("interrupted")
			},
		)

		pm := &PipelineManager{console: mockContext.Console}
		_, err := pm.promptForProvider(*mockContext.Context)
		require.Error(t, err)
	})
}

// =====================================================================
// StoreRepoDetails
// =====================================================================
func Test_StoreRepoDetails_cov3(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("test-env", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

		repoId := uuid.MustParse("11111111-2222-3333-4444-555555555555")
		repoName := "my-repo"
		remoteUrl := "https://dev.azure.com/myorg/myproject/_git/my-repo"
		webUrl := "https://dev.azure.com/myorg/myproject/_git/my-repo"
		sshUrl := "git@ssh.dev.azure.com:v3/myorg/myproject/my-repo"

		gitRepo := &azdoGit.GitRepository{
			Name:      &repoName,
			RemoteUrl: &remoteUrl,
			WebUrl:    &webUrl,
			SshUrl:    &sshUrl,
			Id:        &repoId,
		}

		provider := &AzdoScmProvider{
			env:        env,
			envManager: envManager,
		}

		err := provider.StoreRepoDetails(*mockContext.Context, gitRepo)
		require.NoError(t, err)
		envManager.AssertNumberOfCalls(t, "Save", 3) // repoId, repoName, repoWebUrl
	})

	t.Run("save error on first call", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("test-env", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, mock.Anything).Return(errors.New("disk full"))

		repoId := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
		repoName := "fail-repo"
		remoteUrl := "https://dev.azure.com/org/proj/_git/fail-repo"
		webUrl := "https://dev.azure.com/org/proj/_git/fail-repo"
		sshUrl := "git@ssh.dev.azure.com:v3/org/proj/fail-repo"

		gitRepo := &azdoGit.GitRepository{
			Name:      &repoName,
			RemoteUrl: &remoteUrl,
			WebUrl:    &webUrl,
			SshUrl:    &sshUrl,
			Id:        &repoId,
		}

		provider := &AzdoScmProvider{
			env:        env,
			envManager: envManager,
		}

		err := provider.StoreRepoDetails(*mockContext.Context, gitRepo)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error saving repo id")
	})
}

// =====================================================================
// setPipelineVariables
// =====================================================================
func Test_setPipelineVariables_cov3(t *testing.T) {
	t.Parallel()

	t.Run("basic bicep variables", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Accept all gh variable set and secret set commands
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "dev",
			environment.LocationEnvVarName:       "eastus2",
			environment.SubscriptionIdEnvVarName: "sub-123",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.setPipelineVariables(
			*mockContext.Context, "owner/repo",
			provisioning.Options{Provider: provisioning.Bicep},
			"tenant-id", "client-id",
		)
		require.NoError(t, err)
	})

	t.Run("bicep with resource group", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "staging",
			environment.LocationEnvVarName:       "westus2",
			environment.SubscriptionIdEnvVarName: "sub-456",
			environment.ResourceGroupEnvVarName:  "my-rg",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.setPipelineVariables(
			*mockContext.Context, "owner/repo",
			provisioning.Options{Provider: provisioning.Bicep},
			"tenant-id", "client-id",
		)
		require.NoError(t, err)
	})

	t.Run("terraform variables", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "prod",
			environment.LocationEnvVarName:       "centralus",
			environment.SubscriptionIdEnvVarName: "sub-789",
			"RS_RESOURCE_GROUP":                  "tf-state-rg",
			"RS_STORAGE_ACCOUNT":                 "tfstateacct",
			"RS_CONTAINER_NAME":                  "tfstate",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.setPipelineVariables(
			*mockContext.Context, "owner/repo",
			provisioning.Options{Provider: provisioning.Terraform},
			"tenant-id", "client-id",
		)
		require.NoError(t, err)
	})

	t.Run("terraform missing RS variable", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "test",
			environment.LocationEnvVarName:       "westus",
			environment.SubscriptionIdEnvVarName: "sub-000",
			// Missing RS_RESOURCE_GROUP
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.setPipelineVariables(
			*mockContext.Context, "owner/repo",
			provisioning.Options{Provider: provisioning.Terraform},
			"tenant-id", "client-id",
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terraform remote state is not correctly configured")
	})

	t.Run("set variable error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "auth error"), errors.New("auth failed")
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "dev",
			environment.LocationEnvVarName:       "eastus",
			environment.SubscriptionIdEnvVarName: "sub-x",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.setPipelineVariables(
			*mockContext.Context, "owner/repo",
			provisioning.Options{Provider: provisioning.Bicep},
			"t", "c",
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed setting")
	})
}

// =====================================================================
// configureClientCredentialsAuth
// =====================================================================
func Test_configureClientCredentialsAuth_cov3(t *testing.T) {
	t.Parallel()

	t.Run("bicep basic", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "secret") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{})
		creds := &entraid.AzureCredentials{
			TenantId:     "tid",
			ClientId:     "cid",
			ClientSecret: "csecret",
		}

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.configureClientCredentialsAuth(
			*mockContext.Context,
			provisioning.Options{Provider: provisioning.Bicep},
			"owner/repo",
			creds,
		)
		require.NoError(t, err)
	})

	t.Run("terraform sets extra vars and secrets", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Accept both secrets and variables
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return (strings.Contains(command, "secret") || strings.Contains(command, "variable")) &&
				strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{})
		creds := &entraid.AzureCredentials{
			TenantId:     "tenant-123",
			ClientId:     "client-456",
			ClientSecret: "secret-789",
		}

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.configureClientCredentialsAuth(
			*mockContext.Context,
			provisioning.Options{Provider: provisioning.Terraform},
			"owner/repo",
			creds,
		)
		require.NoError(t, err)
	})

	t.Run("set secret error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "secret") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error"), errors.New("secret set failed")
		})

		env := environment.NewWithValues("test-env", map[string]string{})
		creds := &entraid.AzureCredentials{
			TenantId:     "t",
			ClientId:     "c",
			ClientSecret: "s",
		}

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		err := provider.configureClientCredentialsAuth(
			*mockContext.Context,
			provisioning.Options{Provider: provisioning.Bicep},
			"owner/repo",
			creds,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed setting")
	})
}

// =====================================================================
// configureConnection
// =====================================================================
func Test_configureConnection_cov3(t *testing.T) {
	t.Parallel()

	t.Run("federated only", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "variable") && strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "dev",
			environment.LocationEnvVarName:       "eastus",
			environment.SubscriptionIdEnvVarName: "sub-id",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		repoDetails := &gitRepositoryDetails{
			owner:    "owner",
			repoName: "repo",
		}

		err := provider.configureConnection(
			*mockContext.Context,
			repoDetails,
			provisioning.Options{Provider: provisioning.Bicep},
			&authConfiguration{AzureCredentials: &entraid.AzureCredentials{TenantId: "t1", ClientId: "c1"}},
			&CredentialOptions{EnableClientCredentials: false},
		)
		require.NoError(t, err)
	})

	t.Run("with client credentials", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return (strings.Contains(command, "variable") || strings.Contains(command, "secret")) &&
				strings.Contains(command, "set")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		env := environment.NewWithValues("test-env", map[string]string{
			environment.EnvNameEnvVarName:        "dev",
			environment.LocationEnvVarName:       "eastus",
			environment.SubscriptionIdEnvVarName: "sub-id",
		})

		provider := &GitHubCiProvider{
			env:     env,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			console: mockContext.Console,
		}

		repoDetails := &gitRepositoryDetails{
			owner:    "owner",
			repoName: "repo",
		}

		err := provider.configureConnection(
			*mockContext.Context,
			repoDetails,
			provisioning.Options{Provider: provisioning.Bicep},
			&authConfiguration{
				AzureCredentials: &entraid.AzureCredentials{TenantId: "t1", ClientId: "c1", ClientSecret: "s1"},
			},
			&CredentialOptions{EnableClientCredentials: true},
		)
		require.NoError(t, err)
	})
}

// =====================================================================
// notifyWhenGitHubActionsAreDisabled
// =====================================================================
func Test_notifyWhenGitHubActionsAreDisabled_cov3(t *testing.T) {
	t.Parallel()

	t.Run("actions already enabled upstream", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "actions/workflows")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, `{"total_count": 3}`, ""), nil
		})

		provider := &GitHubScmProvider{
			console: mockContext.Console,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli:  git.NewCli(mockContext.CommandRunner),
		}

		cancelled, err := provider.notifyWhenGitHubActionsAreDisabled(
			*mockContext.Context, t.TempDir(), "owner/repo",
		)
		require.NoError(t, err)
		assert.False(t, cancelled)
	})

	t.Run("no upstream actions and no local workflows", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "actions/workflows")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, `{"total_count": 0}`, ""), nil
		})

		dir := t.TempDir()
		wfDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(wfDir, os.ModePerm))

		provider := &GitHubScmProvider{
			console: mockContext.Console,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli:  git.NewCli(mockContext.CommandRunner),
		}

		cancelled, err := provider.notifyWhenGitHubActionsAreDisabled(
			*mockContext.Context, dir, "owner/repo",
		)
		require.NoError(t, err)
		assert.False(t, cancelled)
	})

	t.Run("no upstream actions with local tracked workflow user continues", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "actions/workflows")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, `{"total_count": 0}`, ""), nil
		})
		// Mock git status for IsUntrackedFile - empty output means file IS tracked
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "status") && strings.Contains(command, ".yml")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		dir := t.TempDir()
		wfDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(wfDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte("on: push"), 0600))

		// user picks "manual enable" choice (index 0)
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

		provider := &GitHubScmProvider{
			console: mockContext.Console,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli:  git.NewCli(mockContext.CommandRunner),
		}

		cancelled, err := provider.notifyWhenGitHubActionsAreDisabled(
			*mockContext.Context, dir, "owner/repo",
		)
		require.NoError(t, err)
		assert.False(t, cancelled)
	})

	t.Run("no upstream actions with local tracked workflow user cancels", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "actions/workflows")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, `{"total_count": 0}`, ""), nil
		})
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "status") && strings.Contains(command, ".yaml")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		dir := t.TempDir()
		wfDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(wfDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(wfDir, "deploy.yaml"), []byte("trigger:"), 0600))

		// user picks "cancel" choice (index 1)
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)

		provider := &GitHubScmProvider{
			console: mockContext.Console,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli:  git.NewCli(mockContext.CommandRunner),
		}

		cancelled, err := provider.notifyWhenGitHubActionsAreDisabled(
			*mockContext.Context, dir, "owner/repo",
		)
		require.NoError(t, err)
		assert.True(t, cancelled)
	})

	t.Run("gh api error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "actions/workflows")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "not found"), errors.New("gh api failed")
		})

		provider := &GitHubScmProvider{
			console: mockContext.Console,
			ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
			gitCli:  git.NewCli(mockContext.CommandRunner),
		}

		_, err := provider.notifyWhenGitHubActionsAreDisabled(
			*mockContext.Context, t.TempDir(), "owner/repo",
		)
		require.Error(t, err)
	})
}

// =====================================================================
// pushGitRepo
// =====================================================================
func Test_pushGitRepo_cov3(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)

		// Mock git add
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "add") && strings.Contains(command, ".")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})
		// Mock git commit
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "commit")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

		gitPushCalled := false
		scm := &mockScmProvider{
			nameFn: func() string { return "GitHub" },
			gitPushFn: func(
				ctx context.Context, repoDetails *gitRepositoryDetails,
				remoteName string, branchName string,
			) error {
				gitPushCalled = true
				return nil
			},
		}

		pm := &PipelineManager{
			azdCtx:      azdCtx,
			gitCli:      git.NewCli(mockContext.CommandRunner),
			scmProvider: scm,
			args:        &PipelineManagerArgs{PipelineRemoteName: "origin"},
		}

		repoInfo := &gitRepositoryDetails{
			owner:    "owner",
			repoName: "repo",
		}

		err := pm.pushGitRepo(*mockContext.Context, repoInfo, "main")
		require.NoError(t, err)
		assert.True(t, gitPushCalled)
	})

	t.Run("add file error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "add")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error"), errors.New("add failed")
		})

		pm := &PipelineManager{
			azdCtx:      azdCtx,
			gitCli:      git.NewCli(mockContext.CommandRunner),
			scmProvider: &mockScmProvider{nameFn: func() string { return "GH" }},
			args:        &PipelineManagerArgs{PipelineRemoteName: "origin"},
		}

		err := pm.pushGitRepo(*mockContext.Context, &gitRepositoryDetails{}, "main")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "adding files")
	})
}

// =====================================================================
// promptForCiFiles
// =====================================================================
func Test_promptForCiFiles_cov3(t *testing.T) {
	t.Parallel()

	t.Run("user confirms file creation", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)

		dir := t.TempDir()
		pm := &PipelineManager{console: mockContext.Console}

		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   dir,
		}

		err := pm.promptForCiFiles(*mockContext.Context, props)
		require.NoError(t, err)

		// Verify the file was created
		ghDir := filepath.Join(dir, ".github", "workflows")
		assert.True(t, fileExists(filepath.Join(ghDir, "azure-dev.yml")))
	})

	t.Run("user declines file creation", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(false)

		dir := t.TempDir()
		pm := &PipelineManager{console: mockContext.Console}

		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   dir,
		}

		err := pm.promptForCiFiles(*mockContext.Context, props)
		require.NoError(t, err)

		// Verify no file was created
		ghDir := filepath.Join(dir, ".github", "workflows")
		assert.False(t, fileExists(filepath.Join(ghDir, "azure-dev.yml")))
	})

	t.Run("confirm error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).RespondFn(
			func(options input.ConsoleOptions) (any, error) {
				return false, errors.New("input error")
			},
		)

		dir := t.TempDir()
		pm := &PipelineManager{console: mockContext.Console}

		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   dir,
		}

		err := pm.promptForCiFiles(*mockContext.Context, props)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompting to create file")
	})
}

// fileExists is a simple test helper to check file existence
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// =====================================================================
// getGitRepoDetails - test ensureRemote success path
// =====================================================================
func Test_getGitRepoDetails_successPath_cov3(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	tempDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)

	// mock ensureRemote: git remote get-url returns a valid url
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https://github.com/owner/repo.git", ""), nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
	})

	scm := &mockScmProvider{
		nameFn: func() string { return "GitHub" },
		gitRepoDetailsFn: func(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
			return &gitRepositoryDetails{
				owner:    "owner",
				repoName: "repo",
				remote:   remoteUrl,
				url:      "https://github.com/owner/repo",
			}, nil
		},
	}

	pm := &PipelineManager{
		azdCtx:        azdCtx,
		gitCli:        git.NewCli(mockContext.CommandRunner),
		console:       mockContext.Console,
		scmProvider:   scm,
		args:          &PipelineManagerArgs{PipelineRemoteName: "origin"},
		importManager: project.NewImportManager(nil),
		prjConfig:     &project.ProjectConfig{},
	}

	details, err := pm.getGitRepoDetails(*mockContext.Context)
	require.NoError(t, err)
	assert.Equal(t, "owner", details.owner)
	assert.Equal(t, "repo", details.repoName)
}

// =====================================================================
// Additional edge case: authConfiguration field access
// =====================================================================
func Test_authConfiguration_fields_cov3(t *testing.T) {
	t.Parallel()

	auth := &authConfiguration{
		AzureCredentials: &entraid.AzureCredentials{
			TenantId:     "tid-456",
			ClientId:     "cid-123",
			ClientSecret: "secret-value",
		},
	}
	assert.Equal(t, "cid-123", auth.ClientId)
	assert.Equal(t, "tid-456", auth.TenantId)
	assert.NotNil(t, auth.AzureCredentials)
	assert.Equal(t, "secret-value", auth.AzureCredentials.ClientSecret)
}

// =====================================================================
// Additional: resolveSmr more subtests
// =====================================================================
func Test_resolveSmr_validUUID_cov3(t *testing.T) {
	t.Parallel()

	smrArg := "12345678-1234-1234-1234-123456789abc"
	result := resolveSmr(smrArg, config.NewEmptyConfig(), config.NewEmptyConfig())
	require.NotNil(t, result)
	assert.Equal(t, smrArg, *result)
}

func Test_resolveSmr_fromProjectConfig_cov3(t *testing.T) {
	t.Parallel()

	projCfg := config.NewEmptyConfig()
	_ = projCfg.Set("pipeline.config.applicationServiceManagementReference", "proj-smr-value")
	userCfg := config.NewEmptyConfig()

	result := resolveSmr("", projCfg, userCfg)
	require.NotNil(t, result)
	assert.Equal(t, "proj-smr-value", *result)
}

func Test_resolveSmr_fromUserConfig_cov3(t *testing.T) {
	t.Parallel()

	projCfg := config.NewEmptyConfig()
	userCfg := config.NewEmptyConfig()
	_ = userCfg.Set("pipeline.config.applicationServiceManagementReference", "user-smr-value")

	result := resolveSmr("", projCfg, userCfg)
	require.NotNil(t, result)
	assert.Equal(t, "user-smr-value", *result)
}

func Test_resolveSmr_projectTakesPrecedenceOverUser_cov3(t *testing.T) {
	t.Parallel()

	projCfg := config.NewEmptyConfig()
	_ = projCfg.Set("pipeline.config.applicationServiceManagementReference", "proj-val")
	userCfg := config.NewEmptyConfig()
	_ = userCfg.Set("pipeline.config.applicationServiceManagementReference", "user-val")

	result := resolveSmr("", projCfg, userCfg)
	require.NotNil(t, result)
	assert.Equal(t, "proj-val", *result)
}

// =====================================================================
// servicePrincipal additional subtests
// =====================================================================
func Test_servicePrincipal_lookupById_cov3(t *testing.T) {
	t.Parallel()

	appId := "found-app-id"
	displayName := "my-sp"
	entraIdSvc := &mockEntraIdService3{
		getSpResult: &graphsdk.ServicePrincipal{
			AppId:       appId,
			DisplayName: displayName,
		},
	}

	result, err := servicePrincipal(
		context.Background(),
		"",        // envClientId
		"sub-123", // subscriptionId
		&PipelineManagerArgs{PipelineServicePrincipalId: "lookup-id"},
		entraIdSvc,
	)
	require.NoError(t, err)
	assert.Equal(t, appId, result.appIdOrName)
	assert.Equal(t, displayName, result.applicationName)
	assert.Equal(t, lookupKindPrincipalId, result.lookupKind)
}

func Test_servicePrincipal_lookupByName_notFound_cov3(t *testing.T) {
	t.Parallel()

	entraIdSvc := &mockEntraIdService3{
		getSpErr: errors.New("not found"),
	}

	result, err := servicePrincipal(
		context.Background(),
		"",
		"sub-123",
		&PipelineManagerArgs{PipelineServicePrincipalName: "my-sp-name"},
		entraIdSvc,
	)
	// When lookupKind is principalName and not found, it returns the name for creation
	require.NoError(t, err)
	assert.Equal(t, "my-sp-name", result.appIdOrName)
	assert.Equal(t, lookupKindPrincipleName, result.lookupKind)
}

func Test_servicePrincipal_lookupById_notFound_cov3(t *testing.T) {
	t.Parallel()

	entraIdSvc := &mockEntraIdService3{
		getSpErr: errors.New("not found"),
	}

	_, err := servicePrincipal(
		context.Background(),
		"",
		"sub-123",
		&PipelineManagerArgs{PipelineServicePrincipalId: "missing-id"},
		entraIdSvc,
	)
	// When lookupKind is principalId and not found, it returns error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not found")
}

func Test_servicePrincipal_envClientId_notFound_cov3(t *testing.T) {
	t.Parallel()

	entraIdSvc := &mockEntraIdService3{
		getSpErr: errors.New("not found"),
	}

	_, err := servicePrincipal(
		context.Background(),
		"env-client-id",
		"sub-123",
		&PipelineManagerArgs{},
		entraIdSvc,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not found")
}

func Test_servicePrincipal_noIdentifiers_fallback_cov3(t *testing.T) {
	t.Parallel()

	entraIdSvc := &mockEntraIdService3{}

	result, err := servicePrincipal(
		context.Background(),
		"",
		"sub-123",
		&PipelineManagerArgs{},
		entraIdSvc,
	)
	require.NoError(t, err)
	assert.Contains(t, result.applicationName, "az-dev-")
	assert.Nil(t, result.servicePrincipal)
}

// =====================================================================
// savePipelineProviderToEnv
// =====================================================================
func Test_savePipelineProviderToEnv_cov3(t *testing.T) {
	t.Parallel()

	t.Run("save success", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

		pm := &PipelineManager{
			env:        env,
			envManager: envManager,
		}

		err := pm.savePipelineProviderToEnv(context.Background(), ciProviderGitHubActions, env)
		require.NoError(t, err)

		val, found := env.LookupEnv(envPersistedKey)
		assert.True(t, found)
		assert.Equal(t, string(ciProviderGitHubActions), val)
	})

	t.Run("save error", func(t *testing.T) {
		env := environment.NewWithValues("test-env", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, mock.Anything).Return(errors.New("save failed"))

		pm := &PipelineManager{
			env:        env,
			envManager: envManager,
		}

		err := pm.savePipelineProviderToEnv(context.Background(), ciProviderAzureDevOps, env)
		require.Error(t, err)
	})
}

// =====================================================================
// generatePipelineDefinition - additional subtests
// =====================================================================
func Test_generatePipelineDefinition_cov3(t *testing.T) {
	t.Parallel()

	t.Run("github actions template", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "azure-dev.yml")
		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
		}
		err := generatePipelineDefinition(outPath, props)
		require.NoError(t, err)

		data, err := os.ReadFile(outPath)
		require.NoError(t, err)
		assert.Contains(t, string(data), "azd")
	})

	t.Run("azdo template", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "azure-dev.yml")
		props := projectProperties{
			CiProvider: ciProviderAzureDevOps,
		}
		err := generatePipelineDefinition(outPath, props)
		require.NoError(t, err)

		data, err := os.ReadFile(outPath)
		require.NoError(t, err)
		assert.Contains(t, string(data), "azure")
	})
}

// =====================================================================
// checkAndPromptForProviderFiles
// =====================================================================
func Test_checkAndPromptForProviderFiles_cov3(t *testing.T) {
	t.Parallel()

	t.Run("files already exist", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		dir := t.TempDir()
		ghDir := filepath.Join(dir, ".github", "workflows")
		require.NoError(t, os.MkdirAll(ghDir, os.ModePerm))
		require.NoError(t, os.WriteFile(filepath.Join(ghDir, "azure-dev.yml"), []byte("on: push"), 0600))

		pm := &PipelineManager{console: mockContext.Console}

		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   dir,
		}

		err := pm.checkAndPromptForProviderFiles(*mockContext.Context, props)
		require.NoError(t, err)
	})

	t.Run("files missing user creates", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)

		dir := t.TempDir()
		pm := &PipelineManager{console: mockContext.Console}

		props := projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   dir,
		}

		err := pm.checkAndPromptForProviderFiles(*mockContext.Context, props)
		require.NoError(t, err)

		// Check file was created
		assert.True(t, fileExists(filepath.Join(dir, ".github", "workflows", "azure-dev.yml")))
	})
}

// =====================================================================
// ensureGitHubLogin - standalone function tests
// =====================================================================
func Test_ensureGitHubLogin_alreadyLoggedIn_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GetAuthStatus → success (logged in)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth") && strings.Contains(command, "status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "Logged in", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	loginPerformed, err := ensureGitHubLogin(
		*mockContext.Context, "/some/path", ghCli, gitCli, "github.com", mockContext.Console)
	require.NoError(t, err)
	assert.False(t, loginPerformed)
}

func Test_ensureGitHubLogin_notLoggedIn_declines_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GetAuthStatus → not logged in (stderr matches regex)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth") && strings.Contains(command, "status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "",
			"You are not logged into any GitHub hosts. "+
				"Run gh auth login to authenticate.",
		), fmt.Errorf("exit status 1")
	})

	// Decline login
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	_, err := ensureGitHubLogin(
		*mockContext.Context, "/some/path", ghCli, gitCli, "github.com", mockContext.Console)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive GitHub login declined")
}

func Test_ensureGitHubLogin_authStatusError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GetAuthStatus → unexpected error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth") && strings.Contains(command, "status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "connection refused"), fmt.Errorf("connection refused")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	_, err := ensureGitHubLogin(
		*mockContext.Context, "/some/path", ghCli, gitCli, "github.com", mockContext.Console)
	require.Error(t, err)
}

func Test_ensureGitHubLogin_loginSuccess_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GetAuthStatus → not logged in (stderr matches regex)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth") && strings.Contains(command, "status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "",
			"You are not logged into any GitHub hosts. "+
				"Run gh auth login to authenticate.",
		), fmt.Errorf("exit status 1")
	})

	// Accept login
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(true)

	// Mock GetGitProtocolType
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git_protocol")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https", ""), nil
	})

	// Mock Login → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "auth") && strings.Contains(command, "login")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "Login success", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	loginPerformed, err := ensureGitHubLogin(
		*mockContext.Context, "/some/path", ghCli, gitCli, "github.com", mockContext.Console)
	require.NoError(t, err)
	assert.True(t, loginPerformed)
}

// =====================================================================
// getRemoteUrlFromExisting - standalone function tests
// =====================================================================
func Test_getRemoteUrlFromExisting_success_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListRepositories
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0,
			`[{"nameWithOwner":"user/repo1","url":"https://github.com/user/repo1",`+
				`"sshUrl":"git@github.com:user/repo1.git"},`+
				`{"nameWithOwner":"user/repo2","url":"https://github.com/user/repo2",`+
				`"sshUrl":"git@github.com:user/repo2.git"}]`,
			""), nil
	})

	// Mock GetGitProtocolType → https
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git_protocol")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https", ""), nil
	})

	// User selects first repo (index 0)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	url, err := getRemoteUrlFromExisting(*mockContext.Context, ghCli, mockContext.Console)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/user/repo1", url)
}

func Test_getRemoteUrlFromExisting_listError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", ""), errors.New("api error")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	_, err := getRemoteUrlFromExisting(*mockContext.Context, ghCli, mockContext.Console)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing existing repositories")
}

func Test_getRemoteUrlFromExisting_noRepos_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "[]", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	_, err := getRemoteUrlFromExisting(*mockContext.Context, ghCli, mockContext.Console)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no existing GitHub repositories found")
}

// =====================================================================
// getRemoteUrlFromNewRepository - standalone function tests
// =====================================================================
func Test_getRemoteUrlFromNewRepository_success_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Prompt for repo name
	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond("my-repo")

	// Mock CreatePrivateRepository → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "create")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ViewRepository
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0,
			`{"nameWithOwner":"user/my-repo",`+
				`"url":"https://github.com/user/my-repo",`+
				`"sshUrl":"git@github.com:user/my-repo.git"}`,
			""), nil
	})

	// Mock GetGitProtocolType → ssh
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git_protocol")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "ssh", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	url, err := getRemoteUrlFromNewRepository(*mockContext.Context, ghCli, "/some/project", mockContext.Console)
	require.NoError(t, err)
	assert.Equal(t, "git@github.com:user/my-repo.git", url)
}

func Test_getRemoteUrlFromNewRepository_createError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Prompt for repo name
	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond("my-repo")

	// Mock CreatePrivateRepository → error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "create")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", ""), errors.New("permission denied")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	_, err := getRemoteUrlFromNewRepository(*mockContext.Context, ghCli, "/some/project", mockContext.Console)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating repository")
}

// =====================================================================
// configureGitRemote (GitHubScmProvider) - method tests
// =====================================================================
func Test_GitHubScmProvider_configureGitRemote_selectExisting_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// User selects "Select an existing GitHub project" (index 0)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "How would you like to configure")
	}).Respond(0)

	// Mock ListRepositories
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0,
			`[{"nameWithOwner":"org/project",`+
				`"url":"https://github.com/org/project",`+
				`"sshUrl":"git@github.com:org/project.git"}]`,
			""), nil
	})

	// User selects first (only) repo
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Choose an existing")
	}).Respond(0)

	// Mock GetGitProtocolType
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git_protocol")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https", ""), nil
	})

	provider := &GitHubScmProvider{
		console: mockContext.Console,
		ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:  git.NewCli(mockContext.CommandRunner),
	}

	url, err := provider.configureGitRemote(*mockContext.Context, "/some/path", "origin")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/org/project", url)
	assert.False(t, provider.newGitHubRepoCreated)
}

func Test_GitHubScmProvider_configureGitRemote_createNew_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// User selects "Create a new private GitHub repository" (index 1)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "How would you like to configure")
	}).Respond(1)

	// Prompt for repo name
	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond("new-repo")

	// Mock CreatePrivateRepository → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "create")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ViewRepository
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "repo") && strings.Contains(command, "view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0,
			`{"nameWithOwner":"user/new-repo",`+
				`"url":"https://github.com/user/new-repo",`+
				`"sshUrl":"git@github.com:user/new-repo.git"}`,
			""), nil
	})

	// Mock GetGitProtocolType
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git_protocol")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https", ""), nil
	})

	provider := &GitHubScmProvider{
		console: mockContext.Console,
		ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:  git.NewCli(mockContext.CommandRunner),
	}

	url, err := provider.configureGitRemote(*mockContext.Context, "/some/path", "origin")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/user/new-repo", url)
	assert.True(t, provider.newGitHubRepoCreated)
}

func Test_GitHubScmProvider_configureGitRemote_enterUrl_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// User selects "Enter a remote URL directly" (index 2)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "How would you like to configure")
	}).Respond(2)

	// Prompt for URL
	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("https://github.com/user/entered-repo")

	provider := &GitHubScmProvider{
		console: mockContext.Console,
		ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:  git.NewCli(mockContext.CommandRunner),
	}

	url, err := provider.configureGitRemote(*mockContext.Context, "/some/path", "origin")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/user/entered-repo", url)
	assert.False(t, provider.newGitHubRepoCreated)
}

func Test_GitHubScmProvider_configureGitRemote_selectError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// User cancels select
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).RespondFn(
		func(options input.ConsoleOptions) (any, error) {
			return 0, errors.New("user cancelled")
		},
	)

	provider := &GitHubScmProvider{
		console: mockContext.Console,
		ghCli:   github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:  git.NewCli(mockContext.CommandRunner),
	}

	_, err := provider.configureGitRemote(*mockContext.Context, "/some/path", "origin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for remote configuration type")
}

// =====================================================================
// preventGitPush (GitHubScmProvider) - deeper coverage
// =====================================================================
func Test_GitHubScmProvider_preventGitPush_newRepoCreated_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	provider := &GitHubScmProvider{
		console:              mockContext.Console,
		ghCli:                github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:               git.NewCli(mockContext.CommandRunner),
		newGitHubRepoCreated: true,
	}

	gitRepo := &gitRepositoryDetails{
		owner:          "test-owner",
		repoName:       "test-repo",
		gitProjectPath: t.TempDir(),
	}

	prevented, err := provider.preventGitPush(*mockContext.Context, gitRepo, "origin", "main")
	require.NoError(t, err)
	assert.False(t, prevented) // New repos skip the check
}

func Test_GitHubScmProvider_preventGitPush_existingRepo_actionsEnabled_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock GitHubActionsExists → actions already enabled upstream
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "actions/workflows")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, `{"total_count": 1}`, ""), nil
	})

	provider := &GitHubScmProvider{
		console:              mockContext.Console,
		ghCli:                github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner),
		gitCli:               git.NewCli(mockContext.CommandRunner),
		newGitHubRepoCreated: false,
	}

	dir := t.TempDir()
	gitRepo := &gitRepositoryDetails{
		owner:          "test-owner",
		repoName:       "test-repo",
		gitProjectPath: dir,
	}

	prevented, err := provider.preventGitPush(*mockContext.Context, gitRepo, "origin", "main")
	require.NoError(t, err)
	assert.False(t, prevented)
}

// =====================================================================
// credentialOptions (AzdoCiProvider) - client credentials path
// =====================================================================
func Test_AzdoCiProvider_credentialOptions_clientCredentials_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}

	opts, err := provider.credentialOptions(
		context.Background(),
		&gitRepositoryDetails{},
		provisioning.Options{},
		AuthTypeClientCredentials,
		nil,
	)
	require.NoError(t, err)
	assert.True(t, opts.EnableClientCredentials)
	assert.False(t, opts.EnableFederatedCredentials)
}

func Test_AzdoCiProvider_credentialOptions_unknownType_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}

	opts, err := provider.credentialOptions(
		context.Background(),
		&gitRepositoryDetails{},
		provisioning.Options{},
		PipelineAuthType("unknown-type"),
		nil,
	)
	require.NoError(t, err)
	assert.False(t, opts.EnableClientCredentials)
	assert.False(t, opts.EnableFederatedCredentials)
}

// =====================================================================
// getGitRepoDetails - ErrNotRepository path (init flow)
// =====================================================================
func Test_getGitRepoDetails_noRepo_initDeclined_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	// Mock GetRemoteUrl → ErrNotRepository
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(128, "", "fatal: not a git repository"), fmt.Errorf("exit code: 128")
	})

	// User declines git init
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(false)

	scm := &mockScmProvider{
		nameFn: func() string { return "GitHub" },
	}

	pm := &PipelineManager{
		azdCtx:        azdCtx,
		gitCli:        git.NewCli(mockContext.CommandRunner),
		console:       mockContext.Console,
		scmProvider:   scm,
		args:          &PipelineManagerArgs{PipelineRemoteName: "origin"},
		importManager: project.NewImportManager(nil),
		prjConfig:     &project.ProjectConfig{},
	}

	_, err := pm.getGitRepoDetails(*mockContext.Context)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confirmation declined")
}

func Test_getGitRepoDetails_noRemote_configureGitRemote_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	callCount := 0
	// First call to GetRemoteUrl → ErrNoSuchRemote, second call → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		callCount++
		if callCount == 1 {
			return exec.NewRunResult(2, "", "error: No such remote 'origin'"), fmt.Errorf("exit code: 2")
		}
		return exec.NewRunResult(0, "https://github.com/owner/repo.git", ""), nil
	})

	// Mock GetCurrentBranch
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
	})

	// Mock AddRemote
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "add")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	scm := &mockScmProvider{
		nameFn: func() string { return "GitHub" },
		configureGitRemoteFn: func(ctx context.Context, repoPath string, remoteName string) (string, error) {
			return "https://github.com/owner/repo.git", nil
		},
		gitRepoDetailsFn: func(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
			return &gitRepositoryDetails{
				owner:    "owner",
				repoName: "repo",
				remote:   remoteUrl,
				url:      "https://github.com/owner/repo",
			}, nil
		},
	}

	pm := &PipelineManager{
		azdCtx:        azdCtx,
		gitCli:        git.NewCli(mockContext.CommandRunner),
		console:       mockContext.Console,
		scmProvider:   scm,
		args:          &PipelineManagerArgs{PipelineRemoteName: "origin"},
		importManager: project.NewImportManager(nil),
		prjConfig:     &project.ProjectConfig{},
	}

	details, err := pm.getGitRepoDetails(*mockContext.Context)
	require.NoError(t, err)
	assert.Equal(t, "owner", details.owner)
	assert.Equal(t, "repo", details.repoName)
}

// =====================================================================
// GitPush (GitHubScmProvider) - simple delegation
// =====================================================================
func Test_GitHubScmProvider_GitPush_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock git push
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "push")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	provider := &GitHubScmProvider{
		gitCli: git.NewCli(mockContext.CommandRunner),
	}

	gitRepo := &gitRepositoryDetails{
		gitProjectPath: t.TempDir(),
	}

	err := provider.GitPush(*mockContext.Context, gitRepo, "origin", "main")
	require.NoError(t, err)
}

func Test_GitHubScmProvider_GitPush_error_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock git push → error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "push")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "rejected"), errors.New("push rejected")
	})

	provider := &GitHubScmProvider{
		gitCli: git.NewCli(mockContext.CommandRunner),
	}

	gitRepo := &gitRepositoryDetails{
		gitProjectPath: t.TempDir(),
	}

	err := provider.GitPush(*mockContext.Context, gitRepo, "origin", "main")
	require.Error(t, err)
}

// =====================================================================
// gitHubActionsEnablingChoice.String()
// =====================================================================
func Test_gitHubActionsEnablingChoice_String_cov3(t *testing.T) {
	t.Parallel()

	manualStr := manualChoice.String()
	assert.Contains(t, manualStr, "manually enabled")

	cancelStr := cancelChoice.String()
	assert.Contains(t, cancelStr, "Exit")
}

// =====================================================================
// Additional coverage: generatePipelineDefinition with azdo template
// =====================================================================
func Test_generatePipelineDefinition_azdo_template_cov3(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "azure-dev.yml")

	props := projectProperties{
		CiProvider: ciProviderAzureDevOps,
	}
	err := generatePipelineDefinition(outPath, props)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "azd")
}

// =====================================================================
// Additional coverage: toCiProviderType and toInfraProviderType edge cases
// =====================================================================
func Test_toCiProviderType_values_cov3(t *testing.T) {
	t.Parallel()

	ghProvider, err := toCiProviderType("github")
	require.NoError(t, err)
	assert.Equal(t, ciProviderGitHubActions, ghProvider)

	azdoProvider, err := toCiProviderType("azdo")
	require.NoError(t, err)
	assert.Equal(t, ciProviderAzureDevOps, azdoProvider)

	_, err = toCiProviderType("unknown")
	require.Error(t, err)
}

func Test_toInfraProviderType_values_cov3(t *testing.T) {
	t.Parallel()

	bicepProvider, err := toInfraProviderType("bicep")
	require.NoError(t, err)
	assert.Equal(t, infraProviderBicep, bicepProvider)

	tfProvider, err := toInfraProviderType("terraform")
	require.NoError(t, err)
	assert.Equal(t, infraProviderTerraform, tfProvider)

	_, err = toInfraProviderType("other")
	require.Error(t, err)
}

// =====================================================================
// Additional: mergeProjectVariablesAndSecrets
// =====================================================================
func Test_mergeProjectVariablesAndSecrets_cov3(t *testing.T) {
	t.Parallel()

	envMap := map[string]string{
		"VAR1": "val1",
		"VAR2": "val2",
		"SEC1": "secret1",
	}

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		[]string{"VAR1", "VAR2"},
		[]string{"SEC1"},
		map[string]string{},
		map[string]string{},
		nil,
		envMap,
	)
	require.NoError(t, err)
	assert.Equal(t, "val1", vars["VAR1"])
	assert.Equal(t, "val2", vars["VAR2"])
	assert.Equal(t, "secret1", secrets["SEC1"])
}

func Test_mergeProjectVariablesAndSecrets_missingValues_cov3(t *testing.T) {
	t.Parallel()

	envMap := map[string]string{
		"VAR1": "val1",
	}

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		[]string{"VAR1", "MISSING_VAR"},
		[]string{"MISSING_SEC"},
		map[string]string{},
		map[string]string{},
		nil,
		envMap,
	)
	require.NoError(t, err)
	assert.Equal(t, "val1", vars["VAR1"])
	// Missing values should not be in the map
	_, ok := vars["MISSING_VAR"]
	assert.False(t, ok)
	_, ok = secrets["MISSING_SEC"]
	assert.False(t, ok)
}

// =====================================================================
// Additional: generateFilePaths
// =====================================================================
func Test_generateFilePaths_cov3(t *testing.T) {
	t.Parallel()

	paths := generateFilePaths([]string{"/repo/dir1", "/repo/dir2"}, []string{"file.yml", "file.yaml"})
	assert.Len(t, paths, 4)
	assert.Contains(t, paths, filepath.Join("/repo/dir1", "file.yml"))
	assert.Contains(t, paths, filepath.Join("/repo/dir1", "file.yaml"))
	assert.Contains(t, paths, filepath.Join("/repo/dir2", "file.yml"))
	assert.Contains(t, paths, filepath.Join("/repo/dir2", "file.yaml"))

	empty := generateFilePaths(nil, nil)
	assert.Empty(t, empty)
}

// =====================================================================
// Additional: parseAzDoRemote
// =====================================================================
func Test_parseAzDoRemote_validHttps_cov3(t *testing.T) {
	t.Parallel()

	details, err := parseAzDoRemote("https://dev.azure.com/myorg/myproject/_git/myrepo")
	require.NoError(t, err)
	assert.Equal(t, "myproject", details.Project)
	assert.Equal(t, "myrepo", details.RepositoryName)
	assert.False(t, details.IsNonStandardHost)
}

func Test_parseAzDoRemote_validSsh_cov3(t *testing.T) {
	t.Parallel()

	details, err := parseAzDoRemote("git@ssh.dev.azure.com:v3/myorg/myproject/myrepo")
	require.NoError(t, err)
	assert.Equal(t, "myproject", details.Project)
	assert.Equal(t, "myrepo", details.RepositoryName)
	assert.False(t, details.IsNonStandardHost)
}

func Test_parseAzDoRemote_invalid_cov3(t *testing.T) {
	t.Parallel()

	_, err := parseAzDoRemote("https://github.com/owner/repo")
	require.Error(t, err)
}

// =====================================================================
// Additional: ensureRemote success path
// =====================================================================
func Test_PipelineManager_ensureRemote_success_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	dir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	// Mock GetRemoteUrl
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https://github.com/owner/repo.git", ""), nil
	})

	// Mock GetCurrentBranch
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
	})

	scm := &mockScmProvider{
		gitRepoDetailsFn: func(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
			return &gitRepositoryDetails{
				owner:    "owner",
				repoName: "repo",
				remote:   remoteUrl,
			}, nil
		},
	}

	pm := &PipelineManager{
		azdCtx:      azdCtx,
		gitCli:      git.NewCli(mockContext.CommandRunner),
		scmProvider: scm,
	}

	details, err := pm.ensureRemote(*mockContext.Context, dir, "origin")
	require.NoError(t, err)
	assert.Equal(t, "owner", details.owner)
	assert.Equal(t, "main", details.branch)
	assert.Equal(t, dir, details.gitProjectPath)
}

// =====================================================================
// Additional: gitRepoDetails edge cases (AzdoScmProvider)
// =====================================================================
func Test_AzdoScmProvider_gitRepoDetails_httpsUrl_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoScmProvider{
		env: environment.NewWithValues("test-env", map[string]string{
			azdo.AzDoEnvironmentProjectIdName: "proj-id-123",
			azdo.AzDoEnvironmentRepoIdName:    "repo-id-456",
			azdo.AzDoEnvironmentOrgName:       "myorg",
			azdo.AzDoEnvironmentProjectName:   "myproject",
			azdo.AzDoEnvironmentRepoName:      "myrepo",
			azdo.AzDoEnvironmentRepoWebUrl:    "https://dev.azure.com/myorg/myproject/_git/myrepo",
		}),
	}
	details, err := provider.gitRepoDetails(
		context.Background(),
		"https://dev.azure.com/myorg/myproject/_git/myrepo",
	)
	require.NoError(t, err)
	assert.Equal(t, "myorg", details.owner)
	assert.Equal(t, "myrepo", details.repoName)
}

func Test_AzdoScmProvider_gitRepoDetails_sshUrl_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoScmProvider{
		env: environment.NewWithValues("test-env", map[string]string{
			azdo.AzDoEnvironmentProjectIdName: "proj-id-123",
			azdo.AzDoEnvironmentRepoIdName:    "repo-id-456",
			azdo.AzDoEnvironmentOrgName:       "myorg",
			azdo.AzDoEnvironmentProjectName:   "myproject",
			azdo.AzDoEnvironmentRepoName:      "myrepo",
		}),
	}
	details, err := provider.gitRepoDetails(
		context.Background(),
		"git@ssh.dev.azure.com:v3/myorg/myproject/myrepo",
	)
	require.NoError(t, err)
	assert.Equal(t, "myorg", details.owner)
	assert.Equal(t, "myrepo", details.repoName)
}

func Test_AzdoScmProvider_gitRepoDetails_invalidUrl_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoScmProvider{
		env: environment.NewWithValues("test-env", map[string]string{}),
	}
	_, err := provider.gitRepoDetails(
		context.Background(),
		"https://github.com/some/repo",
	)
	require.Error(t, err)
}

// =====================================================================
// Additional coverage: CiProviderName and ScmProviderName
// =====================================================================
func Test_PipelineManager_ProviderNames_cov3(t *testing.T) {
	t.Parallel()

	pm := &PipelineManager{
		ciProvider:  &mockCiProvider{nameFn: func() string { return "CiName" }},
		scmProvider: &mockScmProvider{nameFn: func() string { return "ScmName" }},
	}

	assert.Equal(t, "CiName", pm.CiProviderName())
	assert.Equal(t, "ScmName", pm.ScmProviderName())
}

// =====================================================================
// Additional: pipelineProviderFiles map tests
// =====================================================================
func Test_pipelineProviderFiles_cov3(t *testing.T) {
	t.Parallel()

	ghFiles, ok := pipelineProviderFiles[ciProviderGitHubActions]
	require.True(t, ok)
	assert.Greater(t, len(ghFiles.Files), 0)
	assert.Greater(t, len(ghFiles.PipelineDirectories), 0)
	assert.NotEmpty(t, ghFiles.DefaultFile)

	azdoFiles, ok := pipelineProviderFiles[ciProviderAzureDevOps]
	require.True(t, ok)
	assert.Greater(t, len(azdoFiles.Files), 0)
	assert.Greater(t, len(azdoFiles.PipelineDirectories), 0)
	assert.NotEmpty(t, azdoFiles.DefaultFile)
}

// =====================================================================
// configureGitRemote2 tests for the mock provider
// =====================================================================
func Test_mockScmProvider_configureGitRemote_cov3(t *testing.T) {
	t.Parallel()

	scm := &mockScmProvider{
		configureGitRemoteFn: func(ctx context.Context, repoPath string, remoteName string) (string, error) {
			return "https://github.com/owner/repo.git", nil
		},
	}

	url, err := scm.configureGitRemote(context.Background(), "/path", "origin")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/owner/repo.git", url)
}

// =====================================================================
// Additional: CredentialOptions struct - verify fields
// =====================================================================
func Test_CredentialOptions_fields_cov3(t *testing.T) {
	t.Parallel()

	opts := &CredentialOptions{
		EnableClientCredentials:    true,
		EnableFederatedCredentials: false,
		FederatedCredentialOptions: []*graphsdk.FederatedIdentityCredential{
			{Name: "test-cred", Issuer: "issuer", Subject: "sub"},
		},
	}

	assert.True(t, opts.EnableClientCredentials)
	assert.Len(t, opts.FederatedCredentialOptions, 1)
}

// =====================================================================
// Additional: pipeline (azdo) name() and url() methods
// =====================================================================
func Test_pipeline_nameAndUrl_cov3(t *testing.T) {
	t.Parallel()

	defId := 42
	p := &pipeline{
		repoDetails: &AzdoRepositoryDetails{
			projectName: "my-project",
			repoName:    "my-repo",
			orgName:     "my-org",
			repoWebUrl:  "https://dev.azure.com/my-org/my-project/_git/my-repo",
			buildDefinition: &build.BuildDefinition{
				Name: new(string),
				Id:   &defId,
			},
		},
	}
	*p.repoDetails.buildDefinition.Name = "my-pipeline"

	assert.Equal(t, "my-pipeline", p.name())
	assert.Contains(t, p.url(), "_build?definitionId=42")
}

// =====================================================================
// GitHubCiProvider.configurePipeline tests - simplest paths
// =====================================================================
func Test_GitHubCiProvider_configurePipeline_noVarsNoSecrets_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables:   nil,
			projectSecrets:     nil,
			secrets:            map[string]string{},
			variables:          map[string]string{},
			providerParameters: nil,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "actions", result.name())
}

func Test_GitHubCiProvider_configurePipeline_withSecretsAndVars_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ListVariables → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock SetSecret → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock SetVariable → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		url:      "https://github.com/test-owner/test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"VAR1"},
			projectSecrets:   []string{"SEC1"},
			secrets:          map[string]string{"SEC1": "secret-value"},
			variables:        map[string]string{"VAR1": "var-value"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "actions", result.name())
	assert.Contains(t, result.url(), "test-owner/test-repo")
}

func Test_GitHubCiProvider_configurePipeline_listSecretsError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "access denied"), fmt.Errorf("access denied")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
	}

	_, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"VAR1"},
			secrets:          map[string]string{},
			variables:        map[string]string{},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to get list of repository secrets")
}

func Test_GitHubCiProvider_configurePipeline_setSecretError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock SetSecret → error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "failed"), fmt.Errorf("failed")
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
	}

	_, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			secrets:   map[string]string{"SEC1": "val"},
			variables: map[string]string{},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed setting")
}

func Test_GitHubCiProvider_configurePipeline_existingSecretsUpdateAll_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → has OLD_SEC
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "SEC1\t2024-01-01\n", ""), nil
	})

	// Mock ListVariables → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock SetSecret → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// When prompted about existing secret, select "update all" (index 3)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(3)

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"SEC1"},
			projectSecrets:   []string{"SEC1"},
			secrets:          map[string]string{"SEC1": "new-value"},
			variables:        map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_GitHubCiProvider_configurePipeline_existingVarsUpdateAll_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ListVariables → has VAR1
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "VAR1\tval1\t2024-01-01\n", ""), nil
	})

	// Mock SetVariable → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// When prompted about existing variable, select "update all" (index 3)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(3)

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"VAR1"},
			secrets:          map[string]string{},
			variables:        map[string]string{"VAR1": "new-value"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// =====================================================================
// Additional: AzdoCiProvider.configureConnection - federated path
// =====================================================================
func Test_AzdoCiProvider_configureConnection_federated_cov3(t *testing.T) {
	t.Parallel()

	provider := &AzdoCiProvider{}
	err := provider.configureConnection(
		context.Background(),
		&gitRepositoryDetails{},
		provisioning.Options{},
		&authConfiguration{},
		&CredentialOptions{
			EnableFederatedCredentials: true,
		},
	)
	require.NoError(t, err)
}

// =====================================================================
// Additional: mergeProjectVariablesAndSecrets with providerParameters
// =====================================================================
func Test_mergeProjectVariablesAndSecrets_providerParams_cov3(t *testing.T) {
	t.Parallel()

	envMap := map[string]string{
		"MAPPED_VAR": "mapped-value",
	}

	params := []provisioning.Parameter{
		{
			Name:          "param1",
			EnvVarMapping: []string{"MAPPED_VAR"},
		},
	}

	vars, secrets, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		envMap,
	)
	require.NoError(t, err)
	assert.Equal(t, "mapped-value", vars["MAPPED_VAR"])
	assert.Empty(t, secrets)
}

func Test_mergeProjectVariablesAndSecrets_localPromptNoMapping_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:        "bad-param",
			LocalPrompt: true,
			// No EnvVarMapping
		},
	}

	_, _, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not a mapped environment variable")
}

func Test_mergeProjectVariablesAndSecrets_localPromptMultiMapping_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:          "multi-param",
			LocalPrompt:   true,
			EnvVarMapping: []string{"VAR1", "VAR2"},
		},
	}

	_, _, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than one mapped environment variable")
}

func Test_mergeProjectVariablesAndSecrets_singleMappingWithValue_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:               "single-param",
			Value:              "resolved-value",
			EnvVarMapping:      []string{"OUTPUT_VAR"},
			UsingEnvVarMapping: true,
		},
	}

	vars, _, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{},
	)
	require.NoError(t, err)
	assert.Equal(t, "resolved-value", vars["OUTPUT_VAR"])
}

// =====================================================================
// Additional: PipelineManager.Configure error paths
// =====================================================================
func Test_PipelineManager_Configure_requiredToolsError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	pm := &PipelineManager{
		scmProvider: &mockScmProvider{
			requiredToolsFn: func(ctx context.Context) ([]tools.ExternalTool, error) {
				return nil, fmt.Errorf("tool check failed")
			},
		},
		ciProvider: &mockCiProvider{},
		console:    mockContext.Console,
		args:       &PipelineManagerArgs{},
	}

	_, err := pm.Configure(*mockContext.Context, "test-project", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool check failed")
}

// =====================================================================
// Additional coverage: workflow name/url
// =====================================================================
func Test_workflow_nameAndUrl_cov3(t *testing.T) {
	t.Parallel()

	w := &workflow{
		repoDetails: &gitRepositoryDetails{
			owner:    "test-owner",
			repoName: "test-repo",
			url:      "https://github.com/test-owner/test-repo",
		},
	}

	assert.Equal(t, "actions", w.name())
	assert.Contains(t, w.url(), "test-owner/test-repo")
}

// =====================================================================
// Additional: getGitRepoDetails edge cases
// =====================================================================
func Test_getGitRepoDetails_remoteUrlEmpty_configureGitRemote_error_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	dir := t.TempDir()

	// Mock git remote get-url → error: No such remote
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(2, "", "error: No such remote 'origin'"), fmt.Errorf("exit status 2")
	})

	// Mock git rev-parse → success (it IS a repo)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "rev-parse")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, dir, ""), nil
	})

	// Mock git branch --show-current
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
	})

	scm := &mockScmProvider{
		configureGitRemoteFn: func(ctx context.Context, repoPath string, remoteName string) (string, error) {
			return "", fmt.Errorf("configureGitRemote failed")
		},
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	pm := &PipelineManager{
		azdCtx:        azdCtx,
		scmProvider:   scm,
		ciProvider:    &mockCiProvider{},
		gitCli:        git.NewCli(mockContext.CommandRunner),
		console:       mockContext.Console,
		args:          &PipelineManagerArgs{PipelineRemoteName: "origin"},
		importManager: project.NewImportManager(nil),
		prjConfig:     &project.ProjectConfig{},
	}

	_, err := pm.getGitRepoDetails(*mockContext.Context)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configureGitRemote failed")
}

// =====================================================================
// Additional: ensureRemote error from gitRepoDetails
// =====================================================================
func Test_PipelineManager_ensureRemote_gitRepoDetailsError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	dir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	// git remote get-url returns a URL
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https://example.com/repo.git", ""), nil
	})

	// git branch --show-current
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
	})

	scm := &mockScmProvider{
		gitRepoDetailsFn: func(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
			return nil, fmt.Errorf("cannot parse remote")
		},
	}

	pm := &PipelineManager{
		azdCtx:      azdCtx,
		scmProvider: scm,
		gitCli:      git.NewCli(mockContext.CommandRunner),
		console:     mockContext.Console,
		args:        &PipelineManagerArgs{},
	}

	_, err := pm.ensureRemote(*mockContext.Context, dir, "origin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse remote")
}

// =====================================================================
// configurePipeline: existing secrets duplicates → updateAll
// =====================================================================
func Test_GitHubCiProvider_configurePipeline_existingSecrets_updateAll_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → returns 2 existing secrets that are also in toBeSetSecrets
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "SEC_A\nSEC_B\n", ""), nil
	})

	// Mock ListVariables → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock SetSecret → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// For the first duplicate secret, user selects "updateAll" (index 3)
	selectCount := 0
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "already exists")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		selectCount++
		return 3, nil // selectionUpdateAll
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		url:      "https://github.com/test-owner/test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"SEC_A"},
			secrets: map[string]string{
				"SEC_A": "val-a",
				"SEC_B": "val-b",
			},
			variables: map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	// selectUpdateAll was chosen for first, second secret should auto-update
	assert.Equal(t, 1, selectCount, "only first duplicate should prompt, second should auto-update")
}

// =====================================================================
// configurePipeline: existing var same value → unchanged
// =====================================================================
func Test_GitHubCiProvider_configurePipeline_existingVarUnchanged_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ListVariables → one existing var with same value
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// GitHub CLI variable list output: name\tvalue
		return exec.NewRunResult(0, "MY_VAR\tsame-value\n", ""), nil
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		url:      "https://github.com/test-owner/test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"MY_VAR"},
			variables:        map[string]string{"MY_VAR": "same-value"},
			secrets:          map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// =====================================================================
// mergeProjectVariablesAndSecrets: multi-mapping with env values
// =====================================================================
func Test_mergeProjectVariablesAndSecrets_multiMappingWithEnvValues_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:          "multi-param",
			EnvVarMapping: []string{"ENV_A", "ENV_B"},
			Secret:        false,
		},
	}

	vars, _, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{"ENV_A": "value-a", "ENV_B": "value-b"},
	)
	require.NoError(t, err)
	assert.Equal(t, "value-a", vars["ENV_A"])
	assert.Equal(t, "value-b", vars["ENV_B"])
}

// multi-mapping with secret=true
func Test_mergeProjectVariablesAndSecrets_multiMappingSecrets_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:          "multi-secret",
			EnvVarMapping: []string{"SEC_A", "SEC_B"},
			Secret:        true,
		},
	}

	_, secs, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{"SEC_A": "s-val-a"},
	)
	require.NoError(t, err)
	assert.Equal(t, "s-val-a", secs["SEC_A"])
	_, hasSECB := secs["SEC_B"]
	assert.False(t, hasSECB, "SEC_B should not be set because env value is empty")
}

// single mapping with LocalPrompt=true and secret=true
func Test_mergeProjectVariablesAndSecrets_singleMappingLocalPromptSecret_cov3(t *testing.T) {
	t.Parallel()

	params := []provisioning.Parameter{
		{
			Name:          "prompt-secret",
			Value:         "secret-val",
			EnvVarMapping: []string{"PROMPT_SEC"},
			LocalPrompt:   true,
			Secret:        true,
		},
	}

	_, secs, err := mergeProjectVariablesAndSecrets(
		nil,
		nil,
		map[string]string{},
		map[string]string{},
		params,
		map[string]string{},
	)
	require.NoError(t, err)
	assert.Equal(t, "secret-val", secs["PROMPT_SEC"])
}

// projectVariables/projectSecrets override from env
func Test_mergeProjectVariablesAndSecrets_projectOverrideFromEnv_cov3(t *testing.T) {
	t.Parallel()

	vars, secs, err := mergeProjectVariablesAndSecrets(
		[]string{"PROJ_VAR"},
		[]string{"PROJ_SEC"},
		map[string]string{},
		map[string]string{},
		nil,
		map[string]string{
			"PROJ_VAR": "from-env",
			"PROJ_SEC": "sec-from-env",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "from-env", vars["PROJ_VAR"])
	assert.Equal(t, "sec-from-env", secs["PROJ_SEC"])
}

// =====================================================================
// ensureRemote: getCurrentBranch error
// =====================================================================
func Test_PipelineManager_ensureRemote_getCurrentBranchError_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	dir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	// git remote get-url returns a URL
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "remote") && strings.Contains(command, "get-url")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "https://example.com/repo.git", ""), nil
	})

	// git branch --show-current → error
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch") && strings.Contains(command, "--show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "not a git repo"), fmt.Errorf("not a git repo")
	})

	pm := &PipelineManager{
		azdCtx:      azdCtx,
		scmProvider: &mockScmProvider{},
		gitCli:      git.NewCli(mockContext.CommandRunner),
		console:     mockContext.Console,
		args:        &PipelineManagerArgs{},
	}

	_, err := pm.ensureRemote(*mockContext.Context, dir, "origin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting current branch")
}

// =====================================================================
// AzdoCiProvider.credentialOptions: unknown auth type → default
// =====================================================================
func Test_AzdoCiProvider_credentialOptions_unknownAuth_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	provider := &AzdoCiProvider{
		console: mockContext.Console,
	}

	opts, err := provider.credentialOptions(
		*mockContext.Context,
		&gitRepositoryDetails{},
		provisioning.Options{},
		PipelineAuthType("unknown-type"),
		nil,
	)
	require.NoError(t, err)
	assert.False(t, opts.EnableClientCredentials)
	assert.False(t, opts.EnableFederatedCredentials)
}

// =====================================================================
// configurePipeline: existing variable with different value → updateAllVars
// =====================================================================
func Test_GitHubCiProvider_configurePipeline_existingVarDiffValue_updateAll_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock ListVariables → 2 existing vars with DIFFERENT values
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "VAR_A\told-val-a\nVAR_B\told-val-b\n", ""), nil
	})

	// Mock SetVariable → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// User selects "updateAllVars" (index 3)
	selectCount := 0
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "already exists")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		selectCount++
		return 3, nil // selectionUpdateAllVars
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		url:      "https://github.com/test-owner/test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"VAR_A"},
			variables: map[string]string{
				"VAR_A": "new-val-a",
				"VAR_B": "new-val-b",
			},
			secrets: map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, selectCount, "only first var should prompt")
}

// =====================================================================
// configurePipeline: unused secret → deleteAll
// =====================================================================
func Test_GitHubCiProvider_configurePipeline_unusedSecret_deleteAll_cov3(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())

	// Mock ListSecrets → 2 existing secrets (UNUSED_SEC_A and UNUSED_SEC_B)
	// that are in variablesAndSecretsMap (via projectVariables) but NOT in toBeSetSecrets
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "UNUSED_SEC_A\nUNUSED_SEC_B\n", ""), nil
	})

	// Mock ListVariables → empty
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "variable") && strings.Contains(command, "list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock DeleteSecret → success
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "secret") && strings.Contains(command, "delete")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// User selects "deleteAll" for first unused secret (index 3)
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "no longer required")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 3, nil // selectionDeleteAll
	})

	ghCli := github.NewGitHubCli(mockContext.Console, mockContext.CommandRunner)
	gitCli := git.NewCli(mockContext.CommandRunner)

	provider := &GitHubCiProvider{
		env:     environment.NewWithValues("test-env", map[string]string{}),
		ghCli:   ghCli,
		gitCli:  gitCli,
		console: mockContext.Console,
	}

	repoDetails := &gitRepositoryDetails{
		owner:    "test-owner",
		repoName: "test-repo",
		url:      "https://github.com/test-owner/test-repo",
	}

	result, err := provider.configurePipeline(
		*mockContext.Context,
		repoDetails,
		&configurePipelineOptions{
			projectVariables: []string{"UNUSED_SEC_A", "UNUSED_SEC_B"},
			variables:        map[string]string{},
			secrets:          map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}
