package mockaccount

import (
	"context"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

var _ account.Account = &MockAccountManager{}

type anyCredential struct{}

func (a *anyCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "ABC123",
		ExpiresOn: time.Now().Add(time.Hour * 1),
	}, nil

}

func LoggedInFakeAccount() *MockAccountManager {
	return &MockAccountManager{
		DefaultLocation:     "eastus",
		DefaultSubscription: "12345678-1234-1234-1234-123456789012",
		Subscriptions: []account.Subscription{
			{
				Id:                 "12345678-1234-1234-1234-123456789012",
				Name:               "My Subscription",
				TenantId:           "12345678-1234-1234-1234-123456789012",
				UserAccessTenantId: "12345678-1234-1234-1234-123456789012",
			},
		},
		Locations: []account.Location{
			{
				Name:                "eastus",
				DisplayName:         "East US",
				RegionalDisplayName: "East US",
			},
		},
		TenantCredentials: map[string]azcore.TokenCredential{
			"12345678-1234-1234-1234-123456789012": &anyCredential{},
			// home tenant credential
			"": &anyCredential{},
		},
	}
}

type MockAccountManager struct {
	DefaultLocation     string
	DefaultSubscription string

	TenantCredentials map[string]azcore.TokenCredential
	Subscriptions     []account.Subscription
	Locations         []account.Location
}

// ClearSubscriptions implements account.Account.
func (a *MockAccountManager) ClearSubscriptions(ctx context.Context) error {
	a.Subscriptions = nil
	return nil
}

// GetLocation implements account.Account.
func (a *MockAccountManager) GetLocation(ctx context.Context, subscriptionId string, locationName string) (account.Location, error) {
	for _, loc := range a.Locations {
		if loc.Name == locationName {
			return loc, nil
		}
	}

	return account.Location{}, os.ErrNotExist
}

// GetSubscription implements account.Account.
func (a *MockAccountManager) GetSubscription(ctx context.Context, subscriptionId string) (*account.Subscription, error) {
	for _, sub := range a.Subscriptions {
		if sub.Id == subscriptionId {
			return &sub, nil
		}
	}

	return nil, os.ErrNotExist
}

// RefreshSubscriptions implements account.Account.
func (a *MockAccountManager) RefreshSubscriptions(ctx context.Context) error {
	return nil
}

// CredentialForSubscription implements account.Account.
func (a *MockAccountManager) CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error) {
	tenantId, err := a.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return a.TenantCredentials[tenantId], nil
}

// CredentialForTenant implements account.Account.
func (a *MockAccountManager) CredentialForTenant(ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	return a.TenantCredentials[tenantId], nil
}

// LookupTenant implements account.Account.
func (a *MockAccountManager) LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error) {
	for _, sub := range a.Subscriptions {
		if sub.Id == subscriptionId {
			return sub.UserAccessTenantId, nil
		}
	}

	return "", os.ErrNotExist
}

func (a *MockAccountManager) Clear(ctx context.Context) error {
	a.DefaultLocation = ""
	a.DefaultSubscription = ""
	return nil
}

func (a *MockAccountManager) HasDefaultSubscription() bool {
	return a.DefaultSubscription != ""
}

func (a *MockAccountManager) HasDefaultLocation() bool {
	return a.DefaultLocation != ""
}

func (a *MockAccountManager) GetAccountDefaults(ctx context.Context) (*account.Config, error) {
	return &account.Config{
		DefaultSubscription: &account.Subscription{
			Id:                 a.DefaultSubscription,
			Name:               "",
			TenantId:           "",
			UserAccessTenantId: "",
			IsDefault:          true,
		},
		DefaultLocation: &account.Location{},
	}, nil
}
func (a *MockAccountManager) GetSubscriptionsWithDefaultSet(ctx context.Context) ([]account.Subscription, error) {
	subscriptions := a.Subscriptions
	for _, sub := range subscriptions {
		if sub.Id == a.DefaultSubscription {
			sub.IsDefault = true
		}
	}
	return subscriptions, nil
}

func (a *MockAccountManager) GetSubscriptions(ctx context.Context) ([]account.Subscription, error) {
	return a.Subscriptions, nil
}

func (a *MockAccountManager) GetDefaultLocationName(ctx context.Context) string {
	return a.DefaultLocation
}

func (a *MockAccountManager) GetDefaultSubscriptionID(ctx context.Context) string {
	return a.DefaultSubscription
}

func (a *MockAccountManager) GetLocations(ctx context.Context, subscriptionId string) ([]account.Location, error) {
	return a.Locations, nil
}

func (a *MockAccountManager) SetDefaultSubscription(
	ctx context.Context, subscriptionId string) (*account.Subscription, error) {
	a.DefaultSubscription = subscriptionId
	for _, sub := range a.Subscriptions {
		if sub.Id == subscriptionId {
			return &sub, nil
		}
	}

	return nil, nil
}

func (a *MockAccountManager) SetDefaultLocation(
	ctx context.Context, subscriptionId string, location string) (*account.Location, error) {
	a.DefaultLocation = location
	for _, loc := range a.Locations {
		if loc.Name == location {
			return &account.Location{
				Name:                loc.Name,
				DisplayName:         loc.DisplayName,
				RegionalDisplayName: loc.RegionalDisplayName,
			}, nil
		}
	}

	return nil, nil
}

// SubscriptionTenantResolverFunc implements [account.SubscriptionCredentialProvider] using the provided function.
type SubscriptionCredentialProviderFunc func(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)

func (f SubscriptionCredentialProviderFunc) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	return f(ctx, subscriptionId)
}
