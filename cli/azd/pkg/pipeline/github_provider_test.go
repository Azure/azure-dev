// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_gitHub_provider_getRepoDetails(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "https://github.com/Azure/azure-dev.git")
		require.NoError(t, e)
		require.Equal(t, "Azure", details.owner)
		require.Equal(t, "azure-dev", details.repoName)
	})
	t.Run("ssh", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "git@github.com:Azure/azure-dev.git")
		require.NoError(t, e)
		require.EqualValues(t, "Azure", details.owner)
		require.EqualValues(t, "azure-dev", details.repoName)
	})
	t.Run("error", func(t *testing.T) {
		provider := &GitHubScmProvider{}
		ctx := context.Background()
		details, e := provider.gitRepoDetails(ctx, "gt@other.com:Azure/azure-dev.git")
		require.Error(t, e, ErrRemoteHostIsNotGitHub)
		require.EqualValues(t, (*gitRepositoryDetails)(nil), details)
	})
}

func Test_gitHub_provider_preConfigure_check(t *testing.T) {
	t.Run("success with all default values", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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

		mockContext := mocks.NewMockContext(context.Background())
		setupGithubCliMocks(mockContext)

		provider := createGitHubCiProvider(t, mockContext)
		updatedConfig, err := provider.preConfigureCheck(*mockContext.Context, pipelineManagerArgs, infraOptions, "")
		require.NoError(t, err)
		require.False(t, updatedConfig)
	})
}

func createGitHubCiProvider(t *testing.T, mockContext *mocks.MockContext) CiProvider {
	env := environment.New("test")
	ghCli, err := github.NewGitHubCli(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
	)
	require.NoError(t, err)

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
	)
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

func Test_setPipelineVariables_environmentScope(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("Dev")

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	provider := NewGitHubCiProvider(env, mockContext.SubscriptionCredentialProvider, entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions, mockContext.CoreClientOptions,
	), ghCli, git.NewCli(mockContext.CommandRunner), mockContext.Console)

	var envVarCalls []string
	// Allow other gh commands (auth status, version, etc.) to succeed.
	// IMPORTANT: Generic matcher must be registered BEFORE the specific matcher below because
	// the mock runner searches from the end (LIFO) and picks the first predicate that matches.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == ghCli.BinaryPath()
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Specific matcher for environment-scoped variable set; register LAST so it wins over generic.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		if args.Cmd != ghCli.BinaryPath() {
			return false
		}
		// Expect tokens: -R owner/repo variable set NAME -e Dev
		hasR := false
		hasRepo := false
		hasVariableSet := false
		hasEnv := false
		for i := 0; i < len(args.Args); i++ {
			a := args.Args[i]
			if a == "-R" && i+1 < len(args.Args) && args.Args[i+1] == "owner/repo" {
				hasR = true
				hasRepo = true
			}
			if a == "variable" && i+1 < len(args.Args) && args.Args[i+1] == "set" {
				hasVariableSet = true
			}
			if a == "-e" && i+1 < len(args.Args) && args.Args[i+1] == "Dev" {
				hasEnv = true
			}
		}
		return hasR && hasRepo && hasVariableSet && hasEnv
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		envVarCalls = append(envVarCalls, strings.Join(args.Args, " "))
		return exec.NewRunResult(0, "", ""), nil
	})

	err = provider.(*GitHubCiProvider).setPipelineVariables(
		*mockContext.Context, "owner/repo", provisioning.Options{}, "tenant", "client")
	require.NoError(t, err)
	require.NotEmpty(t, envVarCalls, "expected environment-scoped variable set calls")
}

func Test_setPipelineVariables_callsEnsureEnvironment(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("EnsEnv")

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	provider := NewGitHubCiProvider(env, mockContext.SubscriptionCredentialProvider, entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions, mockContext.CoreClientOptions,
	), ghCli, git.NewCli(mockContext.CommandRunner), mockContext.Console)

	ensureCalled := false

	// Generic matcher first
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == ghCli.BinaryPath()
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Specific matcher for EnsureEnvironment (gh api repos/<slug>/environments/<env> --method PUT)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		if args.Cmd != ghCli.BinaryPath() {
			return false
		}
		hasApi := false
		hasPath := false
		hasMethod := false
		hasPut := false
		for i := 0; i < len(args.Args); i++ {
			a := args.Args[i]
			if a == "api" {
				hasApi = true
			}
			if strings.HasPrefix(a, "repos/owner/repo/environments/EnsEnv") {
				hasPath = true
			}
			if a == "--method" {
				hasMethod = true
				if i+1 < len(args.Args) && strings.EqualFold(args.Args[i+1], "PUT") {
					hasPut = true
				}
			}
		}
		return hasApi && hasPath && hasMethod && hasPut
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ensureCalled = true
		return exec.NewRunResult(0, "", ""), nil
	})

	err = provider.(*GitHubCiProvider).setPipelineVariables(
		*mockContext.Context,
		"owner/repo",
		provisioning.Options{},
		"tenant",
		"client",
	)
	require.NoError(t, err)
	require.True(t, ensureCalled, "expected EnsureEnvironment API call to have been invoked")
}

