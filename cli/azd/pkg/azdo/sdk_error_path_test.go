// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ = provisioning.Bicep // keep import even if unused later

// newFailingConnection builds an azuredevops.Connection pointed at an httptest
// server that responds with HTTP 500 to every request. This forces the
// SDK-level client constructors (which eagerly call GetResourceAreas during
// NewClient) to return an error — which lets us cover the error branches of
// our SDK-wrapper functions without reimplementing the full REST surface.
func newFailingConnection(t *testing.T) *azuredevops.Connection {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forced failure", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return azuredevops.NewPatConnection(server.URL+"/org", "pat")
}

// newHalfWorkingConnection returns an azuredevops.Connection that allows the
// SDK's initial ResourceAreas/OPTIONS discovery to succeed (with an empty
// response, so the SDK falls back to BaseUrl), but fails any subsequent
// location-specific call with HTTP 500. This lets us exercise the code paths
// *after* a successful NewClient() call in the wrappers without needing to
// implement every REST endpoint the Azure DevOps SDK hits.
//
// Each call gets a fresh base URL (via a sub-path) so the SDK's global
// per-baseUrl location cache does not interfere between tests.
func newHalfWorkingConnection(t *testing.T, subPath string) *azuredevops.Connection {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Empty JSON collection for OPTIONS / ResourceAreas discovery.
		path := r.URL.Path
		if r.Method == http.MethodOptions || (r.Method == http.MethodGet &&
			(endsWith(path, "/_apis/ResourceAreas") || endsWith(path, "/_apis/resourceareas"))) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"count":0,"value":[]}`))
			return
		}
		http.Error(w, "forced failure", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return azuredevops.NewPatConnection(server.URL+"/"+subPath, "pat")
}

func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func TestServiceConnection_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	ep, err := ServiceConnection(t.Context(), conn, "proj", &ServiceConnectionName)
	require.Error(t, err)
	assert.Nil(t, ep)
}

func TestListTypes_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	types, err := ListTypes(t.Context(), conn, "proj")
	require.Error(t, err)
	assert.Nil(t, types)
}

func TestCreateServiceConnection_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	env := environment.NewWithValues("env", map[string]string{})
	mockConsole := mockinput.NewMockConsole()
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub",
		TenantId:       "tenant",
		ClientId:       "client",
	}
	ep, err := CreateServiceConnection(t.Context(), conn, "proj-id", "proj-name", env, creds, mockConsole)
	require.Error(t, err)
	assert.Nil(t, ep)
}

func TestGetProjectByName_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	project, err := GetProjectByName(t.Context(), conn, "some-project")
	require.Error(t, err)
	assert.Nil(t, project)
}

func TestGetProjectFromExisting_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	mockConsole := mockinput.NewMockConsole()
	name, id, err := GetProjectFromExisting(t.Context(), conn, mockConsole)
	require.Error(t, err)
	assert.Empty(t, name)
	assert.Empty(t, id)
}

func TestCreateRepository_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	repo, err := CreateRepository(t.Context(), "proj", "repo", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetDefaultGitRepositoriesInProject_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	repo, err := GetDefaultGitRepositoriesInProject(t.Context(), "proj", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetGitRepositoriesInProject_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	mockConsole := mockinput.NewMockConsole()
	repo, err := GetGitRepositoriesInProject(t.Context(), "proj", "org", conn, mockConsole)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetGitRepository_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	repo, err := GetGitRepository(t.Context(), "proj", "repo", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestQueueBuild_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	id := 1
	def := &build.BuildDefinition{Id: &id}
	err := QueueBuild(t.Context(), conn, "proj", def, "main")
	require.Error(t, err)
}

func TestCreatePipeline_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	env := environment.NewWithValues("env", map[string]string{})
	mockConsole := mockinput.NewMockConsole()
	def, err := CreatePipeline(
		t.Context(), "proj", "name", "repo",
		conn, nil, env, mockConsole,
		provisioning.Options{}, nil, nil,
	)
	require.Error(t, err)
	assert.Nil(t, def)
}

func TestCreateBuildPolicy_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	env := environment.NewWithValues("env", map[string]string{})
	id := 1
	def := &build.BuildDefinition{Id: &id}
	err := CreateBuildPolicy(t.Context(), conn, "proj", "repo", def, env)
	require.Error(t, err)
}

// The half-working connection lets NewClient() succeed and then fails the
// subsequent API call — exercising more of the wrapper function body.

func TestQueueBuild_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "queue-build")
	id := 1
	def := &build.BuildDefinition{Id: &id}
	err := QueueBuild(t.Context(), conn, "proj", def, "main")
	require.Error(t, err)
}

func TestCreateRepository_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "create-repo")
	repo, err := CreateRepository(t.Context(), "proj", "repo", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetDefaultGitRepositoriesInProject_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "default-repos")
	repo, err := GetDefaultGitRepositoriesInProject(t.Context(), "proj", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetGitRepositoriesInProject_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "get-repos")
	mockConsole := mockinput.NewMockConsole()
	repo, err := GetGitRepositoriesInProject(t.Context(), "proj", "org", conn, mockConsole)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetGitRepository_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "get-repo")
	repo, err := GetGitRepository(t.Context(), "proj", "repo", conn)
	require.Error(t, err)
	assert.Nil(t, repo)
}

func TestGetProjectByName_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "project-by-name")
	project, err := GetProjectByName(t.Context(), conn, "name")
	require.Error(t, err)
	assert.Nil(t, project)
}

func TestGetProjectFromExisting_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "project-from-existing")
	mockConsole := mockinput.NewMockConsole()
	name, id, err := GetProjectFromExisting(t.Context(), conn, mockConsole)
	require.Error(t, err)
	assert.Empty(t, name)
	assert.Empty(t, id)
}

func TestServiceConnection_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "svc-conn-get")
	ep, err := ServiceConnection(t.Context(), conn, "proj", &ServiceConnectionName)
	require.Error(t, err)
	assert.Nil(t, ep)
}

func TestListTypes_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "list-types")
	types, err := ListTypes(t.Context(), conn, "proj")
	require.Error(t, err)
	assert.Nil(t, types)
}

func TestCreatePipeline_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "create-pipeline")
	env := environment.NewWithValues("env", map[string]string{
		"AZURE_LOCATION": "eastus",
	})
	mockConsole := mockinput.NewMockConsole()
	def, err := CreatePipeline(
		t.Context(), "proj", "name", "repo",
		conn, nil, env, mockConsole,
		provisioning.Options{}, nil, nil,
	)
	require.Error(t, err)
	assert.Nil(t, def)
}

func TestCreateBuildPolicy_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "create-build-policy")
	env := environment.NewWithValues("env", map[string]string{})
	id := 1
	def := &build.BuildDefinition{Id: &id}
	err := CreateBuildPolicy(t.Context(), conn, "proj", "repo", def, env)
	require.Error(t, err)
}

func TestCreateServiceConnection_SdkCallErrorPath(t *testing.T) {
	conn := newHalfWorkingConnection(t, "create-svc-conn")
	env := environment.NewWithValues("env", map[string]string{})
	mockConsole := mockinput.NewMockConsole()
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub",
		TenantId:       "tenant",
		ClientId:       "client",
	}
	ep, err := CreateServiceConnection(t.Context(), conn, "proj-id", "proj-name", env, creds, mockConsole)
	require.Error(t, err)
	assert.Nil(t, ep)
}
