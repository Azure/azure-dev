// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const subscriptionFiltersConfigKey = "subscriptionFilters"

// subscriptionFilterConfigPath returns the user config path for a tenant's subscription filter.
func subscriptionFilterConfigPath(tenantId string) string {
	return fmt.Sprintf("%s.%s", subscriptionFiltersConfigKey, tenantId)
}

// LoadSubscriptionFilter loads the saved subscription filter for the given tenant
// from user config. Returns the list of subscription IDs and whether a filter exists.
func LoadSubscriptionFilter(
	cfg config.Config,
	tenantId string,
) ([]string, bool) {
	if tenantId == "" {
		return nil, false
	}

	raw, exists := cfg.GetSlice(subscriptionFilterConfigPath(tenantId))
	if !exists || len(raw) == 0 {
		return nil, false
	}

	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}

	if len(ids) == 0 {
		return nil, false
	}

	return ids, true
}

// SaveSubscriptionFilter saves a subscription filter for the given tenant
// to user config. Returns an error if tenantId is empty.
func SaveSubscriptionFilter(
	cfg config.Config,
	tenantId string,
	subscriptionIds []string,
) error {
	if tenantId == "" {
		return fmt.Errorf("tenantId must not be empty")
	}

	// Convert to []any for config storage compatibility
	values := make([]any, len(subscriptionIds))
	for i, id := range subscriptionIds {
		values[i] = id
	}
	return cfg.Set(subscriptionFilterConfigPath(tenantId), values)
}

// RemoveSubscriptionFilter removes the subscription filter for the given tenant
// from user config. Returns an error if tenantId is empty.
func RemoveSubscriptionFilter(cfg config.Config, tenantId string) error {
	if tenantId == "" {
		return fmt.Errorf("tenantId must not be empty")
	}
	return cfg.Unset(subscriptionFilterConfigPath(tenantId))
}

// ApplySubscriptionFilter filters subscriptions to only those whose IDs are in
// the given filter list. Returns the filtered list and true if a filter was applied.
// If filterIds is nil or empty, returns the original list unchanged.
func ApplySubscriptionFilter(
	subscriptions []account.Subscription,
	filterIds []string,
) ([]account.Subscription, bool) {
	if len(filterIds) == 0 {
		return subscriptions, false
	}

	filterSet := make(map[string]bool, len(filterIds))
	for _, id := range filterIds {
		filterSet[strings.ToLower(id)] = true
	}

	filtered := make([]account.Subscription, 0, len(filterIds))
	for _, sub := range subscriptions {
		if filterSet[strings.ToLower(sub.Id)] {
			filtered = append(filtered, sub)
		}
	}

	// If filter produced no matches (stale filter), return all subscriptions
	if len(filtered) == 0 {
		return subscriptions, false
	}

	return filtered, true
}

// GetAllSubscriptionFilters returns all saved subscription filters from user config
// as a map of tenantId -> []subscriptionId.
func GetAllSubscriptionFilters(
	cfg config.Config,
) map[string][]string {
	raw, exists := cfg.GetMap(subscriptionFiltersConfigKey)
	if !exists {
		return nil
	}

	result := make(map[string][]string, len(raw))
	for tenantId, value := range raw {
		if arr, ok := value.([]any); ok {
			ids := make([]string, 0, len(arr))
			for _, v := range arr {
				if s, ok := v.(string); ok && s != "" {
					ids = append(ids, s)
				}
			}
			if len(ids) > 0 {
				result[tenantId] = ids
			}
		}
	}

	return result
}

// SubscriptionsMatchingFilter returns the subscriptions that match saved filter IDs,
// preserving the order of the subscription list. Pre-selected items are returned
// as their display option strings for use with MultiSelect DefaultValue.
func SubscriptionsMatchingFilter(
	subscriptions []account.Subscription,
	filterIds []string,
	displayFn func(*account.Subscription) string,
) []string {
	if len(filterIds) == 0 {
		return nil
	}

	filterSet := make(map[string]bool, len(filterIds))
	for _, id := range filterIds {
		filterSet[strings.ToLower(id)] = true
	}

	var preSelected []string
	for i := range subscriptions {
		if filterSet[strings.ToLower(subscriptions[i].Id)] {
			preSelected = append(preSelected, displayFn(&subscriptions[i]))
		}
	}

	return preSelected
}

// resolveSelectedTenantId determines the effective tenant ID for a subscription.
// Prefers UserAccessTenantId, falls back to TenantId.
func resolveSelectedTenantId(sub *account.Subscription) string {
	if sub.UserAccessTenantId != "" {
		return sub.UserAccessTenantId
	}
	return sub.TenantId
}

// FilteredSubscriptionNote is the message shown when a subscription filter is active.
const FilteredSubscriptionNote = "Using saved subscription filter." +
	" Run 'azd config sub-filter set' to update."

// ShowAllSubscriptionsOption is the sentinel option text appended to filtered lists.
const ShowAllSubscriptionsOption = "Show all subscriptions"

// subscriptionIdsByOptions maps option display strings back to subscription IDs.
// This is used after MultiSelect to determine which subscriptions were selected.
func subscriptionIdsByOptions(
	subscriptions []account.Subscription,
	displayFn func(*account.Subscription) string,
	selectedOptions []string,
) []string {
	optionToId := make(map[string]string, len(subscriptions))
	for i := range subscriptions {
		optionToId[displayFn(&subscriptions[i])] = subscriptions[i].Id
	}

	ids := make([]string, 0, len(selectedOptions))
	for _, opt := range selectedOptions {
		if id, ok := optionToId[opt]; ok {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)
	return ids
}
