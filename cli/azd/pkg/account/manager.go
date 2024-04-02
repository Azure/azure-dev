package account

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// JSON document path locations for default subscription & location
const (
	defaultSubscriptionKeyPath = "defaults.subscription"
	defaultLocationKeyPath     = "defaults.location"
)

// The default location to use in AZD when not previously set to any value
var defaultLocation Location = Location{
	Name:                "eastus2",
	DisplayName:         "East US 2",
	RegionalDisplayName: "(US) East US 2",
}

// Handles getting and setting configuration
//   - Defaults
//
// Handles subscriptions and tenants related to the account (API calls) -- move to client
// Handles caching around subscriptions and tenants (possible -- this is all local data)
//   - Who is responsible for lazy loading the cache?
//
// Handles middle-tier presentation of "subscriptions and locations"
// In VS, we have a "subscription picker" that is a dropdown of subscriptions
type Account interface {
	Clear(ctx context.Context) error
	HasDefaultSubscription() bool
	HasDefaultLocation() bool
	GetAccountDefaults(ctx context.Context) (*Config, error)
	GetDefaultLocationName(ctx context.Context) string
	GetDefaultSubscriptionID(ctx context.Context) string
	GetSubscriptions(ctx context.Context) ([]Subscription, error)
	GetSubscriptionsWithDefaultSet(ctx context.Context) ([]Subscription, error)
	GetLocations(ctx context.Context, subscriptionId string) ([]Location, error)
	SetDefaultSubscription(ctx context.Context, subscriptionId string) (*Subscription, error)
	SetDefaultLocation(ctx context.Context, subscriptionId string, location string) (*Location, error)

	GetSubscription(ctx context.Context, subscriptionId string) (*Subscription, error)
	GetLocation(ctx context.Context, subscriptionId, locationName string) (Location, error)

	ClearSubscriptions(ctx context.Context) error
	RefreshSubscriptions(ctx context.Context) error

	// Resolve the tenant ID required by the current account to access the given subscription.
	LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error)

	CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)
	CredentialForTenant(ctx context.Context, tenantId string) (azcore.TokenCredential, error)
}

// Represents an azd account.
type account struct {
	// Path to the local azd user configuration file
	filePath      string
	configManager config.FileConfigManager
	config        config.Config

	auth  *auth.Manager
	cloud *cloud.Cloud

	armClientOptions *arm.ClientOptions

	// In-memory store for tenant credentials. Since azcore.TokenCredential is usually backed by a publicClient
	// that holds an in-memory cache, we need to hold on to azcore.TokenCredential instances to maintain that cache.
	// It also allows us to call EnsureLoggedInCredential once.
	tenantCredentials sync.Map

	// Typically auth.Manager
	principalInfo principalInfoProvider

	cache   subCache
	console input.Console
}

// Creates a new Account Manager instance
func NewManager(
	auth *auth.Manager,
	cloud *cloud.Cloud,
	configManager config.FileConfigManager,
	armClientOptions *arm.ClientOptions,
	console input.Console) (Account, error) {
	filePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	azdConfig, err := configManager.Load(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("configuration file '%s' does not exist. Creating new empty config.", filePath)
			azdConfig = config.NewEmptyConfig()
		} else {
			return nil, err
		}
	}

	cache, err := newSubCache()
	if err != nil {
		return nil, err
	}

	return &account{
		auth:             auth,
		armClientOptions: armClientOptions,
		cloud:            cloud,
		filePath:         filePath,
		configManager:    configManager,
		config:           azdConfig,
		console:          console,
		principalInfo:    auth,
		cache:            cache,
	}, nil
}

// Gets the default subscription for the logged in account.
// 1. Returns AZD config defaults if exists
// 2. Returns Coded location default if needed
func (m *account) GetAccountDefaults(ctx context.Context) (*Config, error) {
	subscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		// logging the error, but we don't want to fail, as this could only
		// means an account change
		log.Println(fmt.Errorf("failed retrieving default subscription: %w", err).Error())
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

	return &Config{
		DefaultSubscription: subscription,
		DefaultLocation:     location,
	}, nil
}

