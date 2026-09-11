// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_gitHub_provider_getRepoDetails(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := t.Context()
		details, e := provider.gitRepoDetails(ctx, "https://github.com/Azure/azure-dev.git")
		require.NoError(t, e)
		require.Equal(t, "Azure", details.owner)
		require.Equal(t, "azure-dev", details.repoName)
	})
	t.Run("ssh", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := t.Context()
		details, e := provider.gitRepoDetails(ctx, "git@github.com:Azure/azure-dev.git")
		require.NoError(t, e)
		require.EqualValues(t, "Azure", details.owner)
		require.EqualValues(t, "azure-dev", details.repoName)
	})
	t.Run("error", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := t.Context()
		details, e := provider.gitRepoDetails(ctx, "gt@other.com:Azure/azure-dev.git")
		require.Error(t, e, ErrRemoteHostIsNotGitHub)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})
}

func Test_gitHub_provider_preConfigure_check(t *testing.T) {
	t.Run("success with all default values", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(
			*mockContext.Context,
			PipelineManagerArgs{},
			provisioning.Options{},
			"",
		)
		require.NoError(t, err)
		require.False(t, updatedConfig)

		// No warnings on console
		consoleLog := mockContext.Console.Output()
		require.Len(t, consoleLog, 0)
	})

	t.Run("fails with terraform & federated", func(t *testing.T) {
		pipelineManagerArgs := PipelineManagerArgs{
			PipelineAuthTypeName: string(AuthTypeFederated),
		}

		infraOptions := provisioning.Options{
			Provider: provisioning.Terraform,
		}

		mockContext := mocks.NewMockContext(t.Context())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(*mockContext.Context, pipelineManagerArgs, infraOptions, "")
		require.NoError(t, err)
		require.False(t, updatedConfig)
	})
}

func createGitHubCiProvider(t *testing.T, mockContext *mocks.MockContext) *GitHubCiProvider {
	env := environment.New("test")
	ghCli := github.NewGitHubCli(
		mockContext.Console,
		mockContext.CommandRunner,
	)

	return NewGitHubCiProvider(
		env,
		mockContext.SubscriptionCredentialProvider,
		entraid.NewEntraIdService(
			mockContext.SubscriptionCredentialProvider,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		),
		ghCli,
		git.NewCli(mockContext.CommandRunner),
		mockContext.Console,
	).(*GitHubCiProvider)
}

func setupGithubCliMocks(mockContext *mocks.MockContext) {
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

func TestGitHubEnvironmentCredentialOptions(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)
	mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "/repos/Azure-Samples/my-repo/actions/oidc/customization/sub")
	}).Respond(exec.NewRunResult(
		0,
		`{"use_default": true, "include_claim_keys": []}`,
		"",
	))

	provider := createGitHubCiProvider(t, mockContext)
	provider.gitHubEnvironment = "prod:west"
	options, err := provider.credentialOptions(
		t.Context(),
		&gitRepositoryDetails{
			owner:    "Azure-Samples",
			repoName: "my-repo",
			branch:   "main",
		},
		provisioning.Options{},
		AuthTypeFederated,
		&entraid.AzureCredentials{},
	)
	require.NoError(t, err)
	require.True(t, options.EnableFederatedCredentials)
	require.Len(t, options.FederatedCredentialOptions, 1)
	require.Equal(
		t,
		"repo:Azure-Samples/my-repo:environment:prod%3Awest",
		options.FederatedCredentialOptions[0].Subject,
	)
}

