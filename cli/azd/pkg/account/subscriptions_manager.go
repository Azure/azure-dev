package account

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"go.uber.org/multierr"
)

// SubscriptionTenantResolver allows resolving the correct tenant ID
// that allows the current account access to a given subscription.
type SubscriptionTenantResolver interface {
	// Resolve the tenant ID required by the current account to access the given subscription.
	LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error)
}

type principalInfoProvider interface {
	GetLoggedInServicePrincipalTenantID() (*string, error)
}

type subCache interface {
	Load() ([]Subscription, error)
	Save(save []Subscription) error
	Clear() error
}

// SubscriptionsManager manages listing, storing and retrieving subscriptions for the current account.
//
// Since the application supports multi-tenancy, subscriptions can be accessed by the user through different tenants.
// To lookup access to a given subscription, LookupTenant can be used to lookup the
// current account's required tenantID to access a given subscription.
type SubscriptionsManager struct {
	service       *azcli.SubscriptionsService
	principalInfo principalInfoProvider
	cache         subCache
	msg           input.Messaging
}

func NewSubscriptionsManager(
	service *azcli.SubscriptionsService,
	auth *auth.Manager,
	msg input.Messaging) (*SubscriptionsManager, error) {
	cache, err := NewSubscriptionsCache()
	if err != nil {
		return nil, err
	}

	return &SubscriptionsManager{
		service:       service,
		cache:         cache,
		principalInfo: auth,
		msg:           msg,
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

// Resolve the tenant ID required by the current account to access the given subscription.
//
//   - If the account is logged in with a service principal specified, the service principal's tenant ID
//     is immediately returned (single-tenant mode).
//
//   - Otherwise, the tenant ID is resolved by examining the stored subscriptionID to tenantID cache.
//     See SubscriptionCache for details about caching. On cache miss, all tenants and subscriptions are queried from
//     azure management services for the current account to build the mapping and populate the cache.
func (m *SubscriptionsManager) LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error) {
	principalTenantId, err := m.principalInfo.GetLoggedInServicePrincipalTenantID()
	if err != nil {
		return "", err
	}

	if principalTenantId != nil {
		return *principalTenantId, nil
	}

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
		"failed to resolve user access to subscription with ID '%s'. "+
			"If you recently gained access to this subscription, run `azd login` again to reload subscriptions.\n"+
			"Otherwise, visit this subscription in Azure Portal using the browser, "+
			"then run `azd login` ",
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

type tenantSubsResult struct {
	subs []Subscription
	err  error
}

// ListSubscription lists subscriptions accessible by the current account by calling azure management services.
func (m *SubscriptionsManager) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	var err error
	ctx, span := telemetry.GetTracer().Start(ctx, events.AccountSubscriptionsListEvent)
	defer span.EndWithStatus(err)

	stop := m.msg.ShowProgress(ctx, "Retrieving subscriptions...")
	defer stop()

	principalTenantId, err := m.principalInfo.GetLoggedInServicePrincipalTenantID()
	if err != nil {
		return nil, err
	}

	// If account is a service principal, we can speed up listing by skipping subscription listing across tenants since a
	// service principal is tied to a single tenant.
	if principalTenantId != nil {
		subscriptions, err := m.service.ListSubscriptions(ctx, *principalTenantId)
		if err != nil {
			return nil, err
		}

		tenantSubscriptions := []Subscription{}
		for _, subscription := range subscriptions {
			tenantSubscriptions = append(tenantSubscriptions, toSubscription(subscription, *principalTenantId))
		}

		return tenantSubscriptions, nil
	}

	tenants, err := m.service.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}

	span.SetAttributes(fields.AccountSubscriptionsListTenantsFound.Int(len(tenants)))

	listForTenant := func(
		jobs <-chan armsubscriptions.TenantIDDescription,
		results chan<- tenantSubsResult,
		service *azcli.SubscriptionsService) {
		for tenant := range jobs {
			azSubs, err := service.ListSubscriptions(ctx, *tenant.TenantID)
			if err != nil {
				errorMsg := err.Error()
				displayName := *tenant.DisplayName
				if strings.Contains(errorMsg, "AADSTS50076") {
					err = fmt.Errorf(
						"%s requires Multi-Factor Authentication (MFA). "+
							"To authenticate, login with `azd login --tenant-id %s`",
						displayName,
						*tenant.DefaultDomain)
				} else {
					err = fmt.Errorf("failed to load subscriptions from tenant '%s' : %w", displayName, err)
				}
			}

			results <- tenantSubsResult{toSubscriptions(azSubs, *tenant.TenantID), err}
		}
	}

	numJobs := len(tenants)
	jobs := make(chan armsubscriptions.TenantIDDescription, numJobs)
	results := make(chan tenantSubsResult, numJobs)
	// Apply max number of concurrent workers at 50
	numWorkers := int(math.Min(float64(len(tenants)), 50))
	for i := 0; i < numWorkers; i++ {
		go listForTenant(jobs, results, m.service)
	}

	for i := 0; i < numJobs; i++ {
		jobs <- tenants[i]
	}
	close(jobs)

	allSubscriptions := []Subscription{}
	errors := []error{}
	oneSuccess := false
	for i := 0; i < numJobs; i++ {
		res := <-results
		if res.err != nil {
			errors = append(errors, res.err)
			continue
		}

		oneSuccess = true
		allSubscriptions = append(allSubscriptions, res.subs...)
	}

	sort.Slice(allSubscriptions, func(i, j int) bool {
		return allSubscriptions[i].Name < allSubscriptions[j].Name
	})

	span.SetAttributes(fields.AccountSubscriptionsListTenantsFailed.Int(len(errors)))

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
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	return m.service.ListSubscriptionLocations(ctx, subscriptionId, tenantId)
}

func (m *SubscriptionsManager) GetSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	azSub, err := m.service.GetSubscription(ctx, subscriptionId, tenantId)
	if err != nil {
		return nil, err
	}

	sub := toSubscription(*azSub, tenantId)
	return &sub, nil
}

func toSubscriptions(azSubs []azcli.AzCliSubscriptionInfo, userAccessTenantId string) []Subscription {
	if azSubs == nil {
		return nil
	}

	subs := make([]Subscription, 0, len(azSubs))
	for _, azSub := range azSubs {
		subs = append(subs, toSubscription(azSub, userAccessTenantId))
	}
	return subs
}

func toSubscription(subscription azcli.AzCliSubscriptionInfo, userAccessTenantId string) Subscription {
	return Subscription{
		Id:                 subscription.Id,
		Name:               subscription.Name,
		TenantId:           subscription.TenantId,
		UserAccessTenantId: userAccessTenantId,
	}
}
