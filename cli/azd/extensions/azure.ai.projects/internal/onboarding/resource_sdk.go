// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type sdkResourceChecker struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
}

// NewResourceChecker constructs typed, read-only ARM adapters using an already resolved user-access-tenant credential.
func NewResourceChecker(credential azcore.TokenCredential, options *arm.ClientOptions) ResourceChecker {
	return &sdkResourceChecker{credential: credential, options: onboardingARMOptions(options)}
}

func onboardingARMOptions(options *arm.ClientOptions) *arm.ClientOptions {
	var local arm.ClientOptions
	if options != nil {
		local = *options
	}
	local.DisableRPRegistration = true
	return &local
}

func existingResourceID(raw, kind string) (*arm.ResourceID, error) {
	if !validResourceIDSyntax(raw) {
		return nil, fmt.Errorf("expected a complete %s resource ID without URL credentials or extra components", kind)
	}
	id, err := arm.ParseResourceID(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid resource ID: %w", err)
	}
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "?#%\\") ||
		id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), kind) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	expected := 8
	if strings.EqualFold(kind, "Microsoft.Network/virtualNetworks/subnets") {
		expected = 10
	}
	if !strings.HasPrefix(raw, "/") || len(parts) != expected || slices.Contains(parts, "") {
		return nil, fmt.Errorf("expected a complete %s resource ID without extra segments", kind)
	}
	return id, nil
}

func readFailure(id, scope, action string, err error) Check {
	c := resourceCheck(id, scope, action)
	c.Cause = err
	c.Missing = action + " at " + scope
	c.Observed = "resource could not be read; inspect the original service error"
	c.Remediation = "allow the stated read operation and retry; do not replace an unreadable resource"
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if response.StatusCode == http.StatusNotFound {
			c.Status, c.Observed = Fail, "referenced resource does not exist (HTTP 404)"
		} else {
			c.Observed = fmt.Sprintf("resource read failed (HTTP %d; code %s)", response.StatusCode, response.ErrorCode)
		}
	}
	return c
}

func resourceReadiness(scope, returnedID, state, action string) Check {
	c := resourceCheck("I02", scope, "referenced resource ID matches and provisioningState=Succeeded")
	if returnedID == "" || state == "" {
		return c
	}
	return resourceKnown(c, true, strings.EqualFold(scope, returnedID) && state == "Succeeded",
		"resource ID matches="+fmt.Sprint(strings.EqualFold(scope, returnedID))+"; provisioningState="+state+"; "+action)
}

func (c *sdkResourceChecker) subscriptionTenants(ctx context.Context, plan Plan) (string, Report) {
	report := Report{}
	subscriptions := map[string]bool{}
	if plan.SubscriptionID != "" && !strings.ContainsAny(plan.SubscriptionID, "/\\?#% ") {
		subscriptions[strings.ToLower(plan.SubscriptionID)] = true
	} else {
		report = append(report, resourceResult(resourceCheck("I02", plan.SubscriptionID,
			"valid target subscription ID"), Fail, "target subscription is absent or malformed"))
	}
	for _, item := range []struct {
		resource Resource
		kind     string
	}{
		{plan.Cluster, "Microsoft.ContainerService/managedClusters"},
		{plan.Storage, "Microsoft.Storage/storageAccounts"},
		{plan.Registry, "Microsoft.ContainerRegistry/registries"},
		{plan.Hosting, "Microsoft.ManagedIdentity/userAssignedIdentities"},
		{plan.Workload, "Microsoft.ManagedIdentity/userAssignedIdentities"},
	} {
		if !item.resource.Supplied && item.kind != "Microsoft.ContainerService/managedClusters" {
			continue
		}
		id, err := existingResourceID(item.resource.ID, item.kind)
		if err != nil {
			check := resourceResult(resourceCheck("I02", item.resource.ID, item.kind), Fail,
				"malformed supplied resource ID")
			check.Cause = err
			report = append(report, check)
			continue
		}
		subscriptions[strings.ToLower(id.SubscriptionID)] = true
	}
	if network := plan.LegacyNetwork; network != nil && network.Enabled {
		if network.DNSResourceGroup != "" && network.DNSSubscription != "" {
			subscriptions[strings.ToLower(network.DNSSubscription)] = true
		}
	}
	client, err := arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", c.credential, c.options)
	if err != nil {
		return "", append(report, readFailure("I02", plan.SubscriptionID, "subscriptions/read", err))
	}
	var tenant string
	keys := make([]string, 0, len(subscriptions))
	for subscription := range subscriptions {
		keys = append(keys, subscription)
	}
	slices.Sort(keys)
	for _, subscription := range keys {
		scope := "/subscriptions/" + subscription
		request, err := runtime.NewRequest(ctx, http.MethodGet,
			strings.TrimRight(client.Endpoint(), "/")+scope+"?api-version=2022-12-01")
		if err != nil {
			report = append(report, readFailure("I02", scope, "Microsoft.Resources/subscriptions/read", err))
			continue
		}
		response, err := client.Pipeline().Do(request)
		if err == nil && !runtime.HasStatusCode(response, http.StatusOK) {
			err = runtime.NewResponseError(response)
		}
		if err != nil {
			report = append(report, readFailure("I02", scope, "Microsoft.Resources/subscriptions/read", err))
			continue
		}
		var result struct {
			TenantID string `json:"tenantId"`
		}
		err = runtime.UnmarshalAsJSON(response, &result)
		if err != nil {
			report = append(report, readFailure("I02", scope, "top-level subscription tenantId", err))
			continue
		}
		check := resourceCheck("I02", scope, "all referenced and target subscriptions have the same top-level tenantId")
		if result.TenantID == "" {
			check.Observed = "subscription response has no top-level tenantId; managedByTenants is not ownership"
		} else {
			if tenant == "" {
				tenant = result.TenantID
			}
			check = resourceKnown(check, true, strings.EqualFold(tenant, result.TenantID),
				"subscription owning tenantId="+result.TenantID)
		}
		report = append(report, check)
	}
	if report.Err() != nil {
		tenant = ""
	}
	return tenant, report
}