func TestGitHubEnvironmentResolution(t *testing.T) {
	t.Parallel()

	t.Run("uses persisted mapping", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		provider := createGitHubCiProvider(t, mockContext)
		provider.env.DotenvSet(gitHubEnvironmentPersistedKey, "production")

		environmentName, err := provider.resolveEnvironment(
			t.Context(),
			&gitRepositoryDetails{owner: "o", repoName: "r"},
		)
		require.NoError(t, err)
		require.Equal(t, "production", environmentName)
	})

	t.Run("migrates matching repository configuration", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "variable list")
		}).Respond(exec.NewRunResult(0, "AZURE_ENV_NAME\ttest\n", ""))
		provider := createGitHubCiProvider(t, mockContext)

		environmentName, err := provider.resolveEnvironment(
			t.Context(),
			&gitRepositoryDetails{owner: "o", repoName: "r"},
		)
		require.NoError(t, err)
		require.Empty(t, environmentName)
	})

	t.Run("selects existing environment", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "variable list")
		}).Respond(exec.NewRunResult(0, "", ""))
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/o/r/environments?per_page=100")
		}).Respond(exec.NewRunResult(0, "development\nproduction\n", ""))
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select where GitHub Actions configuration")
		}).Respond(3)
		provider := createGitHubCiProvider(t, mockContext)

		environmentName, err := provider.resolveEnvironment(
			t.Context(),
			&gitRepositoryDetails{owner: "o", repoName: "r"},
		)
		require.NoError(t, err)
		require.Equal(t, "production", environmentName)
	})

	t.Run("creates environment after workflow validation", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "variable list")
		}).Respond(exec.NewRunResult(0, "", ""))
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/o/r/environments?per_page=100")
		}).Respond(exec.NewRunResult(0, "", ""))
		created := false
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "-X PUT") &&
				strings.Contains(cmd, "/repos/o/r/environments/development")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			created = true
			return exec.NewRunResult(0, "", ""), nil
		})
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select where GitHub Actions configuration")
		}).Respond(1)
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Enter a name for the new GitHub environment")
		}).Respond("development")
		provider := createGitHubCiProvider(t, mockContext)

		repoDetails := &gitRepositoryDetails{owner: "o", repoName: "r"}
		environmentName, err := provider.resolveEnvironment(t.Context(), repoDetails)
		require.NoError(t, err)
		require.Equal(t, "development", environmentName)
		require.False(t, created)

		require.NoError(t, provider.ensureEnvironment(t.Context(), repoDetails))
		require.True(t, created)
	})

	t.Run("no prompt defaults to repository scope", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "variable list")
		}).Respond(exec.NewRunResult(0, "", ""))
		provider := createGitHubCiProvider(t, mockContext)

		environmentName, err := provider.resolveEnvironment(
			t.Context(),
			&gitRepositoryDetails{owner: "o", repoName: "r"},
		)
		require.NoError(t, err)
		require.Empty(t, environmentName)
	})

	t.Run("list failure still allows repository scope", func(t *testing.T) {
		t.Parallel()

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "variable list")
		}).Respond(exec.NewRunResult(0, "", ""))
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/o/r/environments?per_page=100")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "forbidden"), errors.New("exit 1")
		})
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select where GitHub Actions configuration")
		}).Respond(0)
		provider := createGitHubCiProvider(t, mockContext)

		environmentName, err := provider.resolveEnvironment(
			t.Context(),
			&gitRepositoryDetails{owner: "o", repoName: "r"},
		)
		require.NoError(t, err)
		require.Empty(t, environmentName)
		require.Contains(
			t,
			strings.Join(mockContext.Console.Output(), "\n"),
			"GitHub environments could not be listed",
		)
	})
}

func Test_credentialNameSanitizer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple repo slug", "Azure/azure-dev", "Azure-azure-dev"},
		{"repo with dots", "my-org/my.repo.name", "my-org-my-repo-name"},
		{"repo with multiple special chars", "org/repo@v2.0", "org-repo-v2-0"},
		{"already safe", "my-org-my-repo", "my-org-my-repo"},
		{"underscores preserved", "org/my_repo", "org-my_repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := credentialNameSanitizer.ReplaceAllString(tt.input, "-")
			require.Equal(t, tt.expected, result)
		})
	}
}

