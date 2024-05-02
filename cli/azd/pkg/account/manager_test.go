package account

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockconfig"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func armClientOptions(httpTransport *mockhttp.MockHttpClient) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: httpTransport},
	}
}

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

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &defaultSubscription, nil)

		manager, err := NewManager(
			mockConfig.WithConfig(expectedConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(context.Background())
		require.NoError(t, err)
		require.Equal(t, "SUBSCRIPTION_01", accountDefaults.DefaultSubscription.Id)
		require.Equal(t, "westus", accountDefaults.DefaultLocation.Name)
	})

	t.Run("FromCodeDefaults", func(t *testing.T) {
		emptyConfig := config.NewEmptyConfig()

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)

		manager, err := NewManager(
			mockConfig.WithConfig(emptyConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(context.Background())
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

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &invalidSubscription, errors.New("subscription not found"))

		manager, err := NewManager(
			mockConfig.WithConfig(emptyConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(context.Background())
		require.Equal(
			t,
			&Account{DefaultSubscription: (*Subscription)(nil), DefaultLocation: (&defaultLocation)},
			accountDefaults,
		)
		require.NoError(t, err)
	})

	t.Run("InvalidLocation", func(t *testing.T) {
		emptyConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_01",
				"location":     "INVALID",
			},
		})

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &defaultSubscription, nil)

		manager, err := NewManager(
			mockConfig.WithConfig(emptyConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		accountDefaults, err := manager.GetAccountDefaults(context.Background())
		require.Nil(t, accountDefaults)
		require.Error(t, err)
	})
}

