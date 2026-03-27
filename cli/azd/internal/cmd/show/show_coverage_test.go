// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package show

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewShowAction — constructor wiring
// ---------------------------------------------------------------------------

func TestNewShowAction_FieldWiring(t *testing.T) {
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	writer := &bytes.Buffer{}
	prjConfig := &project.ProjectConfig{Name: "test-project"}
	flags := &showFlags{}
	args := []string{"some-arg"}
	c := cloud.AzurePublic()

	envManager := &mockenv.MockEnvManager{}
	envManager.On("GetStateCacheManager").Return(nil)

	action := NewShowAction(
		console,
		formatter,
		writer,
		nil, // resourceService
		envManager,
		nil, // infraResourceManager
		prjConfig,
		nil, // importManager
		nil, // featureManager
		nil, // armClientOptions
		nil, // creds
		nil, // kvService
		nil, // azdCtx
		flags,
		args,
		nil, // lazyServiceManager
		nil, // lazyResourceManager
		c,
	)

	require.NotNil(t, action)
	sa, ok := action.(*showAction)
	require.True(t, ok, "expected *showAction, got %T", action)

	assert.Equal(t, prjConfig, sa.projectConfig)
	assert.Equal(t, console, sa.console)
	assert.Equal(t, formatter, sa.formatter)
	assert.Equal(t, writer, sa.writer)
	assert.Equal(t, flags, sa.flags)
	assert.Equal(t, args, sa.args)
	assert.Equal(t, c.PortalUrlBase, sa.portalUrlBase)
}

// ---------------------------------------------------------------------------
// showContainerApp — single container, no secrets
// ---------------------------------------------------------------------------