// Gets the available Azure subscriptions for the current logged in account, across all tenants the user has access to.
// Applies the default subscription on the matching account
func (m *account) GetSubscriptionsWithDefaultSet(ctx context.Context) ([]Subscription, error) {
	defaultSubscription, err := m.getDefaultSubscription(ctx)
	if err != nil {
		return nil, err
	}

	subscriptions, err := m.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	// If we don't have any default explicitly set return raw account list without and default set
	if defaultSubscription == nil {
		return subscriptions, nil
	}

	// If default subscription is set, set it in the results
	results := []Subscription{}
	for _, sub := range subscriptions {
		if sub.Id == defaultSubscription.Id {
			sub.IsDefault = true
		}
		results = append(results, sub)
	}

	return results, nil
}

// Gets the available Azure locations for the specified Azure subscription.
func (m *account) GetLocations(ctx context.Context, subscriptionId string) ([]Location, error) {
	locations, err := m.ListLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving Azure location for account '%s': %w", subscriptionId, err)
	}

	return locations, nil
}

// Sets the default Azure subscription for the current logged in principal.
func (m *account) SetDefaultSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	subscription, err := m.GetSubscription(ctx, subscriptionId)
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
func (m *account) SetDefaultLocation(ctx context.Context, subscriptionId string, location string) (*Location, error) {
	locations, err := m.ListLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving locations: %w", err)
	}

	index := slices.IndexFunc(locations, func(l Location) bool {
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

	return &matchingLocation, nil
}

// HasDefaultSubscription returns true if a default subscription has been configured (i.e defaults.subscription is set)
func (m *account) HasDefaultSubscription() bool {
	_, hasDefaultSubscription := m.config.Get(defaultSubscriptionKeyPath)

	return hasDefaultSubscription
}

// HasDefaultLocation returns true if a default location has been configured (i.e defaults.location is set)
func (m *account) HasDefaultLocation() bool {
	_, hasDefaultLocation := m.config.Get(defaultLocationKeyPath)

	return hasDefaultLocation
}

// Clears any persisted defaults in the AZD config
func (m *account) Clear(ctx context.Context) error {
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

// Returns the default subscription ID stored in configuration.
// If configuration is not found or invalid, an empty string is returned.
func (m *account) GetDefaultSubscriptionID(ctx context.Context) string {
	// Get the default subscription ID from azd configuration
	configSubscriptionId, ok := m.config.Get(defaultSubscriptionKeyPath)
	if !ok {
		return ""
	}

	subId, ok := configSubscriptionId.(string)
	if !ok {
		return ""
	}

	return subId
}

// Returns the default subscription for the current logged in principal
// If set in config will return the configured subscription
// otherwise will return nil.
func (m *account) getDefaultSubscription(ctx context.Context) (*Subscription, error) {
	// Get the default subscription ID from azd configuration
	configSubscriptionId, ok := m.config.Get(defaultSubscriptionKeyPath)

	if !ok {
		return nil, nil
	}

	subscriptionId := fmt.Sprint(configSubscriptionId)
	subscription, err := m.GetSubscription(ctx, subscriptionId)
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

// Gets the default Azure location name stored in configuration.
// If configuration is not found or invalid, a default location (eastus2) is returned.
func (m *account) GetDefaultLocationName(ctx context.Context) string {
	configLocation, ok := m.config.Get(defaultLocationKeyPath)
	if !ok {
		return defaultLocation.Name
	}

	location, ok := configLocation.(string)
	if !ok {
		return defaultLocation.Name
	}

	return location
}

// Gets the default Azure location stored in configuration
// When specified in azd config, will return the location when valid, otherwise azd global default (eastus2)
func (m *account) getDefaultLocation(ctx context.Context, subscriptionId string) (*Location, error) {
	configLocation, ok := m.config.Get(defaultLocationKeyPath)
	if !ok {
		return &defaultLocation, nil
	}

	locationName := fmt.Sprint(configLocation)
	allLocations, err := m.ListLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving account locations: %w", err)
	}

	index := slices.IndexFunc(allLocations, func(l Location) bool {
		return l.Name == locationName
	})

	if index < 0 {
		return nil, fmt.Errorf("the location '%s' is invalid. Check your configuration with `azd config list`", locationName)
	}

	return &allLocations[index], nil
}