func Test_GetSubscriptionsWithDefaultSet(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptionsWithDefaultSet(context.Background())

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

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &subscription, nil)

		manager, err := NewManager(
			mockConfig.WithConfig(defaultConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptionsWithDefaultSet(context.Background())

		defaultIndex := slices.IndexFunc(subscriptions, func(sub Subscription) bool {
			return sub.IsDefault
		})

		require.NoError(t, err)
		require.Len(t, subscriptions, 3)
		require.GreaterOrEqual(t, defaultIndex, 0)
		require.Equal(t, "SUBSCRIPTION_03", subscriptions[defaultIndex].Id)
	})

	t.Run("Error", func(t *testing.T) {
		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountErrorMocks(mockHttp)

		manager, err := NewManager(
			mockConfig,
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			))
		require.NoError(t, err)

		subscriptions, err := manager.GetSubscriptionsWithDefaultSet(context.Background())

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
		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &subscription, nil)

		manager, err := NewManager(
			mockConfig.WithConfig(defaultConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		locations, err := manager.GetLocations(context.Background(), subscription.Id)

		require.NoError(t, err)
		require.Len(t, locations, 4)
	})

	t.Run("ErrorNoDefaultSubscription", func(t *testing.T) {
		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountErrorMocks(mockHttp)

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		locations, err := manager.GetLocations(context.Background(), subscription.Id)

		require.Error(t, err)
		require.Nil(t, locations)
	})

	t.Run("Error", func(t *testing.T) {
		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountErrorMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &subscription, nil)

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		locations, err := manager.GetLocations(context.Background(), subscription.Id)

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

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &expectedSubscription, nil)

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		actualSubscription, err := manager.SetDefaultSubscription(context.Background(), expectedSubscription.Id)

		require.NoError(t, err)
		require.Equal(t, expectedSubscription, *actualSubscription)
	})

	t.Run("InvalidSubscription", func(t *testing.T) {
		expectedSubscription := Subscription{
			Id:       "SUBSCRIPTION_03",
			Name:     "Subscription 3",
			TenantId: "TENANT_ID",
		}

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &expectedSubscription, errors.New("Not found"))

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		actualSubscription, err := manager.SetDefaultSubscription(context.Background(), "invalid")

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

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &subscription, nil)

		manager, err := NewManager(
			mockConfig.WithConfig(defaultConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		location, err := manager.SetDefaultLocation(context.Background(), subscription.Id, expectedLocation)

		require.NoError(t, err)
		require.Equal(t, expectedLocation, location.Name)
	})

	t.Run("InvalidLocation", func(t *testing.T) {
		expectedLocation := "invalid"

		mockConfig := mockconfig.NewMockConfigManager()
		mockHttp := mockhttp.NewMockHttpUtil()
		setupAccountMocks(mockHttp)
		setupGetSubscriptionMock(mockHttp, &subscription, nil)

		manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
			NewSubscriptionsService(
				&mocks.MockMultiTenantCredentialProvider{},
				armClientOptions(mockHttp),
			),
			NewBypassSubscriptionsCache()))
		require.NoError(t, err)

		location, err := manager.SetDefaultLocation(context.Background(), subscription.Id, expectedLocation)

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

	mockConfig := mockconfig.NewMockConfigManager()
	mockHttp := mockhttp.NewMockHttpUtil()
	setupAccountMocks(mockHttp)
	setupGetSubscriptionMock(mockHttp, &expectedSubscription, nil)

	manager, err := NewManager(mockConfig, NewSubscriptionsManagerWithCache(
		NewSubscriptionsService(
			&mocks.MockMultiTenantCredentialProvider{},
			armClientOptions(mockHttp),
		),
		NewBypassSubscriptionsCache()))
	require.NoError(t, err)

	subscription, err := manager.SetDefaultSubscription(context.Background(), expectedSubscription.Id)
	require.NoError(t, err)

	location, err := manager.SetDefaultLocation(context.Background(), subscription.Id, "westus2")
	require.NoError(t, err)

	updatedConfig, err := mockConfig.Load("PATH")
	require.NoError(t, err)

	configSubscription, _ := updatedConfig.Get(defaultSubscriptionKeyPath)
	configLocation, _ := updatedConfig.Get(defaultLocationKeyPath)

	require.Equal(t, subscription.Id, configSubscription)
	require.Equal(t, location.Name, configLocation)

	err = manager.Clear(context.Background())
	require.NoError(t, err)

	clearedConfig, err := mockConfig.Load("PATH")
	require.NotNil(t, clearedConfig)
	require.NoError(t, err)

	configSubscription, _ = clearedConfig.Get(defaultSubscriptionKeyPath)
	configLocation, _ = clearedConfig.Get(defaultLocationKeyPath)

	require.Nil(t, configSubscription)
	require.Nil(t, configLocation)
}

func Test_HasDefaults(t *testing.T) {
	mockConfig := mockconfig.NewMockConfigManager()
	mockHttp := mockhttp.NewMockHttpUtil()

	t.Run("DefaultsSet", func(t *testing.T) {
		azdConfig := config.NewConfig(map[string]any{
			"defaults": map[string]any{
				"subscription": "SUBSCRIPTION_ID",
				"location":     "LOCATION",
			},
		})

		manager, err := NewManager(
			mockConfig.WithConfig(azdConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		require.True(t, manager.HasDefaultSubscription())
		require.True(t, manager.HasDefaultLocation())
	})

	t.Run("DefaultsNotSet", func(t *testing.T) {
		azdConfig := config.NewEmptyConfig()

		manager, err := NewManager(
			mockConfig.WithConfig(azdConfig),
			NewSubscriptionsManagerWithCache(
				NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				NewBypassSubscriptionsCache(),
			),
		)
		require.NoError(t, err)

		require.False(t, manager.HasDefaultSubscription())
		require.False(t, manager.HasDefaultLocation())
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

func setupGetSubscriptionMock(mockHttp *mockhttp.MockHttpClient, subscription *Subscription, err error) {
	if err != nil {
		isSub := func(request *http.Request) bool {
			return mockarmresources.IsGetSubscription(request, subscription.Id)
		}
		mockHttp.When(isSub).SetNonRetriableError(err)
		return
	}

	mockarmresources.MockGetSubscription(mockHttp, subscription.Id, armsubscriptions.Subscription{
		ID:             convert.RefOf(subscription.Id),
		SubscriptionID: convert.RefOf(subscription.Id),
		DisplayName:    convert.RefOf(subscription.Name),
		TenantID:       convert.RefOf(subscription.TenantId),
	})
}

func setupAccountErrorMocks(mockHttp *mockhttp.MockHttpClient) {
	mockHttp.When(mockarmresources.IsListSubscriptions).
		RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				Request:    request,
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Body:       http.NoBody,
			}, nil
		})

	mockHttp.When(mockarmresources.IsListTenants).
		RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				Request:    request,
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Body:       http.NoBody,
			}, nil
		})

	mockHttp.When(func(request *http.Request) bool {
		return mockarmresources.IsListLocations(request, "")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    request,
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{},
			Body:       http.NoBody,
		}, nil
	})
}

