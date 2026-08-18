// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestGitHubRepositoryStatusChecker(t *testing.T) {
	t.Run("ArchivedGitHubRepository", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		mockTransport := mockhttp.NewMockHttpUtil()
		mockTransport.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				req.URL.String() == "https://api.github.com/repos/Azure-Samples/todo-csharp-sql-swa-func"
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, map[string]any{
				"archived": true,
			})
		})

		checker := NewGitHubRepositoryStatusChecker(mockTransport)
		status, err := checker.Check(
			t.Context(),
			"https://github.com/Azure-Samples/todo-csharp-sql-swa-func.git",
		)

		require.NoError(t, err)
		require.NotNil(t, status)
		require.True(t, status.Archived)
	})

	t.Run("GitHubEnterpriseServerRepository", func(t *testing.T) {
		t.Setenv("GH_HOST", "github.contoso.com")
		t.Setenv("GH_TOKEN", "cloud-token")
		t.Setenv("GH_ENTERPRISE_TOKEN", "server-token")
		mockTransport := mockhttp.NewMockHttpUtil()
		mockTransport.When(func(req *http.Request) bool {
			return req.URL.String() == "https://github.contoso.com/api/v3/repos/contoso/template" &&
				req.Header.Get("Authorization") == "Bearer server-token"
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, map[string]any{
				"archived": false,
			})
		})

		checker := NewGitHubRepositoryStatusChecker(mockTransport)
		status, err := checker.Check(t.Context(), "git@github.contoso.com:contoso/template.git")

		require.NoError(t, err)
		require.NotNil(t, status)
		require.False(t, status.Archived)
	})

	t.Run("GitHubEnterpriseCloudRepository", func(t *testing.T) {
		t.Setenv("GH_HOST", "octocorp.ghe.com")
		t.Setenv("GH_TOKEN", "cloud-token")
		t.Setenv("GH_ENTERPRISE_TOKEN", "server-token")
		mockTransport := mockhttp.NewMockHttpUtil()
		mockTransport.When(func(req *http.Request) bool {
			return req.URL.String() == "https://api.octocorp.ghe.com/repos/contoso/template" &&
				req.Header.Get("Authorization") == "Bearer cloud-token"
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, map[string]any{
				"archived": true,
			})
		})

		checker := NewGitHubRepositoryStatusChecker(mockTransport)
		status, err := checker.Check(t.Context(), "https://octocorp.ghe.com/contoso/template")

		require.NoError(t, err)
		require.NotNil(t, status)
		require.True(t, status.Archived)
	})

	t.Run("UnsupportedRepositoryHost", func(t *testing.T) {
		checker := NewGitHubRepositoryStatusChecker(mockhttp.NewMockHttpUtil())
		status, err := checker.Check(t.Context(), "https://gitlab.com/contoso/template")

		require.NoError(t, err)
		require.Nil(t, status)
	})

	t.Run("MetadataRequestFailure", func(t *testing.T) {
		mockTransport := mockhttp.NewMockHttpUtil()
		mockTransport.When(func(req *http.Request) bool {
			return true
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusForbidden)
		})

		checker := NewGitHubRepositoryStatusChecker(mockTransport)
		status, err := checker.Check(t.Context(), "https://github.com/contoso/template")

		require.ErrorContains(t, err, "HTTP 403")
		require.Nil(t, status)
	})

	t.Run("MetadataRequestTimeout", func(t *testing.T) {
		mockTransport := mockhttp.NewMockHttpUtil()
		mockTransport.When(func(req *http.Request) bool {
			return true
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})

		checker := NewGitHubRepositoryStatusChecker(mockTransport).(*githubRepositoryStatusChecker)
		checker.requestTimeout = time.Millisecond
		status, err := checker.Check(t.Context(), "https://github.com/contoso/template")

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Nil(t, status)
	})
}

func TestInitializerConfirmArchivedTemplate(t *testing.T) {
	t.Run("ActiveRepositoryContinuesWithoutPrompt", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{status: &RepositoryStatus{}},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.NoError(t, err)
		require.Empty(t, console.Output())
	})

	t.Run("ArchivedRepositoryAccepted", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			require.Equal(t, "Do you want to continue using this archived template?", options.Message)
			require.Equal(t, false, options.DefaultValue)
			return true
		}).Respond(true)
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{status: &RepositoryStatus{Archived: true}},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.NoError(t, err)
		require.Contains(t, console.Output()[0], "WARNING:")
		require.Contains(t, console.Output()[0], "no longer actively maintained")
		require.Contains(t, console.Output()[1], "security patches")
	})

	t.Run("ArchivedRepositoryDeclined", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return options.DefaultValue == false
		}).Respond(false)
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{status: &RepositoryStatus{Archived: true}},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.ErrorIs(t, err, ErrArchivedTemplateDeclined)
	})

	t.Run("ArchivedRepositoryNoPrompt", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.SetNoPromptMode(true)
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{status: &RepositoryStatus{Archived: true}},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.ErrorContains(t, err, "requires confirmation")
		require.ErrorContains(t, err, "rerun without --no-prompt")
	})

	t.Run("MetadataFailureContinues", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{err: errors.New("rate limited")},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.NoError(t, err)
		require.Empty(t, console.Output())
	})

	t.Run("CancellationStopsInitialization", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		initializer := &Initializer{
			console:       console,
			statusChecker: &fakeRepositoryStatusChecker{err: context.Canceled},
		}

		err := initializer.confirmArchivedTemplate(t.Context(), "https://github.com/contoso/template")

		require.ErrorIs(t, err, context.Canceled)
	})
}

type fakeRepositoryStatusChecker struct {
	status *RepositoryStatus
	err    error
}

func (f *fakeRepositoryStatusChecker) Check(context.Context, string) (*RepositoryStatus, error) {
	return f.status, f.err
}
