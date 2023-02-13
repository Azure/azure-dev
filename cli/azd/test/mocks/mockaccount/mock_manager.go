package mockaccount

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type MockAccountManager struct {
	DefaultLocation     string
	DefaultSubscription string

	Subscriptions []account.Subscription
	Locations     []azcli.AzCliLocation
}

func (a *MockAccountManager) Clear(ctx context.Context) error {
	a.DefaultLocation = ""
	a.DefaultSubscription = ""
	return nil
}

func (a *MockAccountManager) HasDefaults() bool {
	return a.DefaultLocation != "" || a.DefaultSubscription != ""
}

func (a *MockAccountManager) GetAccountDefaults(ctx context.Context) (*account.Account, error) {
	return &account.Account{
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

func (a *MockAccountManager) GetLocations(ctx context.Context, subscriptionId string) ([]azcli.AzCliLocation, error) {
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
				Name:        loc.Name,
				DisplayName: loc.DisplayName,
			}, nil
		}
	}

	return nil, nil
}
