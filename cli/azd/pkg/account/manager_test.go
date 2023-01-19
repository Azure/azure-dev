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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func Test_GetAccountDefaults(t *testing.T) {
	defaultSubscription := Subscription{
		Id:       "SUBSCRIPTION_01",
		Name:     "Subscription 1",
		TenantId: "TENANT_ID",
	}

	t.Run("FromAzdConfig", func(t *testing.T) {
		expectedConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_01",
				"location":     "westus",
			},
		})

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &defaultSubscription, nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(expectedConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(*mockContext.Context)
		require.NoError(t, err)
		require.Equal(t, "SUBSCRIPTION_01", accountDefaults.DefaultSubscription.Id)
		require.Equal(t, "westus", accountDefaults.DefaultLocation.Name)
	})

	t.Run("FromCodeDefaults", func(t *testing.T) {
		emptyConfig := config.NewConfig(nil)

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(emptyConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(*mockContext.Context)
		require.NoError(t, err)
		require.Nil(t, accountDefaults.DefaultSubscription)
		require.Equal(t, "eastus2", accountDefaults.DefaultLocation.Name)
	})

	t.Run("InvalidSubscription", func(t *testing.T) {
		invalidSubscription := defaultSubscription
		invalidSubscription.Id = "INVALID"

		emptyConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "INVALID",
				"location":     "westus",
			},
		})

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &invalidSubscription, errors.New("subscription not found"))

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(emptyConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(*mockContext.Context)
		require.Nil(t, accountDefaults)
		require.Error(t, err)
	})

	t.Run("InvalidLocation", func(t *testing.T) {
		emptyConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_01",
				"location":     "INVALID",
			},
		})

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &defaultSubscription, nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(emptyConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(*mockContext.Context)
		require.Nil(t, accountDefaults)
		require.Error(t, err)
	})
}

func Test_GetSubscriptions(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

		require.NoError(t, err)
		require.Len(t, subscriptions, 3)
	})

	t.Run("SuccessWithDefault", func(t *testing.T) {
		subscription := Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		defaultConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_03",
				"location":     "westus2",
			},
		})

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &subscription, nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(defaultConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

		defaultIndex := slices.IndexFunc(subscriptions, func(sub *azcli.AzCliSubscriptionInfo) bool {
			return sub.IsDefault
		})

		require.NoError(t, err)
		require.Len(t, subscriptions, 3)
		require.GreaterOrEqual(t, defaultIndex, 0)
		require.Equal(t, "SUBSCRIPTION_03", subscriptions[defaultIndex].Id)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountErrorMocks(mockContext)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, subscriptions)
	})
}

func Test_GetLocations(t *testing.T) {
	subscription := Subscription{
		Id:       "SUBSCRIPTION_03",
		Name:     "Subscription 3",
		TenantId: "TENANT_ID",
	}

	defaultConfig := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": "SUBSCRIPTION_03",
			"location":     "westus2",
		},
	})

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &subscription, nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(defaultConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		locations, err := manager.GetLocations(*mockContext.Context, subscription.Id)

		require.NoError(t, err)
		require.Len(t, locations, 4)
	})

	t.Run("ErrorNoDefaultSubscription", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountErrorMocks(mockContext)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		locations, err := manager.GetLocations(*mockContext.Context, subscription.Id)

		require.Error(t, err)
		require.Nil(t, locations)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountErrorMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &subscription, nil)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		locations, err := manager.GetLocations(*mockContext.Context, subscription.Id)

		require.Error(t, err)
		require.Nil(t, locations)
	})
}

func Test_SetDefaultSubscription(t *testing.T) {
	t.Run("ValidSubscription", func(t *testing.T) {
		expectedSubscription := Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &expectedSubscription, nil)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		actualSubscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)

		require.NoError(t, err)
		require.Equal(t, expectedSubscription, *actualSubscription)
	})

	t.Run("InvalidSubscription", func(t *testing.T) {
		expectedSubscription := Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &expectedSubscription, errors.New("Not found"))

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		actualSubscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)

		require.Error(t, err)
		require.Nil(t, actualSubscription)
	})
}