func setupAccountMocks(mockHttp *mockhttp.MockHttpClient) {
	mockarmresources.MockListSubscriptions(mockHttp, armsubscriptions.SubscriptionListResult{
		Value: allTestSubscriptions,
	})

	mockarmresources.MockListTenants(mockHttp, armsubscriptions.TenantListResult{
		Value: []*armsubscriptions.TenantIDDescription{
			{
				DisplayName: convert.RefOf("TENANT"),
				TenantID:    convert.RefOf("TENANT_ID"),
			},
		},
	})

	for _, sub := range allTestSubscriptions {
		mockarmresources.MockListLocations(mockHttp, *sub.SubscriptionID,
			armsubscriptions.LocationListResult{
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
			})
	}
}

type InMemorySubCache struct {
	stored []Subscription
}

func (imc *InMemorySubCache) Load(key string) ([]Subscription, error) {
	return imc.stored, nil
}

func (imc *InMemorySubCache) Save(key string, save []Subscription) error {
	imc.stored = save
	return nil
}

func (imc *InMemorySubCache) Clear() error {
	imc.stored = nil
	return nil
}

func NewInMemorySubscriptionsCache() *InMemorySubCache {
	return &InMemorySubCache{
		stored: []Subscription{},
	}
}

func NewSubscriptionsManagerWithCache(
	service *SubscriptionsService,
	cache subCache) *SubscriptionsManager {
	return &SubscriptionsManager{
		service:       service,
		cache:         cache,
		principalInfo: &principalInfoProviderMock{},
		console:       mockinput.NewMockConsole(),
	}
}

type principalInfoProviderMock struct {
	GetLoggedInServicePrincipalTenantIDFunc func(context.Context) (*string, error)
}

func (p *principalInfoProviderMock) GetLoggedInServicePrincipalTenantID(ctx context.Context) (*string, error) {
	if p.GetLoggedInServicePrincipalTenantIDFunc != nil {
		return p.GetLoggedInServicePrincipalTenantIDFunc(ctx)
	}

	return nil, nil
}

func (p *principalInfoProviderMock) ClaimsForCurrentUser(
	ctx context.Context, options *auth.ClaimsForCurrentUserOptions) (auth.TokenClaims, error) {
	return auth.TokenClaims{
		UniqueName: "test_user",
		Oid:        "test_oid",
	}, nil
}

type BypassSubscriptionsCache struct {
}

func (b *BypassSubscriptionsCache) Load(ctx context.Context, key string) ([]Subscription, error) {
	return nil, errors.New("bypass cache")
}

func (b *BypassSubscriptionsCache) Save(ctx context.Context, key string, save []Subscription) error {
	return nil
}

func (b *BypassSubscriptionsCache) Clear(ctx context.Context) error {
	return nil
}

func NewBypassSubscriptionsCache() *BypassSubscriptionsCache {
	return &BypassSubscriptionsCache{}
}
