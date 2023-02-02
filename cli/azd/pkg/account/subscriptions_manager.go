package account

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"go.uber.org/multierr"
)

type subCache interface {
	Load() ([]Subscription, error)
	Save(save []Subscription) error
	Clear() error
}

type SubscriptionsManager struct {
	service *azcli.SubscriptionsService
	cache   subCache
}

func NewSubscriptionsManager(service *azcli.SubscriptionsService) (*SubscriptionsManager, error) {
	cache, err := NewSubscriptionsCache()
	if err != nil {
		return nil, err
	}

	return &SubscriptionsManager{
		service: service,
		cache:   cache,
	}, nil
}

// Clears stored cached subscriptions.
func (m *SubscriptionsManager) ClearSubscriptions(ctx context.Context) error {
	err := m.cache.Clear()
	if err != nil {
		return fmt.Errorf("clearing stored subscriptions: %w", err)
	}

	return nil
}

// Updates stored cached subscriptions.
func (m *SubscriptionsManager) RefreshSubscriptions(ctx context.Context) error {
	subs, err := m.ListSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("fetching subscriptions: %w", err)
	}

	err = m.cache.Save(subs)
	if err != nil {
		return fmt.Errorf("storing subscriptions: %w", err)
	}

	return nil
}

// Resolve the tenant ID required by the current user account to access the given subscription.
//
// The resolution is first done by examining the cache, then by querying azure management services. See SubscriptionCache
// for details about caching.
func (m *SubscriptionsManager) ResolveUserTenant(ctx context.Context, subscriptionId string) (tenantId string, err error) {
	subscriptions, err := m.GetSubscriptions(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving user access to subscription '%s' : %w", subscriptionId, err)
	}

	for _, sub := range subscriptions {
		if sub.Id == subscriptionId {
			return sub.UserAccessTenantId, nil
		}
	}

	return "", fmt.Errorf(
		"failed to resolve user access to subscription '%s'. Visit this subscription in an Azure Portal browser and try again.",
		subscriptionId)
}

// GetSubscriptions retrieves subscriptions accessible by the current account with caching semantics.
//
// Unlike ListSubscriptions, GetSubscriptions first examines the subscriptions cache.
// On cache miss, subscriptions are fetched, the cached is updated, before the result is returned.
func (m *SubscriptionsManager) GetSubscriptions(ctx context.Context) ([]Subscription, error) {
	subscriptions, err := m.cache.Load()
	if err != nil {
		subscriptions, err = m.ListSubscriptions(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing subscriptions: %w", err)
		}

		err = m.cache.Save(subscriptions)
		if err != nil {
			return nil, fmt.Errorf("saving subscriptions to cache: %w", err)
		}
	}

	return subscriptions, nil
}

// ListSubscription lists subscriptions accessible by the current account by calling azure management services.
func (m *SubscriptionsManager) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	tenants, err := m.service.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}

	allSubscriptions := []Subscription{}
	mfaTenants := []string{}
	errors := []error{}
	oneSuccess := false

	for _, tenant := range tenants {
		tenantId := *tenant.TenantID
		subscriptions, err := m.service.ListSubscriptions(ctx, tenantId)
		if err != nil {
			errorMsg := err.Error()
			if strings.Contains(errorMsg, "AADSTS50076") {
				mfaTenants = append(mfaTenants, tenantId)
			} else {
				errors = append(errors, fmt.Errorf("failed to load subscriptions from tenant %s: %s", tenantId, errorMsg))
			}

			continue
		}

		oneSuccess = true
		for _, subscription := range subscriptions {
			allSubscriptions = append(allSubscriptions, convertSubscription(&subscription, tenantId))
		}
	}

	sort.Slice(allSubscriptions, func(i, j int) bool {
		return allSubscriptions[i].Name < allSubscriptions[j].Name
	})

	if !oneSuccess && len(tenants) > 0 {
		return nil, multierr.Combine(errors...)
	}

	// If at least one was successful, log errors and continue
	for _, err := range errors {
		log.Println(err.Error())
	}

	return allSubscriptions, nil
}

func (m *SubscriptionsManager) ListLocations(ctx context.Context, subscriptionId string) ([]azcli.AzCliLocation, error) {
	tenantId, err := m.ResolveUserTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	return m.service.ListSubscriptionLocations(ctx, subscriptionId, tenantId)
}

func (m *SubscriptionsManager) GetSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	tenantId, err := m.ResolveUserTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	azSub, err := m.service.GetSubscription(ctx, subscriptionId, tenantId)
	if err != nil {
		return nil, err
	}

	sub := convertSubscription(azSub, tenantId)
	return &sub, nil
}

func convertSubscription(subscription *azcli.AzCliSubscriptionInfo, tenantId string) Subscription {
	return Subscription{
		Id:                 subscription.Id,
		Name:               subscription.Name,
		TenantId:           subscription.TenantId,
		UserAccessTenantId: tenantId,
	}
}
