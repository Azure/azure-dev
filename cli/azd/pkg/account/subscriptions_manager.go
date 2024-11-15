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
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"go.uber.org/multierr"
)

// SubscriptionTenantResolver allows resolving the correct tenant ID
// that allows the current account access to a given subscription.
type SubscriptionTenantResolver interface {
	// Resolve the tenant ID required by the current account to access the given subscription.
	LookupTenant(ctx context.Context, subscriptionId string) (tenantId string, err error)
}

// Typically auth.Manager
type principalInfoProvider interface {
	GetLoggedInServicePrincipalTenantID(ctx context.Context) (*string, error)
	ClaimsForCurrentUser(ctx context.Context, options *auth.ClaimsForCurrentUserOptions) (auth.TokenClaims, error)
}

// Typically subscriptionsCache
type subCache interface {
	Load(ctx context.Context, key string) ([]Subscription, error)
	Save(ctx context.Context, key string, save []Subscription) error
	Clear(ctx context.Context) error
}

// SubscriptionsManager manages listing, storing and retrieving subscriptions for the current account.
//
// Since the application supports multi-tenancy, subscriptions can be accessed by the user through different tenants.
// To lookup access to a given subscription, LookupTenant can be used to lookup the
// current account's required tenantID to access a given subscription.
type SubscriptionsManager struct {
	service       *SubscriptionsService
	principalInfo principalInfoProvider
	cache         subCache
	console       input.Console
}

func NewSubscriptionsManager(
	service *SubscriptionsService,
	auth *auth.Manager,
	console input.Console) (*SubscriptionsManager, error) {
	cache, err := newSubCache()
	if err != nil {
		return nil, err
	}

	return &SubscriptionsManager{
		service:       service,
		cache:         cache,
		principalInfo: auth,
		console:       console,
	}, nil
}

// Clears stored cached subscriptions. This can only return an error if a filesystem error other than ErrNotExist occurred.
func (m *SubscriptionsManager) ClearSubscriptions(ctx context.Context) error {
	err := m.cache.Clear(ctx)
	if err != nil {
		return fmt.Errorf("clearing stored subscriptions: %w", err)
	}

	return nil
}

// Updates stored cached subscriptions.
func (m *SubscriptionsManager) RefreshSubscriptions(ctx context.Context) error {
	claims, err := m.principalInfo.ClaimsForCurrentUser(ctx, nil)
	if err != nil {
		return err
	}
	uid := claims.LocalAccountId()
	subs, err := m.ListSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("fetching subscriptions: %w", err)
	}

	err = m.cache.Save(ctx, uid, subs)
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
	principalTenantId, err := m.principalInfo.GetLoggedInServicePrincipalTenantID(ctx)
	if err != nil {
		return "", err
	}

	if principalTenantId != nil {
		return *principalTenantId, nil
	}

	res, err := m.getSubscriptions(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving user access to subscription '%s' : %w", subscriptionId, err)
	}

	for _, sub := range res.subscriptions {
		if sub.Id == subscriptionId {
			return sub.UserAccessTenantId, nil
		}
	}

	return "", fmt.Errorf(
		"failed to resolve user '%s' access to subscription with ID '%s'. "+
			"If you recently gained access to this subscription, run `azd auth login` again to reload subscriptions.\n"+
			"Otherwise, visit this subscription in Azure Portal using the browser, "+
			"then run `azd auth login` ",
		res.userClaims.DisplayUsername(),
		subscriptionId)
}

// GetSubscriptions retrieves subscriptions accessible by the current account with caching semantics.
//
// Unlike ListSubscriptions, GetSubscriptions first examines the subscriptions cache.
// On cache miss, subscriptions are fetched, the cached is updated, before the result is returned.
func (m *SubscriptionsManager) GetSubscriptions(ctx context.Context) ([]Subscription, error) {
	res, err := m.getSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	return res.subscriptions, nil
}

type getSubscriptionsResult struct {
	subscriptions []Subscription
	userClaims    auth.TokenClaims
}

func (m *SubscriptionsManager) getSubscriptions(ctx context.Context) (getSubscriptionsResult, error) {
	claims, err := m.principalInfo.ClaimsForCurrentUser(ctx, nil)
	if err != nil {
		return getSubscriptionsResult{}, err
	}
	uid := claims.LocalAccountId()

	subscriptions, err := m.cache.Load(ctx, uid)
	if err != nil {
		subscriptions, err = m.ListSubscriptions(ctx)
		if err != nil {
			return getSubscriptionsResult{}, fmt.Errorf("listing subscriptions: %w", err)
		}

		err = m.cache.Save(ctx, uid, subscriptions)
		if err != nil {
			return getSubscriptionsResult{}, fmt.Errorf("saving subscriptions to cache: %w", err)
		}
	}

	// When the integration test framework runs a test in playback mode, it sets AZD_DEBUG_SYNTHETIC_SUBSCRIPTION to the
	// ID of the subscription that was used when recording the test. We ensure this subscription is always present in the
	// list returned by `getSubscriptions` so that end to end tests can run successfully.
	if syntheticId := os.Getenv("AZD_DEBUG_SYNTHETIC_SUBSCRIPTION"); syntheticId != "" {
		found := false

		for _, sub := range subscriptions {
			if sub.Id == syntheticId {
				found = true
				break
			}
		}

		if !found {
			subscriptions = append(subscriptions, Subscription{
				Id:                 syntheticId,
				Name:               "AZD Synthetic Test Subscription",
				TenantId:           claims.TenantId,
				UserAccessTenantId: claims.TenantId,
			})
		}
	}

	return getSubscriptionsResult{
		subscriptions: subscriptions,
		userClaims:    claims,
	}, nil
}

func (m *SubscriptionsManager) GetSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
	subscriptions, err := m.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	for _, sub := range subscriptions {
		if sub.Id == subscriptionId {
			return &sub, nil
		}
	}
	return m.getSubscription(ctx, subscriptionId)
}

type tenantSubsResult struct {
	subs []Subscription
	err  error
}

// ListSubscription lists subscriptions accessible by the current account by calling azure management services.
func (m *SubscriptionsManager) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
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
		subscriptions, err := m.service.ListSubscriptions(ctx, *principalTenantId)
		if err != nil {
			return nil, err
		}

		tenantSubscriptions := []Subscription{}
		for _, subscription := range subscriptions {
			tenantSubscriptions = append(tenantSubscriptions, toSubscription(*subscription, *principalTenantId))
		}

		return tenantSubscriptions, nil
	}

	tenants, err := m.service.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}

	listForTenant := func(
		jobs <-chan armsubscriptions.TenantIDDescription,
		results chan<- tenantSubsResult,
		service *SubscriptionsService) {
		for tenant := range jobs {
			azSubs, err := service.ListSubscriptions(ctx, *tenant.TenantID)
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
	close(results)

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

func (m *SubscriptionsManager) GetLocation(ctx context.Context, subscriptionId, locationName string) (Location, error) {
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

func (m *SubscriptionsManager) ListLocations(
	ctx context.Context,
	subscriptionId string,
) ([]Location, error) {
	var err error
	msg := "Retrieving locations..."
	m.console.ShowSpinner(ctx, msg, input.Step)
	defer m.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))

	return m.listLocations(ctx, subscriptionId)
}

func (m *SubscriptionsManager) listLocations(
	ctx context.Context,
	subscriptionId string,
) ([]Location, error) {
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	return m.service.ListSubscriptionLocations(ctx, subscriptionId, tenantId)
}

func (m *SubscriptionsManager) getSubscription(ctx context.Context, subscriptionId string) (*Subscription, error) {
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
