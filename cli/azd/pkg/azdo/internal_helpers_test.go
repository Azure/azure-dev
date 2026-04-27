// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLocalClient_ErrorPath covers the error branch of newLocalClient by
// pointing it at an httptest server that fails to serve resource-area
// discovery.
func TestNewLocalClient_ErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	c, err := newLocalClient(t.Context(), conn)
	require.Error(t, err)
	assert.Nil(t, c)
}

// TestGetPolicyConfigurations_NilProjectError covers the argument-validation
// branch of clientImpl.getPolicyConfigurations.
func TestGetPolicyConfigurations_NilProjectError(t *testing.T) {
	c := &clientImpl{Client: azuredevops.Client{}}
	// nil project pointer
	resp, err := c.getPolicyConfigurations(t.Context(), getPolicyConfigurationsArgs{Project: nil})
	require.Error(t, err)
	assert.Nil(t, resp)

	// empty project string
	empty := ""
	resp, err = c.getPolicyConfigurations(t.Context(), getPolicyConfigurationsArgs{Project: &empty})
	require.Error(t, err)
	assert.Nil(t, resp)
}

// TestGetAgentQueue_NewClientErrorPath covers the NewClient-error branch of
// getAgentQueue.
func TestGetAgentQueue_NewClientErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	mockConsole := mockinput.NewMockConsole()
	q, err := getAgentQueue(t.Context(), "proj", conn, mockConsole)
	require.Error(t, err)
	assert.Nil(t, q)
}

// TestGetProjectFromNew_CreateProjectErrorPath covers GetProjectFromNew's
// "creating project" failure branch: the prompt returns a name, then
// createProject fails at core.NewClient, and the error message does not match
// either of the two string-matched retry cases, so the function returns with
// the wrapped creation error.
func TestGetProjectFromNew_CreateProjectErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	env := environment.NewWithValues("env", map[string]string{})
	mockConsole := mockinput.NewMockConsole()
	mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).Respond("my-project")

	name, id, err := GetProjectFromNew(t.Context(), ".", conn, env, mockConsole)
	require.Error(t, err)
	assert.Empty(t, name)
	assert.Empty(t, id)
}

// TestGetProjectFromNew_PromptErrorPath covers the branch where the prompt
// itself returns an error.
func TestGetProjectFromNew_PromptErrorPath(t *testing.T) {
	conn := newFailingConnection(t)
	env := environment.NewWithValues("env", map[string]string{})
	mockConsole := mockinput.NewMockConsole()
	mockConsole.WhenPrompt(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			return "", assert.AnError
		})

	name, id, err := GetProjectFromNew(t.Context(), ".", conn, env, mockConsole)
	require.Error(t, err)
	assert.Empty(t, name)
	assert.Empty(t, id)
}

// stubBuildClient implements build.Client. Only GetDefinitions is implemented;
// any other method call causes a nil-pointer panic (via the embedded nil
// interface). This keeps the stub small while still letting us test the code
// paths that only call GetDefinitions.
type stubBuildClient struct {
	build.Client
	getDefinitionsCallCount int
	// response/err to return from GetDefinitions
	response *build.GetDefinitionsResponseValue
	err      error
}

func (s *stubBuildClient) GetDefinitions(
	ctx context.Context, args build.GetDefinitionsArgs,
) (*build.GetDefinitionsResponseValue, error) {
	s.getDefinitionsCallCount++
	return s.response, s.err
}

// TestGetDefinitionsPager_FetcherErrorPath drives the pager through one
// iteration where GetDefinitions returns an error, covering the pager's
// Fetcher error branch.
func TestGetDefinitionsPager_FetcherErrorPath(t *testing.T) {
	name := "my-pipeline"
	projectId := "proj"
	stub := &stubBuildClient{err: assert.AnError}

	pager := getDefinitionsPager(stub, &projectId, &name)
	require.NotNil(t, pager)
	require.True(t, pager.More())
	_, err := pager.NextPage(t.Context())
	require.Error(t, err)
	assert.Equal(t, 1, stub.getDefinitionsCallCount)
}

// TestGetPipelineDefinition_NotFound covers getPipelineDefinition when
// GetDefinitions returns a successful empty page (no matching definition),
// exercising the happy-path body of the pager and the final "return nil, nil".
func TestGetPipelineDefinition_NotFound(t *testing.T) {
	name := "pipe"
	projectId := "proj"
	// Response with no definitions and no continuation token → pager stops.
	stub := &stubBuildClient{
		response: &build.GetDefinitionsResponseValue{
			Value:             []build.BuildDefinitionReference{},
			ContinuationToken: "",
		},
	}

	def, err := getPipelineDefinition(t.Context(), stub, &projectId, &name)
	require.NoError(t, err)
	assert.Nil(t, def)
}

// TestGetPipelineDefinition_FetcherError covers getPipelineDefinition when
// the underlying pager's Fetcher returns an error, exercising the error
// return path inside the for-range loop.
func TestGetPipelineDefinition_FetcherError(t *testing.T) {
	name := "pipe"
	projectId := "proj"
	stub := &stubBuildClient{err: assert.AnError}

	def, err := getPipelineDefinition(t.Context(), stub, &projectId, &name)
	require.Error(t, err)
	assert.Nil(t, def)
}
