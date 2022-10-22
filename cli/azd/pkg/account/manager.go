package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

const (
	defaultSubscriptionKeyPath = "defaults.subscription"
	defaultLocationKeyPath     = "defaults.location"
)

var defaultLocation Location = Location{
	Name:        "eastus2",
	DisplayName: "(US) East US 2",
}

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
func (m *Manager) GetAccountDefaults(ctx context.Context) (*Account, error) {
	subscription, err := m.getDefaultSubscription(ctx)

	// If we don't have a default subscription then this is likely the first run experience
	// or the subscription specified in configuration is invalid.
	if err != nil {
		subscription = nil
	}

	location, err := m.getDefaultLocation(ctx)

	// If we don't have a default location then either this is the first run experience
	// or the location specified in configuration is invalid.
	if err != nil {
		location = &defaultLocation
	}

	return &Account{
		DefaultSubscription: subscription,
		DefaultLocation:     location,
	}, nil
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

	if defaultSubscription == nil {
		return nil, errors.New("default subscription is required to load Azure locations for account")
	}

	locations, err := m.azCli.ListAccountLocations(ctx, defaultSubscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", defaultSubscription.Id, err)
	}

	return locations, nil
}

// Sets the default Azure subscription for the current logged in account.
func (m *Manager) SetDefaultSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	azdConfig, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed loading configuration: %w", err)
	}

	subscription, err := m.azCli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed getting account for id '%s'", subscriptionId)
	}

	err = azdConfig.Set(defaultSubscriptionKeyPath, subscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed setting default subscription: %w", err)
	}

	err = azdConfig.Save()
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return &Subscription{
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}, nil
}

// Sets the default Azure location for the current logged in account.
func (m *Manager) SetDefaultLocation(ctx context.Context, location string) (*Location, error) {
	azdConfig, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed loading configuration: %w", err)
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

	err = azdConfig.Set(defaultLocationKeyPath, matchingLocation.Name)
	if err != nil {
		return nil, fmt.Errorf("failed setting default location: %w", err)
	}

	err = azdConfig.Save()
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return &Location{
		Name:        matchingLocation.Name,
		DisplayName: matchingLocation.RegionalDisplayName,
	}, nil
}

// Clears and persisted defaults in the AZD config
func (m *Manager) Clear(ctx context.Context) error {
	azdConfig, err := config.Load()
	if err != nil {
		// If a config was never saved, nothing to do
		return nil
	}

	err = azdConfig.Unset("defaults")
	if err != nil {
		return fmt.Errorf("failed clearing defaults: %w", err)
	}

	err = azdConfig.Save()
	if err != nil {
		return fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return nil
}

func (m *Manager) getDefaultSubscription(ctx context.Context) (*Subscription, error) {
	azdConfig := config.GetConfig(ctx)

	configSubscriptionId, ok := azdConfig.Get(defaultSubscriptionKeyPath)
	if !ok {
		return nil, nil
	}

	subscriptionId := fmt.Sprint(configSubscriptionId)
	subscription, err := m.azCli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving subscription with ID '%s'. %w", subscriptionId, err)
	}

	return &Subscription{
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}, nil
}

func (m *Manager) getDefaultLocation(ctx context.Context) (*Location, error) {
	azdConfig := config.GetConfig(ctx)

	configLocation, ok := azdConfig.Get(defaultLocationKeyPath)
	if !ok {
		return &defaultLocation, nil
	}

	locationName := fmt.Sprint(configLocation)
	allLocations, err := m.GetLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving account locations: %w", err)
	}

	index := slices.IndexFunc(allLocations, func(l azcli.AzCliLocation) bool {
		return l.Name == locationName
	})

	if index > -1 {
		return &Location{
			Name:        allLocations[index].Name,
			DisplayName: allLocations[index].RegionalDisplayName,
		}, nil
	}

	return &defaultLocation, nil
}