func Test_SetDefaultLocation(t *testing.T) {
	defaultConfig := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": "SUBSCRIPTION_03",
			"location":     "westus2",
		},
	})

	subscription := Subscription{
		Id:       "SUBSCRIPTION_03",
		Name:     "Subscription 3",
		TenantId: "TENANT_ID",
	}

	t.Run("ValidLocation", func(t *testing.T) {
		expectedLocation := "westus2"

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &subscription, nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(defaultConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		location, err := manager.SetDefaultLocation(*mockContext.Context, subscription.Id, expectedLocation)

		require.NoError(t, err)
		require.Equal(t, expectedLocation, location.Name)
	})

	t.Run("InvalidLocation", func(t *testing.T) {
		expectedLocation := "invalid"

		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)
		setupGetSubscriptionMock(mockContext, &subscription, nil)

		manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
		require.NoError(t, err)

		location, err := manager.SetDefaultLocation(*mockContext.Context, subscription.Id, expectedLocation)

		require.Error(t, err)
		require.Nil(t, location)
	})
}

func Test_Clear(t *testing.T) {
	expectedSubscription := Subscription{
		Id:       "SUBSCRIPTION_03",
		Name:     "Subscription 3",
		TenantId: "TENANT_ID",
	}

	mockContext := mocks.NewMockContext(context.Background())
	setupAccountMocks(mockContext)
	setupGetSubscriptionMock(mockContext, &expectedSubscription, nil)

	manager, err := NewManager(mockContext.ConfigManager, mockazcli.NewAzCliFromMockContext(mockContext))
	require.NoError(t, err)

	subscription, err := manager.SetDefaultSubscription(*mockContext.Context, expectedSubscription.Id)
	require.NoError(t, err)

	location, err := manager.SetDefaultLocation(*mockContext.Context, subscription.Id, "westus2")
	require.NoError(t, err)

	updatedConfig, err := mockContext.ConfigManager.Load("PATH")
	require.NoError(t, err)

	configSubscription, _ := updatedConfig.Get(defaultSubscriptionKeyPath)
	configLocation, _ := updatedConfig.Get(defaultLocationKeyPath)

	require.Equal(t, subscription.Id, configSubscription)
	require.Equal(t, location.Name, configLocation)

	err = manager.Clear(*mockContext.Context)
	require.NoError(t, err)

	clearedConfig, err := mockContext.ConfigManager.Load("PATH")
	require.NotNil(t, clearedConfig)
	require.NoError(t, err)

	configSubscription, _ = clearedConfig.Get(defaultSubscriptionKeyPath)
	configLocation, _ = clearedConfig.Get(defaultLocationKeyPath)

	require.Nil(t, configSubscription)
	require.Nil(t, configLocation)
}

func Test_HasDefaults(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	t.Run("DefaultsSet", func(t *testing.T) {
		azdConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_ID",
				"location":     "LOCATION",
			},
		})

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(azdConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		value := manager.HasDefaults()
		require.True(t, value)
	})

	t.Run("DefaultsNotSet", func(t *testing.T) {
		azdConfig := config.NewConfig(nil)

		manager, err := NewManager(
			mockContext.ConfigManager.WithConfig(azdConfig),
			mockazcli.NewAzCliFromMockContext(mockContext),
		)
		require.NoError(t, err)

		value := manager.HasDefaults()
		require.False(t, value)
	})
}

var allTestSubscriptions []*armsubscriptions.Subscription = []*armsubscriptions.Subscription{
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
}

func setupGetSubscriptionMock(mockContext *mocks.MockContext, subscription *Subscription, err error) {
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
				Value: allTestSubscriptions,
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
						Metadata: &armsubscriptions.LocationMetadata{
							RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical),
						},
					},
					{
						ID:                  convert.RefOf("westus2"),
						Name:                convert.RefOf("westus2"),
						DisplayName:         convert.RefOf("West US 2"),
						RegionalDisplayName: convert.RefOf("(US) West US 2"),
						Metadata: &armsubscriptions.LocationMetadata{
							RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical),
						},
					},
					{
						ID:                  convert.RefOf("eastus"),
						Name:                convert.RefOf("eastus"),
						DisplayName:         convert.RefOf("East US"),
						RegionalDisplayName: convert.RefOf("(US) East US"),
						Metadata: &armsubscriptions.LocationMetadata{
							RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical),
						},
					},
					{
						ID:                  convert.RefOf("eastus2"),
						Name:                convert.RefOf("eastus2"),
						DisplayName:         convert.RefOf("East US 2"),
						RegionalDisplayName: convert.RefOf("(US) East US 2"),
						Metadata: &armsubscriptions.LocationMetadata{
							RegionType: convert.RefOf(armsubscriptions.RegionTypePhysical),
						},
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
}
