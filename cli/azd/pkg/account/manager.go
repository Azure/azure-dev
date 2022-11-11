package account

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

// JSON document path locations for default subscription & location
const (
	defaultSubscriptionKeyPath = "defaults.subscription"
	defaultLocationKeyPath     = "defaults.location"
)

// The default location to use in AZD when not previously set to any value
var defaultLocation Location = Location{
	Name:        "eastus2",
	DisplayName: "(US) East US 2",
}

// Manages azd account configuration
type Manager struct {
	// Path to the local azd user configuration file
	filePath      string
	configManager config.Manager
	config        config.Config
	azCli         azcli.AzCli
}

// Creates a new Account Manager instance
func NewManager(configManager config.Manager, azCli azcli.AzCli) (*Manager, error) {
	filePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	azdConfig, err := configManager.Load(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("configuration file '%s' does not exist. Creating new empty config.", filePath)
			azdConfig = config.NewConfig(nil)
		} else {
			return nil, err
		}
	}

	return &Manager{
		filePath:      filePath,
		azCli:         azCli,
		configManager: configManager,
		config:        azdConfig,
	}, nil
}

// Gets the default subscription for the logged in account.
// 1. Returns AZD config defaults if exists
// 2. Returns Coded location default if needed
func (m *Manager) GetAccountDefaults(ctx context.Context) (*Account, error) {
	subscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving default subscription: %w", err)
	}

	var location *Location

	if subscription == nil {
		location = &defaultLocation
	} else {
		location, err = m.getDefaultLocation(ctx, subscription.Id)
		if err != nil {
			return nil, fmt.Errorf("failed retrieving default location: %w", err)
		}
	}

	return &Account{
		DefaultSubscription: subscription,
		DefaultLocation:     location,
	}, nil
}

// Gets the available Azure subscriptions for the current logged in account.
// Applies the default subscription on the matching account
func (m *Manager) GetSubscriptions(ctx context.Context) ([]*azcli.AzCliSubscriptionInfo, error) {
	defaultSubscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		return nil, err
	}

	accounts, err := m.getAllSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	// If we don't have any default explicitly set return raw account list without and default set
	if defaultSubscription == nil {
		return accounts, nil
	}

	// If default subscription is set, set it in the results
	results := []*azcli.AzCliSubscriptionInfo{}
	for _, sub := range accounts {
		if sub.Id == defaultSubscription.Id {
			sub.IsDefault = true
		}
		results = append(results, sub)
	}

	return results, nil
}

// Gets the available Azure locations for the specified Azure subscription.
func (m *Manager) GetLocations(ctx context.Context, subscriptionId string) ([]azcli.AzCliLocation, error) {
	locations, err := m.azCli.ListAccountLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", subscriptionId, err)
	}

	return locations, nil
}

// Sets the default Azure subscription for the current logged in principal.
func (m *Manager) SetDefaultSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	subscription, err := m.azCli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed getting account for id '%s'", subscriptionId)
	}

	err = m.config.Set(defaultSubscriptionKeyPath, subscription.Id)
	if err != nil {
		return nil, fmt.Errorf("failed setting default subscription: %w", err)
	}

	err = m.configManager.Save(m.config, m.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return &Subscription{
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}, nil
}

// Sets the default Azure location for the current logged in principal.
func (m *Manager) SetDefaultLocation(ctx context.Context, subscriptionId string, location string) (*Location, error) {
	locations, err := m.GetLocations(ctx, subscriptionId)
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

	err = m.config.Set(defaultLocationKeyPath, matchingLocation.Name)
	if err != nil {
		return nil, fmt.Errorf("failed setting default location: %w", err)
	}

	err = m.configManager.Save(m.config, m.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return &Location{
		Name:        matchingLocation.Name,
		DisplayName: matchingLocation.RegionalDisplayName,
	}, nil
}

// Checks whether account related defaults of subscription and location have previously been set
func (m *Manager) HasDefaults() bool {
	_, hasDefaultSubscription := m.config.Get(defaultSubscriptionKeyPath)
	_, hasDefaultLocation := m.config.Get(defaultLocationKeyPath)

	return hasDefaultSubscription && hasDefaultLocation
}

// Clears any persisted defaults in the AZD config
func (m *Manager) Clear(ctx context.Context) error {
	err := m.config.Unset("defaults")
	if err != nil {
		return fmt.Errorf("failed clearing defaults: %w", err)
	}

	err = m.configManager.Save(m.config, m.filePath)
	if err != nil {
		return fmt.Errorf("failed saving AZD configuration: %w", err)
	}

	return nil
}

// Gets the available Azure subscriptions for the current logged in principal.
func (m *Manager) getAllSubscriptions(ctx context.Context) ([]*azcli.AzCliSubscriptionInfo, error) {
	accounts, err := m.azCli.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing azure subscriptions: %w", err)
	}

	// If default subscription is set, set it in the results
	results := []*azcli.AzCliSubscriptionInfo{}
	results = append(results, accounts...)

	return results, nil
}

// Returns the default subscription for the current logged in principal
// If set in config will return the configured subscription
// otherwise will return the first subscription found.
func (m *Manager) getDefaultSubscription(ctx context.Context) (*Subscription, error) {
	// Get the default subscription ID from azd configuration
	configSubscriptionId, ok := m.config.Get(defaultSubscriptionKeyPath)

	if !ok {
		return nil, nil
	}

	subscriptionId := fmt.Sprint(configSubscriptionId)
	subscription, err := m.azCli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf(
			`the subscription id '%s' is either invalid or you no longer have access. 
			Check your configuration with 'azd config list'. %w`,
			subscriptionId,
			err,
		)
	}

	return &Subscription{
		Id:       subscription.Id,
		Name:     subscription.Name,
		TenantId: subscription.TenantId,
	}, nil
}

// Gets the default Azure location for the specified subscription
// When specified in azd config, will return the location when valid, otherwise azd global default (eastus2)
func (m *Manager) getDefaultLocation(ctx context.Context, subscriptionId string) (*Location, error) {
	configLocation, ok := m.config.Get(defaultLocationKeyPath)
	if !ok {
		return &defaultLocation, nil
	}

	locationName := fmt.Sprint(configLocation)
	allLocations, err := m.GetLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving account locations: %w", err)
	}

	index := slices.IndexFunc(allLocations, func(l azcli.AzCliLocation) bool {
		return l.Name == locationName
	})

	if index < 0 {
		return nil, fmt.Errorf("the location '%s' is invalid. Check your configuration with `azd config list`", locationName)
	}

	return &Location{
		Name:        allLocations[index].Name,
		DisplayName: allLocations[index].RegionalDisplayName,
	}, nil
}
