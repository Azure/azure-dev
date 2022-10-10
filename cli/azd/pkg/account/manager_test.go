package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func Test_GetAccountDefaults(t *testing.T) {
	t.Run("FromAzdConfig", func(t *testing.T) {
		expectedConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:       "SUBSCRIPTION_01",
				Name:     "Subscription 1",
				TenantId: "TENANT_ID",
			},
			DefaultLocation: &config.Location{
				Name:        "westus",
				DisplayName: "(US) West US",
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		ctx := config.WithConfig(*mockContext.Context, &expectedConfig)

		manager := NewManager(ctx)
		actualConfig, err := manager.GetAccountDefaults(ctx)

		require.NoError(t, err)
		require.Equal(t, expectedConfig, *actualConfig)
	})

	t.Run("FromAzConfig", func(t *testing.T) {
		emptyConfig := config.Config{}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		ctx := config.WithConfig(*mockContext.Context, &emptyConfig)

		manager := NewManager(ctx)
		actualConfig, err := manager.GetAccountDefaults(ctx)

		expectedConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:       "SUBSCRIPTION_02",
				Name:     "Subscription 2",
				TenantId: "TENANT_ID",
			},
			DefaultLocation: &config.Location{
				Name:        "westus2",
				DisplayName: "(US) West US 2",
			},
		}

		require.NoError(t, err)
		require.Equal(t, expectedConfig, *actualConfig)
	})

	t.Run("FromCodeDefaults", func(t *testing.T) {
		emptyConfig := config.Config{}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupEmptyAzCliMocks(mockContext)

		ctx := config.WithConfig(*mockContext.Context, &emptyConfig)

		manager := NewManager(ctx)
		actualConfig, err := manager.GetAccountDefaults(ctx)

		expectedConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:       "SUBSCRIPTION_02",
				Name:     "Subscription 2",
				TenantId: "TENANT_ID",
			},
			// Location should default to east us 2 when not found in either azd or az configs.
			DefaultLocation: &config.Location{
				Name:        "eastus2",
				DisplayName: "(US) East US 2",
			},
		}

		require.NoError(t, err)
		require.Equal(t, expectedConfig, *actualConfig)
	})

	t.Run("NotLoggedIn", func(t *testing.T) {
		emptyConfig := config.Config{}

		mockContext := mocks.NewMockContext(context.Background())
		ctx := config.WithConfig(*mockContext.Context, &emptyConfig)

		setupAzNotLoggedInMocks(mockContext)

		manager := NewManager(ctx)
		actualConfig, err := manager.GetAccountDefaults(ctx)

		require.Error(t, err)
		require.Nil(t, actualConfig)
	})
}

func Test_GetSubscriptions(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

		require.NoError(t, err)
		require.Len(t, subscriptions, 3)
	})

	t.Run("SuccessWithDefault", func(t *testing.T) {
		defaultConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:       "SUBSCRIPTION_03",
				Name:     "Subscription 3",
				TenantId: "TENANT_ID",
			},
			DefaultLocation: &config.Location{
				Name:        "westus2",
				DisplayName: "(US) West US 2",
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		ctx := config.WithConfig(*mockContext.Context, &defaultConfig)

		manager := NewManager(ctx)
		subscriptions, err := manager.GetSubscriptions(ctx)

		defaultIndex := slices.IndexFunc(subscriptions, func(s azcli.AzCliSubscriptionInfo) bool {
			return s.IsDefault
		})

		require.NoError(t, err)
		require.Len(t, subscriptions, 3)
		require.GreaterOrEqual(t, defaultIndex, 0)
		require.Equal(t, defaultConfig.DefaultSubscription.Id, subscriptions[defaultIndex].Id)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountErrorMocks(mockContext)
		setupAzNotLoggedInMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, subscriptions)
	})
}

func Test_GetLocations(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountErrorMocks(mockContext)
		setupAzNotLoggedInMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		locations, err := manager.GetLocations(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, locations)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		locations, err := manager.GetLocations(*mockContext.Context)

		require.NoError(t, err)
		require.Len(t, locations, 4)
	})
}

func Test_SetDefaultSubscription(t *testing.T) {
	t.Run("ValidSubscription", func(t *testing.T) {
		expectedSubscription := config.Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &expectedSubscription, nil)

		manager := NewManager(*mockContext.Context)
		actualSubscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)

		require.NoError(t, err)
		require.Equal(t, expectedSubscription, *actualSubscription)
	})

	t.Run("InvalidSubscription", func(t *testing.T) {
		expectedSubscription := config.Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &expectedSubscription, errors.New("Not found"))

		manager := NewManager(*mockContext.Context)
		actualSubscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)

		require.Error(t, err)
		require.Nil(t, actualSubscription)
	})
}

func Test_SetDefaultLocation(t *testing.T) {
	t.Run("ValidLocation", func(t *testing.T) {
		expectedLocation := "westus2"

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		location, err := manager.SetDefaultLocation(*mockContext.Context, expectedLocation)

		require.NoError(t, err)
		require.Equal(t, expectedLocation, location.Name)
	})

	t.Run("InvalidLocation", func(t *testing.T) {
		expectedLocation := "invalid"

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		location, err := manager.SetDefaultLocation(*mockContext.Context, expectedLocation)

		require.Error(t, err)
		require.Nil(t, location)
	})
}