func (c *sdkResourceChecker) Check(ctx context.Context, plan Plan) (Facts, Report, error) {
	tenant, report := c.subscriptionTenants(ctx, plan)
	input := resourceCheck("I01", plan.Cluster.ID, "complete nonempty managedClusters resource ID")
	if _, err := existingResourceID(plan.Cluster.ID, "Microsoft.ContainerService/managedClusters"); err != nil {
		input = resourceResult(input, Fail, "cluster resource ID is malformed")
		input.Cause = err
	} else {
		input = resourceResult(input, Pass, "cluster resource ID is valid; no replacement cluster will be created")
	}
	report = append(report, input)
	facts := Facts{TenantID: tenant}
	for _, item := range []struct {
		resource Resource
		identity *Identity
	}{
		{plan.Hosting, &facts.HostingIdentity}, {plan.Workload, &facts.WorkloadIdentity},
	} {
		if !item.resource.Supplied {
			continue
		}
		identity, check := c.readSuppliedIdentity(ctx, item.resource, tenant)
		*item.identity = identity
		report = append(report, check)
	}
	planCheck := resourceCheck("I03", plan.AgentSubnet.ID, "resolved supported creation/reuse and companion topology")
	if plan.Network != nil {
		planCheck.Observed = "automatic hosting subnet plans are no longer supported"
		planCheck.Remediation = "retain any old ownership journal for cleanup; select networking through the standard network block"
	} else {
		planCheck = resourceResult(planCheck, Pass,
			"standard network selection retained; Storage/ACR create/reuse resolved")
	}
	report = append(report, planCheck)
	cluster, pools, skus, clusterReport := c.readCluster(ctx, plan.Cluster)
	report = append(report, clusterReport...)
	if cluster != nil {
		kubelet, checks := aksResourceChecks(*cluster, pools, skus)
		facts.KubeletObjectID = kubelet
		report = append(report, checks...)
	} else {
		for i := 1; i <= 11; i++ {
			report = append(report, resourceCheck(fmt.Sprintf("A%02d", i), plan.Cluster.ID, "readable AKS metadata"))
		}
	}
	var storage *armstorage.Account
	if plan.Storage.Supplied {
		var checks Report
		storage, checks = c.readStorage(ctx, plan.Storage)
		report = append(report, checks...)
		if storage != nil {
			report = append(report, storageResourceChecks(*storage)...)
		} else {
			for _, id := range []string{"S01", "S02"} {
				report = append(report, resourceCheck(id, plan.Storage.ID, "readable supplied Storage account"))
			}
		}
	} else {
		for _, id := range []string{"S01", "S02"} {
			report = append(report, resourceResult(resourceCheck(id, plan.Storage.Parent, "supplied Storage"),
				NotApplicable, "Storage will be created after preflight"))
		}
	}
	if plan.Registry.Supplied {
		abac, checks := c.readRegistry(ctx, plan.Registry)
		facts.RegistryABAC = abac
		report = append(report, checks...)
	} else {
		report = append(report, resourceResult(resourceCheck("C01", plan.Registry.Parent, "supplied ACR"),
			NotApplicable, "registry will be created after preflight"))
	}
	report = append(report, c.networkChecks(ctx, plan, cluster, pools, storage)...)
	if err := ctx.Err(); err != nil {
		return facts, report, err
	}
	return facts, report, report.Err()
}

