package account

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

// Manages azd account configuration
// `az cli` is not required and will only be called `azd` default have not already been set.
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
	azdConfig := config.GetConfig(ctx)

	if azdConfig.DefaultSubscription != nil && azdConfig.DefaultLocation != nil {
		return azdConfig, nil
	}

	if azdConfig.DefaultSubscription == nil {
		defaultSubscription, err := m.getDefaultSubscription(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed retrieving default subscription from Azure CLI: %w", err)
		}

		azdConfig.DefaultSubscription = defaultSubscription
	}

	if azdConfig.DefaultLocation == nil {
		defaultLocation, err := m.getDefaultLocation(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed retrieving default location from Azure CLI: %w", err)
		}

		azdConfig.DefaultLocation = defaultLocation
	}

	return azdConfig, nil
}

// Gets the available Azure subscriptions for the current logged in account.
func (m *Manager) GetSubscriptions(ctx context.Context) ([]azcli.AzCliSubscriptionInfo, error) {
	defaultSubscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		return nil, err
	}

	accounts, err := m.azCli.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing azure subscriptions: %w", err)
	}

	if defaultSubscription == nil {
		return accounts, nil
	}

	// If default subscription is set, set it in the results
	results := []azcli.AzCliSubscriptionInfo{}
	for _, sub := range accounts {
		if sub.Id == defaultSubscription.Id {
			sub.IsDefault = true
		}
		results = append(results, sub)
	}

	return results, nil
}

// Gets the available Azure locations for the default Azure subscription.
func (m *Manager) GetLocations(ctx context.Context) ([]azcli.AzCliLocation, error) {
	defaultSubscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		return nil, err
	}

	locations, err := m.azCli.ListAccountLocations(ctx, defaultSubscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", defaultSubscription.Id, err)
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
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}

	err = azdConfig.Save()
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return azdConfig.DefaultSubscription, nil
}

// Sets the default Azure subscription for the current logged in account.
func (m *Manager) SetDefaultSubscriptionWithName(ctx context.Context, subscriptionName string) (*config.Subscription, error) {
	subscriptions, err := m.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	// Lookup subscriptions and attempt to match by name
	subIndex := slices.IndexFunc(subscriptions, func(s azcli.AzCliSubscriptionInfo) bool {
		return strings.TrimSpace(strings.ToLower(subscriptionName)) == strings.ToLower(s.Name)
	})

	if subIndex < 0 {
		return nil, fmt.Errorf("subscription '%s' not found", subscriptionName)
	}

	return m.SetDefaultSubscription(ctx, subscriptions[subIndex].Id)
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
		DisplayName: matchingLocation.RegionalDisplayName,
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

func (m *Manager) getDefaultSubscription(ctx context.Context) (*config.Subscription, error) {
	azdConfig := config.GetConfig(ctx)

	if azdConfig != nil && azdConfig.DefaultSubscription != nil {
		return azdConfig.DefaultSubscription, nil
	}

	subscription, err := m.azCli.GetDefaultAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving default subscription from Azure CLI: %w", err)
	}

	return &config.Subscription{
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}, nil
}

func (m *Manager) getDefaultLocation(ctx context.Context) (*config.Location, error) {
	azdConfig := config.GetConfig(ctx)

	if azdConfig != nil && azdConfig.DefaultLocation != nil {
		return azdConfig.DefaultLocation, nil
	}

	defaultLocation := &config.Location{
		Name:        "eastus2",
		DisplayName: "(US) East US 2",
	}

	configValue, err := m.azCli.GetCliConfigValue(ctx, "defaults.location")
	if err == nil {
		allLocations, err := m.GetLocations(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed retrieving account locations: %w", err)
		}

		index := slices.IndexFunc(allLocations, func(l azcli.AzCliLocation) bool {
			return l.Name == configValue.Value
		})

		if index > -1 {
			return &config.Location{
				Name:        allLocations[index].Name,
				DisplayName: allLocations[index].RegionalDisplayName,
			}, nil
		}
	}

	return defaultLocation, nil
}
