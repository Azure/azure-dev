// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package show

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "rg-test"
)

// mockJobGetResponse registers a mock HTTP handler that responds to GET
// requests for a Container App Job resource.
func mockJobGetResponse(
	mockContext *mocks.MockContext,
	jobName string,
	job *armappcontainers.Job,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/jobs/%s",
				testSubscriptionID,
				testResourceGroup,
				jobName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappcontainers.JobsClientGetResponse{
			Job: *job,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func newJobResourceID(jobName string) *arm.ResourceID {
	id, err := arm.ParseResourceID(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/jobs/%s",
		testSubscriptionID,
		testResourceGroup,
		jobName,
	))
	if err != nil {
		panic(fmt.Sprintf("failed to parse test resource ID: %v", err))
	}
	return id
}

func Test_showContainerAppJob_SingleContainer(t *testing.T) {
	jobName := "my-job"
	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr("myregistry.azurecr.io/myimage:latest"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  to.Ptr("APP_ENV"),
								Value: to.Ptr("production"),
							},
							{
								Name:  to.Ptr("APP_PORT"),
								Value: to.Ptr("8080"),
							},
							{
								Name:      to.Ptr("DB_PASSWORD"),
								SecretRef: to.Ptr("db-password-secret"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	require.Equal(t, jobName, service.Name)
	require.Equal(t, "Container App Job", service.DisplayType)
	require.Equal(t, "production", service.Env["APP_ENV"])
	require.Equal(t, "8080", service.Env["APP_PORT"])
	require.Equal(t, "*******", service.Env["DB_PASSWORD"])
	require.Empty(t, service.IngresUrl)
}

func Test_showContainerAppJob_NilProperties(t *testing.T) {
	jobName := "nil-props-job"
	job := &armappcontainers.Job{
		Name:       to.Ptr(jobName),
		Properties: nil,
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	require.Equal(t, jobName, service.Name)
	require.Equal(t, "Container App Job", service.DisplayType)
	require.Empty(t, service.Env)
}

func Test_showContainerAppJob_MultiContainer_MatchByName(t *testing.T) {
	jobName := "multi-job"
	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr("sidecar"),
						Image: to.Ptr("sidecar:latest"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  to.Ptr("SIDECAR_VAR"),
								Value: to.Ptr("sidecar-value"),
							},
						},
					},
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr("main-app:latest"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  to.Ptr("MAIN_VAR"),
								Value: to.Ptr("main-value"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	require.Equal(t, jobName, service.Name)
	// Should pick the container matching the job name, not the sidecar
	require.Equal(t, "main-value", service.Env["MAIN_VAR"])
	require.NotContains(t, service.Env, "SIDECAR_VAR")
}

func Test_showContainerAppJob_MultiContainer_NoMatch(t *testing.T) {
	jobName := "no-match-job"
	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					{
						Name:  to.Ptr("worker-a"),
						Image: to.Ptr("worker-a:latest"),
					},
					{
						Name:  to.Ptr("worker-b"),
						Image: to.Ptr("worker-b:latest"),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.Error(t, err)
	require.Nil(t, service)
	require.Contains(t, err.Error(), "has more than one container")
	require.Contains(t, err.Error(), jobName)
}

func Test_showContainerAppJob_NilContainerElement(t *testing.T) {
	jobName := "nil-elem-job"
	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					nil,
					{
						Name:  to.Ptr(jobName),
						Image: to.Ptr("app:latest"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  to.Ptr("FOUND"),
								Value: to.Ptr("yes"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	require.Equal(t, "yes", service.Env["FOUND"])
}

func Test_showContainerAppJob_SingleNilContainer(t *testing.T) {
	jobName := "single-nil-job"
	job := &armappcontainers.Job{
		Name: to.Ptr(jobName),
		Properties: &armappcontainers.JobProperties{
			Template: &armappcontainers.JobTemplate{
				Containers: []*armappcontainers.Container{
					nil,
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockJobGetResponse(mockContext, jobName, job)

	id := newJobResourceID(jobName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerAppJob(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	require.Equal(t, jobName, service.Name)
	require.Equal(t, "Container App Job", service.DisplayType)
	require.Empty(t, service.Env)
}
