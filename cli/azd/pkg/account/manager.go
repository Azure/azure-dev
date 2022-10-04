package account

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

type Manager struct {
	azCli azcli.AzCli
}

// Creates a new Account Manager instance
func NewManager(ctx context.Context) *Manager {
	return &Manager{
		azCli: azcli.GetAzCli(ctx),
	}
}

// Gets the default subscription for the logged in account.
// 1. Returns AZD config defaults if exists
// 2. Returns AZ CLI defaults if exists
func (m *Manager) GetAccountDefaults(ctx context.Context) (*config.Config, error) {
	azdConfig, err := config.Load()

	if err == nil && azdConfig.DefaultSubscription != nil {
		return azdConfig, nil
	}

	if azdConfig == nil {
		azdConfig = &config.Config{}
	}

	subscription, err := m.azCli.GetDefaultAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving default subscription from Azure CLI")
	}

	azdConfig.DefaultSubscription = &config.Subscription{
		Id:   subscription.Id,
		Name: subscription.Name,
	}

	return azdConfig, nil
}

// Gets the available Azure subscriptions for the current logged in account.
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

// Gets the available Azure locations for the default Azure subscription.
func (m *Manager) GetLocations(ctx context.Context) ([]azcli.AzCliLocation, error) {
	azdConfig, err := m.GetAccountDefaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving account defaults: %w", err)
	}

	locations, err := m.azCli.ListAccountLocations(ctx, azdConfig.DefaultSubscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", azdConfig.DefaultSubscription.Id, err)
	}

	return locations, nil
}

// Sets the default Azure subscription for the current logged in account.
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

// Sets the default Azure location for the current logged in account.
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

// Clears and persisted defaults in the AZD config
func (m *Manager) Clear(ctx context.Context) error {
	azdConfig, err := config.Load()
	if err != nil {
		// If a config was never saved, nothing to do
		return nil
	}

	azdConfig.DefaultSubscription = nil
	azdConfig.DefaultLocation = nil

	err = azdConfig.Save()
	if err != nil {
		return fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return nil
}