func Test_credentialOptions_withOIDCCustomSubject(t *testing.T) {
	repoDetails := &gitRepositoryDetails{
		owner:    "Azure-Samples",
		repoName: "my-repo",
		branch:   "main",
	}

	repoSlug := "Azure-Samples/my-repo"

	// Helper to set up mock context with no-prompt mode
	// (tests run non-interactively so prompts are skipped)
	setupMock := func(t *testing.T) *mocks.MockContext {
		t.Helper()
		mc := mocks.NewMockContext(t.Context())
		setupGithubCliMocks(mc)
		mc.Console.SetNoPromptMode(true)
		return mc
	}

	t.Run("default OIDC subjects when API returns use_default", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/"+repoSlug+
				"/actions/oidc/customization/sub")
		}).Respond(exec.NewRunResult(
			0,
			`{"use_default": true, "include_claim_keys": []}`,
			"",
		))

		provider := createGitHubCiProvider(t, mockContext)
		opts, err := provider.credentialOptions(
			t.Context(),
			repoDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.NoError(t, err)
		require.True(t, opts.EnableFederatedCredentials)
		require.Len(t, opts.FederatedCredentialOptions, 2)

		prCred := opts.FederatedCredentialOptions[0]
		require.Equal(t,
			"repo:Azure-Samples/my-repo:pull_request",
			prCred.Subject,
		)

		mainCred := opts.FederatedCredentialOptions[1]
		require.Equal(t,
			"repo:Azure-Samples/my-repo:ref:refs/heads/main",
			mainCred.Subject,
		)
	})

	t.Run("immutable OIDC subjects use API prefix", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/"+repoSlug+
				"/actions/oidc/customization/sub")
		}).Respond(exec.NewRunResult(
			0,
			`{"use_default":true,"use_immutable_subject":false,`+
				`"sub_claim_prefix":`+
				`"repo:Azure-Samples@1844662/my-repo@599293758"}`,
			"",
		))

		provider := createGitHubCiProvider(t, mockContext)
		opts, err := provider.credentialOptions(
			t.Context(),
			repoDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.NoError(t, err)
		require.True(t, opts.EnableFederatedCredentials)
		require.Len(t, opts.FederatedCredentialOptions, 2)

		require.Equal(t,
			"repo:Azure-Samples@1844662/my-repo@599293758:pull_request",
			opts.FederatedCredentialOptions[0].Subject,
		)
		require.Equal(t,
			"repo:Azure-Samples@1844662/my-repo@599293758:"+
				"ref:refs/heads/main",
			opts.FederatedCredentialOptions[1].Subject,
		)
	})

	t.Run("custom immutable OIDC subjects use API prefix", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/"+repoSlug+
				"/actions/oidc/customization/sub")
		}).Respond(exec.NewRunResult(
			0,
			`{"use_default":false,"use_immutable_subject":true,`+
				`"sub_claim_prefix":`+
				`"repo:Azure-Samples@1844662/my-repo@599293758",`+
				`"include_claim_keys":["repo","context"]}`,
			"",
		))

		provider := createGitHubCiProvider(t, mockContext)
		opts, err := provider.credentialOptions(
			t.Context(),
			repoDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.NoError(t, err)
		require.True(t, opts.EnableFederatedCredentials)
		require.Len(t, opts.FederatedCredentialOptions, 2)

		require.Equal(t,
			"repo:Azure-Samples@1844662/my-repo@599293758:pull_request",
			opts.FederatedCredentialOptions[0].Subject,
		)
		require.Equal(t,
			"repo:Azure-Samples@1844662/my-repo@599293758:"+
				"ref:refs/heads/main",
			opts.FederatedCredentialOptions[1].Subject,
		)
	})

	t.Run("custom OIDC subjects with ID-based claims", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/"+repoSlug+
				"/actions/oidc/customization/sub")
		}).Respond(exec.NewRunResult(
			0,
			`{"use_default": false, "include_claim_keys": `+
				`["repository_owner_id", "repository_id"]}`,
			"",
		))

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/"+repoSlug) &&
				!strings.Contains(cmd, "oidc")
		}).Respond(exec.NewRunResult(
			0,
			`{"id": 599293758, "owner": {"id": 1844662}}`,
			"",
		))

		provider := createGitHubCiProvider(t, mockContext)
		opts, err := provider.credentialOptions(
			t.Context(),
			repoDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.NoError(t, err)
		require.True(t, opts.EnableFederatedCredentials)

		// Without "context" in claim keys, PR and branch subjects
		// collapse to the same value — deduplication yields 1 FIC.
		require.Len(t, opts.FederatedCredentialOptions, 1)

		prCred := opts.FederatedCredentialOptions[0]
		require.Equal(t,
			"repository_owner_id:1844662:"+
				"repository_id:599293758",
			prCred.Subject,
		)
	})

	t.Run("error on OIDC API failure", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "oidc/customization/sub")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "HTTP 403: Forbidden"),
				fmt.Errorf("HTTP 403: Forbidden")
		})

		provider := createGitHubCiProvider(t, mockContext)
		_, err := provider.credentialOptions(
			t.Context(),
			repoDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to query")
	})

	t.Run("multiple branches with custom OIDC", func(t *testing.T) {
		mockContext := setupMock(t)

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/Azure-Samples/my-repo"+
				"/actions/oidc/customization/sub")
		}).Respond(exec.NewRunResult(
			0,
			`{"use_default": false, "include_claim_keys": `+
				`["repository_owner_id", "repository_id"]}`,
			"",
		))

		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "/repos/Azure-Samples/my-repo") &&
				!strings.Contains(cmd, "oidc")
		}).Respond(exec.NewRunResult(
			0,
			`{"id": 599293758, "owner": {"id": 1844662}}`,
			"",
		))

		devDetails := &gitRepositoryDetails{
			owner:    "Azure-Samples",
			repoName: "my-repo",
			branch:   "develop",
		}

		provider := createGitHubCiProvider(t, mockContext)
		opts, err := provider.credentialOptions(
			t.Context(),
			devDetails,
			provisioning.Options{},
			AuthTypeFederated,
			nil,
		)
		require.NoError(t, err)
		// Without "context", all subjects collapse to the same value.
		// Deduplication yields only 1 FIC.
		require.Len(t, opts.FederatedCredentialOptions, 1)

		require.Contains(t,
			opts.FederatedCredentialOptions[0].Subject,
			"repository_owner_id:1844662",
		)
	})
}