func mockContainerAppGetResponse(
	mockContext *mocks.MockContext,
	appName string,
	app *armappcontainers.ContainerApp,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
				testSubscriptionID,
				testResourceGroup,
				appName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappcontainers.ContainerAppsClientGetResponse{
			ContainerApp: *app,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func newContainerAppResourceID(appName string) *arm.ResourceID {
	id, err := arm.ParseResourceID(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
		testSubscriptionID,
		testResourceGroup,
		appName,
	))
	if err != nil {
		panic(fmt.Sprintf("failed to parse test resource ID: %v", err))
	}
	return id
}

func TestShowContainerApp_SingleContainer(t *testing.T) {
	appName := "my-app"
	fqdn := "my-app.azurecontainerapps.io"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					Fqdn: &fqdn,
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Name:  &appName,
						Image: strPtr("myregistry.azurecr.io/myimage:latest"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  strPtr("APP_ENV"),
								Value: strPtr("production"),
							},
							{
								Name:  strPtr("APP_PORT"),
								Value: strPtr("8080"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(t, appName, service.Name)
	assert.Equal(t, "Container App", service.DisplayType)
	assert.Equal(t, fmt.Sprintf("https://%s", fqdn), service.IngresUrl)
	assert.Equal(t, "production", service.Env["APP_ENV"])
	assert.Equal(t, "8080", service.Env["APP_PORT"])
}

func TestShowContainerApp_SecretRef_NoShowSecrets(t *testing.T) {
	appName := "secret-app"
	fqdn := "secret-app.azurecontainerapps.io"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					Fqdn: &fqdn,
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Name: &appName,
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:      strPtr("DB_PASSWORD"),
								SecretRef: strPtr("db-password-secret"),
							},
							{
								Name:  strPtr("APP_ENV"),
								Value: strPtr("prod"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	// Secret ref without showSecrets should be masked
	assert.Equal(t, "*******", service.Env["DB_PASSWORD"])
	assert.Equal(t, "prod", service.Env["APP_ENV"])
}

func TestShowContainerApp_EmptyContainers(t *testing.T) {
	appName := "empty-app"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(t, appName, service.Name)
	assert.Empty(t, service.Env)
}

func TestShowContainerApp_MultiContainer_MatchByName(t *testing.T) {
	appName := "multi-app"
	fqdn := "multi-app.azurecontainerapps.io"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					Fqdn: &fqdn,
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Name: strPtr("sidecar"),
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  strPtr("SIDECAR_VAR"),
								Value: strPtr("side"),
							},
						},
					},
					{
						Name: &appName,
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name:  strPtr("MAIN_VAR"),
								Value: strPtr("main"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(t, "main", service.Env["MAIN_VAR"])
	assert.NotContains(t, service.Env, "SIDECAR_VAR")
}

func TestShowContainerApp_MultiContainer_NoMatch(t *testing.T) {
	appName := "no-match-app"
	fqdn := "no-match-app.azurecontainerapps.io"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					Fqdn: &fqdn,
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Name: strPtr("worker-a"),
					},
					{
						Name: strPtr("worker-b"),
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.Error(t, err)
	require.Nil(t, service)
	assert.Contains(t, err.Error(), "has more than one container")
}

func TestShowContainerApp_NilEnvName(t *testing.T) {
	appName := "nil-env-app"
	fqdn := "nil-env-app.azurecontainerapps.io"
	app := &armappcontainers.ContainerApp{
		Name: &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					Fqdn: &fqdn,
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{
						Name: &appName,
						Env: []*armappcontainers.EnvironmentVar{
							{
								Name: nil, // nil env name should be skipped
							},
							{
								Name:  strPtr("GOOD_VAR"),
								Value: strPtr("good"),
							},
						},
					},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockContainerAppGetResponse(mockContext, appName, app)

	id := newContainerAppResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showContainerApp(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(t, "good", service.Env["GOOD_VAR"])
	assert.Len(t, service.Env, 1)
}

// ---------------------------------------------------------------------------
// showAppService
// ---------------------------------------------------------------------------

func mockAppServiceGetResponse(
	mockContext *mocks.MockContext,
	appName string,
	site *armappservice.Site,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
				testSubscriptionID,
				testResourceGroup,
				appName,
			),
		) && !strings.Contains(request.URL.Path, "/config/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappservice.WebAppsClientGetResponse{
			Site: *site,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func mockAppServiceListSettings(
	mockContext *mocks.MockContext,
	appName string,
	settings map[string]*string,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(
			request.URL.Path,
			fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s/config/appsettings/list",
				testSubscriptionID,
				testResourceGroup,
				appName,
			),
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappservice.WebAppsClientListApplicationSettingsResponse{
			StringDictionary: armappservice.StringDictionary{
				Properties: settings,
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func newAppServiceResourceID(appName string) *arm.ResourceID {
	id, err := arm.ParseResourceID(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites/%s",
		testSubscriptionID,
		testResourceGroup,
		appName,
	))
	if err != nil {
		panic(fmt.Sprintf("failed to parse test resource ID: %v", err))
	}
	return id
}

func TestShowAppService_BasicSettings(t *testing.T) {
	appName := "my-webapp"
	hostname := "my-webapp.azurewebsites.net"
	site := &armappservice.Site{
		Name: &appName,
		Properties: &armappservice.SiteProperties{
			DefaultHostName: &hostname,
		},
	}

	settings := map[string]*string{
		"APP_ENV": strPtr("production"),
		"PORT":    strPtr("3000"),
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(t, appName, service.Name)
	assert.Equal(t, "App Service", service.DisplayType)
	assert.Equal(t, fmt.Sprintf("https://%s", hostname), service.IngresUrl)
	assert.Equal(t, "production", service.Env["APP_ENV"])
	assert.Equal(t, "3000", service.Env["PORT"])
}

func TestShowAppService_SecretsAreMasked(t *testing.T) {
	appName := "secret-webapp"
	site := &armappservice.Site{
		Name:       &appName,
		Properties: &armappservice.SiteProperties{},
	}

	settings := map[string]*string{
		"POSTGRES_PASSWORD":     strPtr("supersecret"),
		"REDIS_URL":             strPtr("redis://host:6379"),
		"NORMAL_SETTING":        strPtr("visible"),
		"KEY_VAULT_REF_SETTING": strPtr("@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)"),
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts:  mockContext.ArmClientOptions,
		showSecrets: false,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)

	// Known secret keys should be masked
	assert.Equal(t, "*******", service.Env["POSTGRES_PASSWORD"])
	assert.Equal(t, "*******", service.Env["REDIS_URL"])
	// Key Vault reference should also be masked
	assert.Equal(t, "*******", service.Env["KEY_VAULT_REF_SETTING"])
	// Normal setting should be visible
	assert.Equal(t, "visible", service.Env["NORMAL_SETTING"])
}

func TestShowAppService_ShowSecretsRevealsValues(t *testing.T) {
	appName := "reveal-webapp"
	site := &armappservice.Site{
		Name:       &appName,
		Properties: &armappservice.SiteProperties{},
	}

	settings := map[string]*string{
		"POSTGRES_PASSWORD": strPtr("supersecret"),
		"NORMAL_SETTING":    strPtr("visible"),
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts:  mockContext.ArmClientOptions,
		showSecrets: true,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)

	// When showSecrets is true, actual values should be shown
	assert.Equal(t, "supersecret", service.Env["POSTGRES_PASSWORD"])
	assert.Equal(t, "visible", service.Env["NORMAL_SETTING"])
}

func TestShowAppService_NilSettingValue(t *testing.T) {
	appName := "nil-val-webapp"
	site := &armappservice.Site{
		Name:       &appName,
		Properties: &armappservice.SiteProperties{},
	}

	settings := map[string]*string{
		"GOOD_KEY":    strPtr("value"),
		"NIL_VAL_KEY": nil,
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)

	assert.Equal(t, "value", service.Env["GOOD_KEY"])
	// nil value should be skipped
	_, exists := service.Env["NIL_VAL_KEY"]
	assert.False(t, exists)
}

func TestShowAppService_NoHostName(t *testing.T) {
	appName := "no-hostname"
	site := &armappservice.Site{
		Name:       &appName,
		Properties: &armappservice.SiteProperties{},
	}

	settings := map[string]*string{}

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts: mockContext.ArmClientOptions,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Empty(t, service.IngresUrl)
}

func TestShowAppService_AllKnownSecretKeys(t *testing.T) {
	appName := "all-secrets-webapp"
	site := &armappservice.Site{
		Name:       &appName,
		Properties: &armappservice.SiteProperties{},
	}

	// All known secret keys as defined in showAppService
	knownSecretKeys := []string{
		"MONGODB_URL",
		"POSTGRES_PASSWORD",
		"POSTGRES_URL",
		"MYSQL_PASSWORD",
		"MYSQL_URL",
		"REDIS_PASSWORD",
		"REDIS_URL",
	}

	settings := make(map[string]*string)
	for _, key := range knownSecretKeys {
		settings[key] = strPtr("secret-value-for-" + key)
	}
	settings["SAFE_KEY"] = strPtr("not-a-secret")

	mockContext := mocks.NewMockContext(context.Background())
	mockAppServiceGetResponse(mockContext, appName, site)
	mockAppServiceListSettings(mockContext, appName, settings)

	id := newAppServiceResourceID(appName)
	opts := showResourceOptions{
		clientOpts:  mockContext.ArmClientOptions,
		showSecrets: false,
	}

	service, err := showAppService(
		*mockContext.Context, mockContext.Credentials, id, opts,
	)
	require.NoError(t, err)
	require.NotNil(t, service)

	for _, key := range knownSecretKeys {
		assert.Equal(t, "*******", service.Env[key], "expected key %s to be masked", key)
	}
	assert.Equal(t, "not-a-secret", service.Env["SAFE_KEY"])
}

// ---------------------------------------------------------------------------
// serviceEndpoint — error paths (lazy manager failures)
// ---------------------------------------------------------------------------

func TestServiceEndpoint_LazyResourceManagerError(t *testing.T) {
	lazyRM := lazy.NewLazy(func() (project.ResourceManager, error) {
		return nil, fmt.Errorf("resource manager init failed")
	})
	lazyRM.GetValue() //nolint:errcheck // prime the error

	s := &showAction{
		lazyResourceManager: lazyRM,
	}

	result := s.serviceEndpoint(context.Background(), "sub123", &project.ServiceConfig{}, nil)
	assert.Empty(t, result)
}

func TestServiceEndpoint_LazyServiceManagerError(t *testing.T) {
	lazyRM := lazy.NewLazy(func() (project.ResourceManager, error) {
		return nil, nil // succeeds with nil (won't be reached)
	})
	lazySM := lazy.NewLazy(func() (project.ServiceManager, error) {
		return nil, fmt.Errorf("service manager init failed")
	})

	s := &showAction{
		lazyResourceManager: lazyRM,
		lazyServiceManager:  lazySM,
	}

	result := s.serviceEndpoint(context.Background(), "sub123", &project.ServiceConfig{}, nil)
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }
