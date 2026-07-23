// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_isBuildFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{"nil error", nil, false},
		{"unrelated error", errors.New("connection refused"), false},
		{"transient build failure", errors.New("the build process failed"), true},
		{"transient build failure wrapped", errors.New("deploy error: the build process failed with exit code 1"), true},
		{"real build failure with logs", errors.New("the build process failed, check logs for more info"), false},
		{"genuine build failure from status API",
			errors.New("Deployment failed because the build process failed\n"), false},
		{"partial match", errors.New("build process"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expect, isBuildFailure(tt.err))
		})
	}
}

// mockScmChecker is a mock implementation of scmReadyChecker for testing.
type mockScmChecker struct {
	calls atomic.Int32
	fn    func(ctx context.Context, call int) (bool, error)
}

func (m *mockScmChecker) IsScmReady(ctx context.Context) (bool, error) {
	call := int(m.calls.Add(1))
	return m.fn(ctx, call)
}

func Test_waitForScmReady_ImmediateSuccess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mock := &mockScmChecker{fn: func(_ context.Context, _ int) (bool, error) {
		return true, nil
	}}

	var logs []string
	err := waitForScmReady(ctx, mock, time.Millisecond, func(msg string) { logs = append(logs, msg) })

	require.NoError(t, err)
	require.Equal(t, int32(1), mock.calls.Load())
	require.Contains(t, logs, "SCM site is ready")
}

func Test_waitForScmReady_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Mock returns not-ready on first probe, then context.Canceled on the second call
	// to exercise the error propagation path inside the ticker loop.
	mock := &mockScmChecker{fn: func(_ context.Context, call int) (bool, error) {
		if call == 1 {
			return false, nil // immediate probe: not ready
		}
		cancel()
		return false, context.Canceled
	}}

	err := waitForScmReady(ctx, mock, time.Millisecond, func(string) {})

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, int(mock.calls.Load()), 2, "IsScmReady should be called at least twice")
}

func Test_waitForScmReady_TransientErrorsThenSuccess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	mock := &mockScmChecker{fn: func(_ context.Context, call int) (bool, error) {
		if call < 3 {
			return false, errors.New("transient error")
		}
		return true, nil
	}}

	err := waitForScmReady(ctx, mock, time.Millisecond, func(string) {})

	require.NoError(t, err)
	require.GreaterOrEqual(t, int(mock.calls.Load()), 3)
}

func Test_waitForScmReady_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()

	// Mock returns not-ready on first probe, then DeadlineExceeded on subsequent calls
	// to exercise the error propagation path inside the ticker loop.
	mock := &mockScmChecker{fn: func(_ context.Context, call int) (bool, error) {
		if call == 1 {
			return false, nil // immediate probe: not ready
		}
		return false, context.DeadlineExceeded
	}}

	err := waitForScmReady(t.Context(), mock, time.Millisecond, func(string) {})

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, int(mock.calls.Load()), 2, "IsScmReady should be called at least twice")
}

func Test_AzureClient_GetAppServiceProperties(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-app") &&
			!strings.Contains(req.URL.Path, "/slots/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.Site{
				ID:       new("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/my-app"),
				Name:     new("my-app"),
				Location: new("eastus"),
				Kind:     new("app,linux"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName:   new("my-app.azurewebsites.net"),
					HTTPSOnly:         new(true),
					EnabledHostNames:  []*string{new("my-app.azurewebsites.net")},
					HostNameSSLStates: []*armappservice.HostNameSSLState{},
					SiteConfig:        &armappservice.SiteConfig{LinuxFxVersion: new("NODE|18-lts")},
					AvailabilityState: to.Ptr(armappservice.SiteAvailabilityStateNormal),
				},
			})
	})

	props, err := client.GetAppServiceProperties(*mockCtx.Context, "SUB", "RG", "my-app")
	require.NoError(t, err)
	assert.Contains(t, props.HostNames, "my-app.azurewebsites.net")
}

