package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

type Manager struct {
	azCli azcli.AzCli
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{
		azCli: azcli.GetAzCli(ctx),
	}
}

func (m *Manager) GetSubscriptions(ctx context.Context) ([]azcli.AzCliSubscriptionInfo, error) {
	config := config.GetConfig(ctx)

	accounts, err := m.azCli.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing azure subscriptions: %w", err)
	}

	if config.DefaultSubscription == nil {
		return accounts, nil
	}

	// If default subscription is set, set it in the results
	results := []azcli.AzCliSubscriptionInfo{}
	for _, sub := range accounts {
		if sub.Id == config.DefaultSubscription.Id {
			sub.IsDefault = true
		}
		results = append(results, sub)
	}

	return results, nil
}

func (m *Manager) GetLocations(ctx context.Context) ([]azcli.AzCliLocation, error) {
	config := config.GetConfig(ctx)
	if config.DefaultSubscription == nil {
		return nil, errors.New("default subscription has not been set")
	}

	locations, err := m.azCli.ListAccountLocations(ctx, config.DefaultSubscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", config.DefaultSubscription.Id, err)
	}

	return locations, nil
}

func (m *Manager) SetDefaultSubscription(ctx context.Context, subscriptionId string) (*config.Subscription, error) {
	azdConfig, err := config.Load()
	if err != nil {
		azdConfig = &config.Config{}
	}

	subscription, err := m.azCli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed getting account for id '%s'", subscriptionId)
	}

	azdConfig.DefaultSubscription = &config.Subscription{
		Id:   subscription.Id,
		Name: subscription.Name,
	}

	err = azdConfig.Save()
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return azdConfig.DefaultSubscription, nil
}

func (m *Manager) SetDefaultLocation(ctx context.Context, location string) (*config.Location, error) {
	azdConfig, err := config.Load()
	if err != nil {
		azdConfig = &config.Config{}
	}

	locations, err := m.GetLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving locations: %w", err)
	}

	index := slices.IndexFunc(locations, func(l azcli.AzCliLocation) bool {
		return l.Name == location
	})

	if index < 0 {
		return nil, fmt.Errorf("location '%s' is not valid", location)
	}

	matchingLocation := locations[index]

	azdConfig.DefaultLocation = &config.Location{
		Name:        matchingLocation.Name,
		DisplayName: matchingLocation.DisplayName,
	}

	err = azdConfig.Save()
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return azdConfig.DefaultLocation, nil
}