func Test_credentialOptions_includesEnvironmentFederatedSubject(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("Prod")

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	provider := NewGitHubCiProvider(env, mockContext.SubscriptionCredentialProvider, entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions, mockContext.CoreClientOptions,
	), ghCli, git.NewCli(mockContext.CommandRunner), mockContext.Console)

	repoDetails := &gitRepositoryDetails{owner: "org", repoName: "repo", branch: "feature"}
	creds, err := provider.(*GitHubCiProvider).credentialOptions(
		*mockContext.Context, repoDetails, provisioning.Options{}, AuthTypeFederated, nil)
	require.NoError(t, err)
	require.True(t, creds.EnableFederatedCredentials)
	found := false
	re := regexp.MustCompile(`repo:org/repo:environment:Prod`)
	for _, fc := range creds.FederatedCredentialOptions {
		if re.MatchString(fc.Subject) {
			found = true
			break
		}
	}
	require.True(t, found, "expected environment federated subject present")
}

func Test_configurePipeline_environmentScopeSecrets(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("Stage")

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	_ = env // reserved for future extended simulation

	// Generic gh matcher first
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == ghCli.BinaryPath()
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) { return exec.NewRunResult(0, "", ""), nil })

	var secretSetCalls []string
	// Specific matcher for environment secret set
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		if args.Cmd != ghCli.BinaryPath() {
			return false
		}
		hasR, hasRepo, hasSecretSet, hasEnv := false, false, false, false
		for i := 0; i < len(args.Args); i++ {
			a := args.Args[i]
			if a == "-R" && i+1 < len(args.Args) && args.Args[i+1] == "owner/repo" {
				hasR = true
				hasRepo = true
			}
			if a == "secret" && i+1 < len(args.Args) && args.Args[i+1] == "set" {
				hasSecretSet = true
			}
			if a == "-e" && i+1 < len(args.Args) && args.Args[i+1] == "Stage" {
				hasEnv = true
			}
		}
		return hasR && hasRepo && hasSecretSet && hasEnv
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		secretSetCalls = append(secretSetCalls, strings.Join(args.Args, " "))
		return exec.NewRunResult(0, "", ""), nil
	})

	// Directly call ghCli SetEnvironmentSecret to validate environment flag usage for secrets.
	err = ghCli.SetEnvironmentSecret(*mockContext.Context, "owner/repo", "Stage", "TEST_SECRET", "value")
	require.NoError(t, err)
	require.NotEmpty(t, secretSetCalls, "expected environment-scoped secret set calls")
}

func Test_credentialOptions_allFederatedSubjectsWithEnvironment(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("EnvX")

	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	provider := NewGitHubCiProvider(env, mockContext.SubscriptionCredentialProvider, entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions, mockContext.CoreClientOptions,
	), ghCli, git.NewCli(mockContext.CommandRunner), mockContext.Console)

	repoDetails := &gitRepositoryDetails{owner: "org", repoName: "repo", branch: "feature"}
	creds, err := provider.(*GitHubCiProvider).credentialOptions(
		*mockContext.Context, repoDetails, provisioning.Options{}, AuthTypeFederated, nil)
	require.NoError(t, err)
	require.True(t, creds.EnableFederatedCredentials)

	expected := map[string]bool{
		"repo:org/repo:pull_request":           false,
		"repo:org/repo:ref:refs/heads/feature": false,
		"repo:org/repo:ref:refs/heads/main":    false,
		"repo:org/repo:environment:EnvX":       false,
	}
	for _, fc := range creds.FederatedCredentialOptions {
		if _, ok := expected[fc.Subject]; ok {
			expected[fc.Subject] = true
		}
	}
	for subj, found := range expected {
		require.True(t, found, "missing federated credential subject %s", subj)
	}
}

func Test_credentialOptions_envOnlyMode(t *testing.T) {
	t.Setenv("AZD_USE_GITHUB_ENVIRONMENTS", "1")
	mockContext := mocks.NewMockContext(context.Background())
	setupGithubCliMocks(mockContext)
	env := environment.New("staging")
	ghCli, err := github.NewGitHubCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	provider := NewGitHubCiProvider(env, mockContext.SubscriptionCredentialProvider, entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions, mockContext.CoreClientOptions,
	), ghCli, git.NewCli(mockContext.CommandRunner), mockContext.Console)

	repoDetails := &gitRepositoryDetails{owner: "azure", repoName: "sample", branch: "feature/x"}
	creds := &entraid.AzureCredentials{ClientId: "client", TenantId: "tenant", SubscriptionId: "sub"}
	options, err := provider.credentialOptions(
		*mockContext.Context,
		repoDetails,
		provisioning.Options{},
		AuthTypeFederated,
		creds,
	)
	require.NoError(t, err)
	require.True(t, options.EnableFederatedCredentials)
	subjects := []string{}
	for _, fc := range options.FederatedCredentialOptions {
		subjects = append(subjects, fc.Subject)
	}
	for _, s := range subjects {
		require.NotContains(t, s, ":pull_request")
	}
	for _, s := range subjects {
		require.NotContains(t, s, ":ref:refs/heads/")
	}
	foundEnv := false
	for _, s := range subjects {
		if strings.Contains(s, ":environment:staging") {
			foundEnv = true
		}
	}
	require.True(t, foundEnv, "expected only environment subject present")
	require.Equal(t, 1, len(subjects), "expected exactly one federated credential (environment)")
}