func Test_AzureClient_GetAppServiceSlotProperties(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/slots/staging")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.Site{
				ID:       new("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/my-app/slots/staging"),
				Name:     new("my-app/staging"),
				Location: new("eastus"),
				Kind:     new("app,linux"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName:   new("my-app-staging.azurewebsites.net"),
					HTTPSOnly:         new(true),
					EnabledHostNames:  []*string{new("my-app-staging.azurewebsites.net")},
					HostNameSSLStates: []*armappservice.HostNameSSLState{},
					SiteConfig:        &armappservice.SiteConfig{LinuxFxVersion: new("NODE|18-lts")},
					AvailabilityState: to.Ptr(armappservice.SiteAvailabilityStateNormal),
				},
			})
	})

	props, err := client.GetAppServiceSlotProperties(*mockCtx.Context, "SUB", "RG", "my-app", "staging")
	require.NoError(t, err)
	assert.Contains(t, props.HostNames, "my-app-staging.azurewebsites.net")
}

func Test_AzureClient_UpdateAppServiceContainerImage(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	var capturedBody string
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPatch &&
			strings.HasSuffix(req.URL.Path, "/Microsoft.Web/sites/my-app/config/web") &&
			!strings.Contains(req.URL.Path, "/slots/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		capturedBody = string(bodyBytes)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.SiteConfigResource{
				Properties: &armappservice.SiteConfig{
					LinuxFxVersion: new("DOCKER|myregistry.azurecr.io/myapp:v1"),
				},
			})
	})

	err := client.UpdateAppServiceContainerImage(
		*mockCtx.Context, "SUB", "RG", "my-app", "myregistry.azurecr.io/myapp:v1")
	require.NoError(t, err)
	assert.NotEmpty(t, capturedBody, "Update should have been called with a body")
	assert.JSONEq(t, `{
		"properties": {
			"linuxFxVersion": "DOCKER|myregistry.azurecr.io/myapp:v1"
		}
	}`, capturedBody, "body should patch only linuxFxVersion on the configuration resource")
}

func Test_AzureClient_UpdateAppServiceContainerImage_Error(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPatch &&
			strings.HasSuffix(req.URL.Path, "/Microsoft.Web/sites/my-app/config/web")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
	})

	err := client.UpdateAppServiceContainerImage(
		*mockCtx.Context, "SUB", "RG", "my-app", "myregistry.azurecr.io/myapp:v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating container image")
}

func Test_AzureClient_UpdateAppServiceSlotContainerImage(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	var capturedBody string
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPatch &&
			strings.HasSuffix(req.URL.Path, "/slots/staging/config/web")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		capturedBody = string(bodyBytes)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.SiteConfigResource{
				Properties: &armappservice.SiteConfig{
					LinuxFxVersion: new("DOCKER|myregistry.azurecr.io/myapp:v1"),
				},
			})
	})

	err := client.UpdateAppServiceSlotContainerImage(
		*mockCtx.Context, "SUB", "RG", "my-app", "staging", "myregistry.azurecr.io/myapp:v1")
	require.NoError(t, err)
	assert.NotEmpty(t, capturedBody, "UpdateSlot should have been called with a body")
	assert.JSONEq(t, `{
		"properties": {
			"linuxFxVersion": "DOCKER|myregistry.azurecr.io/myapp:v1"
		}
	}`, capturedBody, "body should patch only linuxFxVersion on the slot configuration resource")
}

func Test_AzureClient_ValidateAppServiceForContainerDeploy(t *testing.T) {
	t.Run("ValidLinuxContainer_NoError", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-app")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armappservice.Site{
					Kind: new("app,linux,container"),
					Properties: &armappservice.SiteProperties{
						SiteConfig: &armappservice.SiteConfig{
							LinuxFxVersion: new("DOCKER|myregistry.azurecr.io/myapp:v1"),
						},
					},
				})
		})

		err := client.ValidateAppServiceForContainerDeploy(*mockCtx.Context, "SUB", "RG", "my-app")
		require.NoError(t, err)
	})

	t.Run("NotLinux_ReturnsError", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-app")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armappservice.Site{
					Kind: new("app"),
					Properties: &armappservice.SiteProperties{
						SiteConfig: &armappservice.SiteConfig{},
					},
				})
		})

		err := client.ValidateAppServiceForContainerDeploy(*mockCtx.Context, "SUB", "RG", "my-app")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not configured as a Linux app")
	})

	t.Run("NoDockerFxVersion_ReturnsError", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-app")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armappservice.Site{
					Kind: new("app,linux"),
					Properties: &armappservice.SiteProperties{
						SiteConfig: &armappservice.SiteConfig{
							LinuxFxVersion: new("NODE|18-lts"),
						},
					},
				})
		})

		err := client.ValidateAppServiceForContainerDeploy(*mockCtx.Context, "SUB", "RG", "my-app")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not configured for container deployment")
	})
}

