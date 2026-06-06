// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// tenantInfo holds display metadata for a tenant extracted from the subscription list.
type tenantInfo struct {
	// Id is the tenant ID (GUID).
	Id string
	// DisplayName is the friendly name of the tenant, or the ID if no name is available.
	DisplayName string
	// SubscriptionCount is the number of subscriptions accessible via this tenant.
	SubscriptionCount int
}

// extractUniqueTenants extracts unique tenants from a list of subscriptions,
// grouped by UserAccessTenantId (falling back to TenantId when UserAccessTenantId is empty).
// The returned list is sorted by DisplayName.
// Tenant display names are resolved from the provided tenantDisplayNames map;
// if a tenant ID is not in the map, the ID itself is used as the display name.
func extractUniqueTenants(
	subscriptions []account.Subscription,
	tenantDisplayNames map[string]string,
) []tenantInfo {
	tenantMap := make(map[string]*tenantInfo)

	for _, sub := range subscriptions {
		tid := sub.UserAccessTenantId
		if tid == "" {
			tid = sub.TenantId
		}
		if tid == "" {
			continue
		}

		if info, exists := tenantMap[tid]; exists {
			info.SubscriptionCount++
		} else {
			displayName := tid
			if name, ok := tenantDisplayNames[tid]; ok && name != "" {
				displayName = name
			}
			tenantMap[tid] = &tenantInfo{
				Id:                tid,
				DisplayName:       displayName,
				SubscriptionCount: 1,
			}
		}
	}

	tenants := make([]tenantInfo, 0, len(tenantMap))
	for _, info := range tenantMap {
		tenants = append(tenants, *info)
	}

	slices.SortFunc(tenants, func(a, b tenantInfo) int {
		if c := cmp.Compare(
			strings.ToLower(a.DisplayName),
			strings.ToLower(b.DisplayName),
		); c != 0 {
			return c
		}
		return cmp.Compare(a.Id, b.Id)
	})

	return tenants
}

// filterSubscriptionsByTenant filters subscriptions to only those accessible
// through the specified tenant ID. If tenantId is empty, all subscriptions are returned.
func filterSubscriptionsByTenant(
	subscriptions []account.Subscription,
	tenantId string,
) []account.Subscription {
	if tenantId == "" {
		return subscriptions
	}

	filtered := make([]account.Subscription, 0, len(subscriptions))
	for _, sub := range subscriptions {
		accessTenant := sub.UserAccessTenantId
		if accessTenant == "" {
			accessTenant = sub.TenantId
		}
		if accessTenant == tenantId {
			filtered = append(filtered, sub)
		}
	}
	return filtered
}

// filterByTenantEnvVar filters subscriptions by AZURE_TENANT_ID if set.
// This is applied in both prompt and no-prompt modes.
// If the env var is set but no subscriptions match (e.g. stale tenant ID),
// the filter is a no-op and returns all subscriptions to avoid blocking the user.
func filterByTenantEnvVar(subscriptions []account.Subscription) []account.Subscription {
	tenantId := os.Getenv(environment.TenantIdEnvVarName)
	if tenantId == "" {
		return subscriptions
	}

	filtered := filterSubscriptionsByTenant(subscriptions, tenantId)
	// If filtering produces no results, fall back to showing all subscriptions
	// rather than erroring out — the tenant ID may be stale
	if len(filtered) == 0 {
		log.Println("AZURE_TENANT_ID did not match any subscription tenants, showing all subscriptions")
		return subscriptions
	}

	return filtered
}

// promptTenantSelection prompts the user to select a tenant when multiple tenants are available.
// Returns the selected tenant ID, or empty string if the user chose "All tenants".
// If there is only one tenant, it is returned automatically without prompting.
func promptTenantSelection(
	ctx context.Context,
	console input.Console,
	tenants []tenantInfo,
) (string, error) {
	if len(tenants) <= 1 {
		if len(tenants) == 1 {
			return tenants[0].Id, nil
		}
		return "", nil
	}

	allTenantsLabel := fmt.Sprintf(
		"%2d. All tenants",
		len(tenants)+1,
	)

	options := make([]string, len(tenants)+1)
	for i, t := range tenants {
		options[i] = formatTenantOption(i+1, t)
	}
	options[len(tenants)] = allTenantsLabel

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Select a tenant",
		Options: options,
	})
	if err != nil {
		return "", fmt.Errorf("selecting tenant: %w", err)
	}

	// Last option = "All tenants"
	if selectedIndex == len(tenants) {
		return "", nil
	}

	return tenants[selectedIndex].Id, nil
}

// TenantDisplayNameProvider is a function that fetches tenant display names.
type TenantDisplayNameProvider func(ctx context.Context) (map[string]string, error)

// promptAndFilterByTenant prompts the user to select a tenant when subscriptions span multiple tenants.
// It extracts unique tenants, fetches display names only when needed, and returns filtered subscriptions.
func promptAndFilterByTenant(
	ctx context.Context,
	console input.Console,
	subscriptions []account.Subscription,
	getTenantNames TenantDisplayNameProvider,
) ([]account.Subscription, error) {
	// Quick check without display names to avoid unnecessary API call
	tenants := extractUniqueTenants(subscriptions, nil)
	if len(tenants) <= 1 {
		return subscriptions, nil
	}

	// Only fetch tenant display names when we actually need to prompt
	var tenantNames map[string]string
	if getTenantNames != nil {
		var err error
		tenantNames, err = getTenantNames(ctx)
		if err != nil {
			log.Printf("failed to fetch tenant display names: %v", err)
			tenantNames = nil
		}
	}

	tenants = extractUniqueTenants(subscriptions, tenantNames)

	selectedTenantId, err := promptTenantSelection(ctx, console, tenants)
	if err != nil {
		return nil, err
	}

	return filterSubscriptionsByTenant(subscriptions, selectedTenantId), nil
}

func formatTenantOption(index int, t tenantInfo) string {
	subCountLabel := fmt.Sprintf(
		"%d subscription", t.SubscriptionCount,
	)
	if t.SubscriptionCount != 1 {
		subCountLabel += "s"
	}

	return fmt.Sprintf(
		"%2d. %s %s",
		index,
		t.DisplayName,
		output.WithGrayFormat("(%s)", subCountLabel),
	)
}
