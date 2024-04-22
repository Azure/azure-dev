package account

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"go.uber.org/multierr"
)

type principalInfoProvider interface {
	GetLoggedInServicePrincipalTenantID(ctx context.Context) (*string, error)
}

type subCache interface {
	Load() ([]Subscription, error)
	Save(save []Subscription) error
	Clear() error
}

// Clears stored cached subscriptions. This can only return an error is a filesystem error other than ErrNotExist occurred.
func (m *account) ClearSubscriptions(ctx context.Context) error {
	err := m.cache.Clear()
	if err != nil {
		return fmt.Errorf("clearing stored subscriptions: %w", err)
	}

	return nil
}

// Updates stored cached subscriptions.
func (m *account) RefreshSubscriptions(ctx context.Context) error {
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
func (m *account) LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error) {
	principalTenantId, err := m.principalInfo.GetLoggedInServicePrincipalTenantID(ctx)
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
			"If you recently gained access to this subscription, run `azd auth login` again to reload subscriptions.\n"+
			"Otherwise, visit this subscription in Azure Portal using the browser, "+
			"then run `azd auth login` ",
		subscriptionId)
}

// Gets the available Azure subscriptions for the current logged in account, across all tenants the user has access to.
//
// Unlike ListSubscriptions, GetSubscriptions first examines the subscriptions cache.
// On cache miss, subscriptions are fetched, the cached is updated, before the result is returned.
func (m *account) GetSubscriptions(ctx context.Context) ([]Subscription, error) {
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

func (m *account) GetSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	subscriptions, err := m.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	for _, sub := range subscriptions {
		if sub.Id == subscriptionId {
			return &sub, nil
		}
	}
	return m.getSubscriptionImpl(ctx, subscriptionId)
}

type tenantSubsResult struct {
	subs []Subscription
	err  error
}

// ListSubscription lists subscriptions accessible by the current account by calling azure management services.
func (m *account) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	var err error
	ctx, span := tracing.Start(ctx, events.AccountSubscriptionsListEvent)
	defer span.EndWithStatus(err)

	msg := "Retrieving subscriptions..."
	m.console.ShowSpinner(ctx, msg, input.Step)
	defer m.console.StopSpinner(ctx, "", input.StepDone)

	principalTenantId, err := m.principalInfo.GetLoggedInServicePrincipalTenantID(ctx)
	if err != nil {
		return nil, err
	}

	// If account is a service principal, we can speed up listing by skipping subscription listing across tenants since a
	// service principal is tied to a single tenant.
	if principalTenantId != nil {
		subscriptions, err := m.listSubscriptions(ctx, *principalTenantId)
		if err != nil {
			return nil, err
		}

		tenantSubscriptions := []Subscription{}
		for _, subscription := range subscriptions {
			tenantSubscriptions = append(tenantSubscriptions, toSubscription(*subscription, *principalTenantId))
		}

		return tenantSubscriptions, nil
	}

	tenants, err := m.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}

	span.SetAttributes(fields.AccountSubscriptionsListTenantsFound.Int(len(tenants)))

	listForTenant := func(
		jobs <-chan armsubscriptions.TenantIDDescription,
		results chan<- tenantSubsResult) {
		for tenant := range jobs {
			azSubs, err := m.listSubscriptions(ctx, *tenant.TenantID)
			if err != nil {
				errorMsg := err.Error()
				name := *tenant.TenantID
				if tenant.DisplayName != nil {
					name = *tenant.DisplayName
				}

				if strings.Contains(errorMsg, "AADSTS50076") {
					idOrDomain := *tenant.TenantID
					if tenant.DefaultDomain != nil {
						idOrDomain = *tenant.DefaultDomain
					}

					err = fmt.Errorf(
						"%s requires Multi-Factor Authentication (MFA). "+
							"To authenticate, login with `azd auth login --tenant-id %s`",
						name,
						idOrDomain)
				} else {
					err = fmt.Errorf("failed to load subscriptions from tenant '%s' : %w", name, err)
				}
			}

			results <- tenantSubsResult{toSubscriptions(azSubs, *tenant.TenantID), err}
		}
	}

	numJobs := len(tenants)
	jobs := make(chan armsubscriptions.TenantIDDescription, numJobs)
	results := make(chan tenantSubsResult, numJobs)
	maxWorkers := 25
	if workerMax := os.Getenv("AZD_SUBSCRIPTIONS_FETCH_MAX_CONCURRENCY"); workerMax != "" {
		if val, err := strconv.ParseInt(workerMax, 10, 0); err == nil {
			maxWorkers = int(val)
		}
	}

	numWorkers := int(math.Min(float64(len(tenants)), float64(maxWorkers)))
	for i := 0; i < numWorkers; i++ {
		go listForTenant(jobs, results)
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
	close(results)

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

func (m *account) GetLocation(ctx context.Context, subscriptionId, locationName string) (Location, error) {
	var err error
	m.console.ShowSpinner(ctx, "Reading subscription and location from environment...", input.Step)
	defer m.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))

	allLocations, err := m.listLocations(ctx, subscriptionId)
	if err != nil {
		return Location{}, err
	}

	for _, location := range allLocations {
		if locationName == location.Name {
			return location, nil
		}
	}
	return Location{}, fmt.Errorf("location name %s not found", locationName)
}

func (m *account) ListLocations(
	ctx context.Context,
	subscriptionId string,
) ([]Location, error) {
	var err error
	msg := "Retrieving locations..."
	m.console.ShowSpinner(ctx, msg, input.Step)
	defer m.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))

	return m.listLocations(ctx, subscriptionId)
}

func (m *account) listLocations(
	ctx context.Context,
	subscriptionId string,
) ([]Location, error) {
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	return m.ListSubscriptionLocations(ctx, subscriptionId, tenantId)
}

func (m *account) getSubscriptionImpl(ctx context.Context, subscriptionId string) (*Subscription, error) {
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	azSub, err := m.getSubscription(ctx, subscriptionId, tenantId)
	if err != nil {
		return nil, err
	}

	sub := toSubscription(*azSub, tenantId)
	return &sub, nil
}

func toSubscriptions(azSubs []*armsubscriptions.Subscription, userAccessTenantId string) []Subscription {
	if azSubs == nil {
		return nil
	}

	subs := make([]Subscription, 0, len(azSubs))
	for _, azSub := range azSubs {
		subs = append(subs, toSubscription(*azSub, userAccessTenantId))
	}
	return subs
}

func toSubscription(subscription armsubscriptions.Subscription, userAccessTenantId string) Subscription {
	return Subscription{
		Id:                 *subscription.SubscriptionID,
		Name:               convert.ToValueWithDefault(subscription.DisplayName, *subscription.SubscriptionID),
		TenantId:           *subscription.TenantID,
		UserAccessTenantId: userAccessTenantId,
	}
}