func Test_AzureClient_GetAppServiceContainerConfiguration(t *testing.T) {
	tests := []struct {
		name            string
		kind            *string
		siteConfig      *armappservice.SiteConfig
		expectLinux     bool
		expectContainer bool
	}{
		{
			name:            "LinuxContainer",
			kind:            new("functionapp,linux,container"),
			siteConfig:      &armappservice.SiteConfig{LinuxFxVersion: new("DOCKER|registry/image:tag")},
			expectLinux:     true,
			expectContainer: true,
		},
		{
			name:            "CaseInsensitive",
			kind:            new("FUNCTIONAPP,LINUX,CONTAINER"),
			siteConfig:      &armappservice.SiteConfig{LinuxFxVersion: new("docker|registry/image:tag")},
			expectLinux:     true,
			expectContainer: true,
		},
		{
			name:        "LinuxCode",
			kind:        new("functionapp,linux"),
			siteConfig:  &armappservice.SiteConfig{LinuxFxVersion: new("NODE|20-lts")},
			expectLinux: true,
		},
		{
			name:       "Windows",
			kind:       new("functionapp"),
			siteConfig: &armappservice.SiteConfig{},
		},
		{
			name: "MissingProperties",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCtx := mocks.NewMockContext(t.Context())
			client := newAzureClientFromMockContext(mockCtx)

			mockCtx.HttpClient.When(func(req *http.Request) bool {
				return req.Method == http.MethodGet &&
					strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-function")
			}).RespondFn(func(req *http.Request) (*http.Response, error) {
				var properties *armappservice.SiteProperties
				if tt.siteConfig != nil {
					properties = &armappservice.SiteProperties{SiteConfig: tt.siteConfig}
				}
				return mocks.CreateHttpResponseWithBody(req, http.StatusOK, armappservice.Site{
					Kind:       tt.kind,
					Properties: properties,
				})
			})

			configuration, err := client.GetAppServiceContainerConfiguration(
				*mockCtx.Context,
				"SUB",
				"RG",
				"my-function",
			)

			require.NoError(t, err)
			assert.Equal(t, tt.expectLinux, configuration.IsLinux)
			assert.Equal(t, tt.expectContainer, configuration.IsContainer)
		})
	}
}

func Test_AzureClient_UpdateAppServiceAppSettings(t *testing.T) {
	t.Run("MergesWithExisting", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodPost &&
				strings.Contains(req.URL.Path, "/config/appsettings/list")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armappservice.StringDictionary{
					Properties: map[string]*string{
						"EXISTING_KEY": new("existing_value"),
					},
				})
		})

		var capturedBody string
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodPut &&
				strings.Contains(req.URL.Path, "/config/appsettings") &&
				!strings.Contains(req.URL.Path, "/list")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			capturedBody = string(bodyBytes)
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armappservice.StringDictionary{
					Properties: map[string]*string{
						"EXISTING_KEY": new("existing_value"),
						"NEW_KEY":      new("new_value"),
					},
				})
		})

		err := client.UpdateAppServiceAppSettings(
			*mockCtx.Context, "SUB", "RG", "my-app",
			map[string]string{"NEW_KEY": "new_value"})
		require.NoError(t, err)
		assert.Contains(t, capturedBody, "EXISTING_KEY", "should preserve existing settings")
		assert.Contains(t, capturedBody, "NEW_KEY", "should include new settings")
	})
}