func (c *sdkResourceChecker) readCluster(
	ctx context.Context, resource Resource,
) (*armcontainerservice.ManagedCluster, []*armcontainerservice.AgentPool, []*armcompute.ResourceSKU, Report) {
	id, err := existingResourceID(resource.ID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return nil, nil, nil, nil // local malformed-ID evidence is already recorded under I02
	}
	client, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, nil, nil, Report{readFailure("I02", resource.ID, "managedClusters/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, nil, nil, Report{readFailure("I02", resource.ID,
			"Microsoft.ContainerService/managedClusters/read", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	report := Report{resourceReadiness(resource.ID, resourceValue(response.ID), state, "managedClusters/read")}
	poolClient, err := armcontainerservice.NewAgentPoolsClient(id.SubscriptionID, c.credential, c.options)
	var pools []*armcontainerservice.AgentPool
	if err == nil {
		pager := poolClient.NewListPager(id.ResourceGroupName, id.Name, nil)
		for pager.More() {
			page, pageErr := pager.NextPage(ctx)
			if pageErr != nil {
				err = pageErr
				break
			}
			pools = append(pools, page.Value...)
		}
	}
	if err != nil {
		report = append(report, readFailure("A10", resource.ID,
			"Microsoft.ContainerService/managedClusters/agentPools/read", err))
		// A partially enumerated pool set cannot establish the required pool configuration.
		pools = nil
	}
	skus, skuErr := c.readSKUs(ctx, id.SubscriptionID, resourceValue(response.Location))
	if skuErr != nil {
		report = append(report, readFailure("A05", "/subscriptions/"+id.SubscriptionID,
			"Microsoft.Compute/skus/read", skuErr))
	}
	return &response.ManagedCluster, pools, skus, report
}

func (c *sdkResourceChecker) readSKUs(
	ctx context.Context, subscription, location string,
) ([]*armcompute.ResourceSKU, error) {
	client, err := armcompute.NewResourceSKUsClient(subscription, c.credential, c.options)
	if err != nil {
		return nil, err
	}
	filter := "location eq '" + strings.ReplaceAll(location, "'", "''") + "'"
	pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{Filter: &filter})
	var result []*armcompute.ResourceSKU
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, page.Value...)
	}
	return result, nil
}

func (c *sdkResourceChecker) readStorage(ctx context.Context, resource Resource) (*armstorage.Account, Report) {
	id, err := existingResourceID(resource.ID, "Microsoft.Storage/storageAccounts")
	if err != nil {
		return nil, nil
	}
	client, err := armstorage.NewAccountsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, Report{readFailure("I02", resource.ID, "Microsoft.Storage/storageAccounts/read", err)}
	}
	response, err := client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure("I02", resource.ID, "Microsoft.Storage/storageAccounts/read", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	return &response.Account, Report{
		resourceReadiness(resource.ID, resourceValue(response.ID), state, "storageAccounts/read"),
	}
}

func (c *sdkResourceChecker) readRegistry(ctx context.Context, resource Resource) (bool, Report) {
	id, err := existingResourceID(resource.ID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return false, Report{resourceCheck("C01", resource.ID, "valid supplied registry ID")}
	}
	client, err := armcontainerregistry.NewRegistriesClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return false, Report{readFailure("C01", resource.ID, "Microsoft.ContainerRegistry/registries/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return false, Report{readFailure("C01", resource.ID, "Microsoft.ContainerRegistry/registries/read", err)}
	}
	abac, check := registryResourceCheck(response.Registry)
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	return abac, Report{resourceReadiness(resource.ID, resourceValue(response.ID), state, "registries/read"), check}
}

func safeResourceEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