func Test_Clear(t *testing.T) {
	expectedSubscription := config.Subscription{
		Id:       "SUBSCRIPTION_03",
		Name:     "Subscription 3",
		TenantId: "TENANT_ID",
	}

	mockContext := mocks.NewMockContext(context.Background())
	setupAccountMocks(mockContext)
	setupGetSubscriptionMock(mockContext, &expectedSubscription, nil)

	manager := NewManager(*mockContext.Context)
	subscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)
	require.NoError(t, err)

	location, err := manager.SetDefaultLocation(*mockContext.Context, "westus2")
	require.NoError(t, err)

	updatedConfig, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, subscription, updatedConfig.DefaultSubscription)
	require.Equal(t, location, updatedConfig.DefaultLocation)

	err = manager.Clear(*mockContext.Context)
	require.NoError(t, err)

	clearedConfig, err := config.Load()
	require.NotNil(t, clearedConfig)
	require.Nil(t, clearedConfig.DefaultSubscription)
	require.Nil(t, clearedConfig.DefaultLocation)
	require.NoError(t, err)
}

func setupAzNotLoggedInMocks(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az account show")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "Please run 'az login' to setup account"), errors.New("error")
	})
}

func setupEmptyAzCliMocks(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az config get defaults.location")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "No config"), errors.New("No config found")
	})
}

func setupGetSubscriptionMock(mockContext *mocks.MockContext, subscription *config.Subscription, err error) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == fmt.Sprintf("/subscriptions/%s", subscription.Id)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if err != nil {
			return &http.Response{
				Request:    request,
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Body:       http.NoBody,
			}, nil
		}

		res := armsubscriptions.Subscription{
			ID:             convert.RefOf(subscription.Id),
			SubscriptionID: convert.RefOf(subscription.Id),
			DisplayName:    convert.RefOf(subscription.Name),
			TenantID:       convert.RefOf(subscription.TenantId),
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})
}

func setupAccountErrorMocks(mockContext *mocks.MockContext) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/subscriptions"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{},
			Body:       http.NoBody,
		}, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/locations")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{},
			Body:       http.NoBody,
		}, nil
	})
}

func setupAccountMocks(mockContext *mocks.MockContext) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/subscriptions"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.ClientListResponse{
			SubscriptionListResult: armsubscriptions.SubscriptionListResult{
				Value: []*armsubscriptions.Subscription{
					{
						ID:             convert.RefOf("subscriptions/SUBSCRIPTION_01"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_01"),
						DisplayName:    convert.RefOf("Subscription 1"),
						TenantID:       convert.RefOf("TENANT_ID"),
					},
					{
						ID:             convert.RefOf("subscriptions/SUBSCRIPTION_02"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_02"),
						DisplayName:    convert.RefOf("Subscription 2"),
						TenantID:       convert.RefOf("TENANT_ID"),
					},
					{
						ID:             convert.RefOf("subscriptions/SUBSCRIPTION_03"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_03"),
						DisplayName:    convert.RefOf("Subscription 3"),
						TenantID:       convert.RefOf("TENANT_ID"),
					},
				},
			},
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/locations")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.ClientListLocationsResponse{
			LocationListResult: armsubscriptions.LocationListResult{
				Value: []*armsubscriptions.Location{
					{
						ID:                  convert.RefOf("westus"),
						Name:                convert.RefOf("westus"),
						DisplayName:         convert.RefOf("West US"),
						RegionalDisplayName: convert.RefOf("(US) West US"),
						Metadata:            &armsubscriptions.LocationMetadata{RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical)},
					},
					{
						ID:                  convert.RefOf("westus2"),
						Name:                convert.RefOf("westus2"),
						DisplayName:         convert.RefOf("West US 2"),
						RegionalDisplayName: convert.RefOf("(US) West US 2"),
						Metadata:            &armsubscriptions.LocationMetadata{RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical)},
					},
					{
						ID:                  convert.RefOf("eastus"),
						Name:                convert.RefOf("eastus"),
						DisplayName:         convert.RefOf("East US"),
						RegionalDisplayName: convert.RefOf("(US) East US"),
						Metadata:            &armsubscriptions.LocationMetadata{RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical)},
					},
					{
						ID:                  convert.RefOf("eastus2"),
						Name:                convert.RefOf("eastus2"),
						DisplayName:         convert.RefOf("East US 2"),
						RegionalDisplayName: convert.RefOf("(US) East US 2"),
						Metadata:            &armsubscriptions.LocationMetadata{RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical)},
					},
				},
			},
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az account show")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		showRes := map[string]any{
			"id":        "SUBSCRIPTION_02",
			"name":      "Subscription 2",
			"tenantId":  "TENANT_ID",
			"isDefault": false,
		}

		jsonBytes, _ := json.Marshal(showRes)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az config get defaults.location")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		configRes := map[string]any{
			"name":   "location",
			"source": ".azure/config",
			"value":  "westus2",
		}

		jsonBytes, _ := json.Marshal(configRes)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})
}
