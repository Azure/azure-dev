// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armcompute armstorage kubelet

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type sdkResourceChecker struct {
	credential  azcore.TokenCredential
	options     *arm.ClientOptions
	readClients *resourceClients
	reads       *AuthorizationEvidence
}

// NewResourceChecker constructs read-only ARM adapters using an already resolved user-access-tenant credential.
func NewResourceChecker(
	credential azcore.TokenCredential, options *arm.ClientOptions, evidence *AuthorizationEvidence,
) ResourceChecker {
	return &sdkResourceChecker{credential: credential, options: onboardingARMOptions(options), reads: evidence}
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
	if raw != strings.TrimSpace(raw) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return ParseID(raw, kind)
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
	c := resourceCheck("resource-refs", scope, "referenced resource ID matches and provisioningState=Succeeded")
	if returnedID == "" || state == "" {
		return c
	}
	return resourceKnown(c, true, strings.EqualFold(scope, returnedID) && state == "Succeeded",
		"resource ID matches="+fmt.Sprint(strings.EqualFold(scope, returnedID))+"; provisioningState="+state+"; "+action)
}

func (c *sdkResourceChecker) subscriptionTenants(ctx context.Context, plan Plan) (string, Report) {
	report := Report{}
	subscriptions := map[string]bool{}
	for _, subscription := range plan.OwningSubscriptions {
		subscriptions[strings.ToLower(subscription)] = true
	}
	if plan.SubscriptionID != "" && !strings.ContainsAny(plan.SubscriptionID, "/\\?#% ") {
		subscriptions[strings.ToLower(plan.SubscriptionID)] = true
	} else {
		report = append(report, resourceResult(resourceCheck("resource-refs", plan.SubscriptionID,
			"valid target subscription ID"), Fail, "target subscription is absent or malformed"))
	}
	for _, item := range []struct {
		resource Resource
		kind     string
	}{
		{plan.Cluster, "Microsoft.ContainerService/managedClusters"},
		{plan.Storage, "Microsoft.Storage/storageAccounts"},
		{plan.Hosting, "Microsoft.ManagedIdentity/userAssignedIdentities"},
		{plan.Workload, "Microsoft.ManagedIdentity/userAssignedIdentities"},
	} {
		if item.resource.Missing != "" {
			if parent, err := arm.ParseResourceID(item.resource.Parent); err == nil && parent.SubscriptionID != "" {
				subscriptions[strings.ToLower(parent.SubscriptionID)] = true
			} else {
				report = append(report, SkippedCheck("resource-refs", item.resource.diagnosticScope(),
					"resource subscription owning tenant", item.resource.Missing))
			}
			continue
		}
		id, err := ParseID(item.resource.ID, item.kind)
		if err != nil {
			check := resourceResult(resourceCheck("resource-refs", item.resource.ID, item.kind), Fail,
				"malformed supplied resource ID")
			check.Cause = err
			report = append(report, check)
			continue
		}
		subscriptions[strings.ToLower(id.SubscriptionID)] = true
	}
	_, err := c.reads.armClient()
	if err != nil {
		return "", append(report, readFailure("resource-refs", plan.SubscriptionID, "subscriptions/read", err))
	}
	keys := slices.Sorted(maps.Keys(subscriptions))
	checks := make(Report, len(keys))
	tenants := make([]string, len(keys))
	tasks := make([]func(), 0, len(keys))
	for i, subscription := range keys {
		tasks = append(tasks, func() {
			tenants[i], checks[i] = readSubscriptionTenant(ctx, c.reads, subscription)
		})
	}
	parallelReads(tasks...)
	var tenant string
	for i, check := range checks {
		if tenants[i] != "" {
			if tenant == "" {
				tenant = tenants[i]
			}
			check = resourceKnown(check, true, strings.EqualFold(tenant, tenants[i]),
				"subscription owning tenantId="+tenants[i])
		}
		report = append(report, check)
	}
	if slices.ContainsFunc(report, Check.blocks) {
		tenant = ""
	}
	return tenant, report
}

func readSubscriptionTenant(ctx context.Context, reader *AuthorizationEvidence, subscription string) (string, Check) {
	scope := "/subscriptions/" + subscription
	response, err := reader.GetSubscription(ctx, subscription)
	if err != nil {
		return "", readFailure("resource-refs", scope, "Microsoft.Resources/subscriptions/read", err)
	}
	var result struct {
		TenantID string `json:"tenantId"`
	}
	err = runtime.UnmarshalAsJSON(response, &result)
	if err != nil {
		return "", readFailure("resource-refs", scope, "top-level subscription tenantId", err)
	}
	check := resourceCheck("resource-refs", scope,
		"all referenced and target subscriptions have the same top-level tenantId")
	if result.TenantID == "" {
		check.Observed = "subscription response has no top-level tenantId; managedByTenants is not ownership"
	}
	return result.TenantID, check
}

func (c *sdkResourceChecker) Check(ctx context.Context, plan Plan) (Facts, Report, error) {
	local := *c
	local.readClients = &resourceClients{}
	if local.reads == nil {
		local.reads = NewAuthorizationEvidence(c.credential, c.options)
	}
	c = &local
	tenant, report := c.subscriptionTenants(ctx, plan)
	report = append(slices.Clone(plan.SkippedChecks), report...)
	facts := Facts{}
	var identities [2]Identity
	var identityReports [2]Check
	var storage *armstorage.Account
	var storageReport Report
	var cluster *armcontainerservice.ManagedCluster
	var pools []*armcontainerservice.AgentPool
	var poolsKnown bool
	var clusterReport Report
	var selected *armcontainerservice.AgentPool
	tasks := []func(){func() {
		cluster, pools, poolsKnown, clusterReport = c.readCluster(ctx, plan.Cluster)
	}}
	if plan.Storage.Supplied {
		tasks = append(tasks, func() {
			storage, storageReport = c.readStorage(ctx, plan.Storage)
		})
	}
	if plan.Hosting.Supplied || plan.Hosting.PrincipalRequired {
		tasks = append(tasks, func() {
			identities[0], identityReports[0] = c.readIdentity(ctx, plan.Hosting, tenant)
		})
	}
	if plan.Workload.Supplied || plan.Workload.PrincipalRequired {
		tasks = append(tasks, func() {
			identities[1], identityReports[1] = c.readIdentity(ctx, plan.Workload, tenant)
		})
	}
	parallelReads(tasks...)
	facts.HostingIdentity, facts.WorkloadIdentity = identities[0], identities[1]
	for _, check := range identityReports {
		if check.ID != "" {
			report = append(report, check)
		}
	}
	report = append(report, storageReport...)
	report = append(report, clusterReport...)
	if cluster != nil {
		kubelet, checks, pool := aksResourceChecks(*cluster, pools, poolsKnown)
		selected = pool
		c.poolCapacityWarnings(ctx, *cluster, pools, selected, checks)
		facts.KubeletObjectID = kubelet
		report = append(report, checks...)
	} else {
		for _, id := range aksCheckIDs {
			if plan.Cluster.Missing != "" {
				report = append(report, SkippedCheck(id, plan.Cluster.diagnosticScope(),
					"AKS prerequisite configuration", plan.Cluster.Missing))
			} else {
				report = append(report, resourceCheck(id, plan.Cluster.ID, "readable AKS metadata"))
			}
		}
	}
	if missing := plan.Storage.Missing + plan.Storage.LifecycleMissing; missing != "" {
		for _, id := range []string{"storage-type", "storage-security"} {
			report = append(report, SkippedCheck(id, plan.Storage.diagnosticScope(), "Storage configuration", missing))
		}
	} else if plan.Storage.Supplied {
		if storage != nil {
			report = append(report, storageResourceChecks(*storage)...)
		} else {
			for _, id := range []string{"storage-type", "storage-security"} {
				report = append(report, resourceCheck(id, plan.Storage.ID, "readable supplied Storage account"))
			}
		}
	} else {
		for _, id := range []string{"storage-type", "storage-security"} {
			report = append(report, resourceResult(resourceCheck(id, plan.Storage.Parent, "supplied Storage"),
				NotApplicable, "Storage uses the default ARM creation/update path"))
		}
	}
	report = append(report, c.networkChecks(ctx, plan, cluster, selected)...)
	if err := ctx.Err(); err != nil {
		return facts, report, err
	}
	return facts, report, report.Err()
}

func (c *sdkResourceChecker) readCluster(
	ctx context.Context, resource Resource,
) (*armcontainerservice.ManagedCluster, []*armcontainerservice.AgentPool, bool, Report) {
	if resource.Missing != "" {
		return nil, nil, false, Report{SkippedCheck("resource-refs", resource.diagnosticScope(),
			"AKS resource existence and identity", resource.Missing)}
	}
	id, err := existingResourceID(resource.ID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return nil, nil, false, Report{readFailure("resource-refs", resource.ID, "complete managedClusters resource ID", err)}
	}
	client, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, nil, false, Report{readFailure("resource-refs", resource.ID, "managedClusters/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, nil, false, Report{readFailure("resource-refs", resource.ID,
			"Microsoft.ContainerService/managedClusters/read", err)}
	}
	report := Report{resourceKnown(resourceCheck("resource-refs", resource.ID, "referenced AKS resource ID matches"),
		response.ID != nil, strings.EqualFold(resource.ID, resourceValue(response.ID)),
		"returned AKS resource ID="+resourceValue(response.ID))}
	pools, poolErr := c.readAgentPools(ctx, id)
	if poolErr != nil {
		report = append(report, readFailure("resource-refs", resource.ID,
			"Microsoft.ContainerService/managedClusters/agentPools/read", poolErr))
	}
	return &response.ManagedCluster, pools, poolErr == nil, report
}

func (c *sdkResourceChecker) readAgentPools(
	ctx context.Context, id *arm.ResourceID,
) ([]*armcontainerservice.AgentPool, error) {
	client, err := armcontainerservice.NewAgentPoolsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, err
	}
	var pools []*armcontainerservice.AgentPool
	pager := client.NewListPager(id.ResourceGroupName, id.Name, nil)
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			// Partial enumeration cannot establish the required pool configuration.
			return nil, pageErr
		}
		pools = append(pools, page.Value...)
	}
	return pools, nil
}

func (c *sdkResourceChecker) poolCapacityWarnings(
	ctx context.Context, cluster armcontainerservice.ManagedCluster, pools []*armcontainerservice.AgentPool,
	selected *armcontainerservice.AgentPool, report Report,
) {
	var candidates []*armcontainerservice.AgentPool
	for _, pool := range pools {
		if pool != nil && pool.Properties != nil &&
			(resourceValue(pool.Properties.Mode) == "System" || pool == selected) {
			candidates = append(candidates, pool)
		}
	}
	if len(candidates) == 0 {
		return
	}
	id, err := existingResourceID(resourceValue(cluster.ID), "Microsoft.ContainerService/managedClusters")
	var skus []*armcompute.ResourceSKU
	if err == nil {
		skus, err = c.readSKUs(ctx, id.SubscriptionID, resourceValue(cluster.Location))
	}
	warnings := map[string][]string{}
	for _, pool := range candidates {
		checkID, cpu, memory := "system-pool", 4.0, 16.0
		if pool == selected {
			checkID, cpu, memory = "agent-pool", 8, 32
		}
		warning := ""
		if err != nil {
			warning = fmt.Sprintf(
				"Pool %s capacity could not be verified: %s. At least %g available vCPU / %g GiB per VM is recommended; "+
					"this recommendation does not block provisioning or change the pool.",
				resourceValue(pool.Name), err, cpu, memory)
		} else {
			warning = poolCapacityWarning(pool, skus, resourceValue(cluster.Location), cpu, memory)
		}
		if warning != "" {
			warnings[checkID] = append(warnings[checkID], warning)
		}
	}
	for i := range report {
		if messages := warnings[report[i].ID]; len(messages) > 0 {
			report[i].Warning = strings.Join(messages, " ")
		}
	}
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

func (c *sdkResourceChecker) readStorage(
	ctx context.Context, resource Resource,
) (*armstorage.Account, Report) {
	if resource.Missing != "" {
		return nil, Report{SkippedCheck("resource-refs", resource.diagnosticScope(),
			"Storage resource existence and identity", resource.Missing)}
	}
	id, err := ParseID(resource.ID, "Microsoft.Storage/storageAccounts")
	if err != nil {
		return nil, Report{readFailure("resource-refs", resource.ID, "valid Storage resource ID", err)}
	}
	client, err := armstorage.NewAccountsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, Report{readFailure("resource-refs", resource.ID, "Microsoft.Storage/storageAccounts/read", err)}
	}
	response, err := client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure("resource-refs", resource.ID, "Microsoft.Storage/storageAccounts/read", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	return &response.Account, Report{
		resourceReadiness(resource.ID, resourceValue(response.ID), state, "storageAccounts/read"),
	}
}

func (c *sdkResourceChecker) readIdentity(
	ctx context.Context, resource Resource, tenant string,
) (Identity, Check) {
	if resource.Missing != "" {
		return Identity{}, SkippedCheck("resource-refs", resource.diagnosticScope(),
			"managed identity existence and metadata", resource.Missing)
	}
	check := resourceCheck("resource-refs", resource.ID,
		"existing user-assigned identity with matching ID, principal, client and tenant")
	id, err := ParseID(resource.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
	if err != nil {
		check.Status, check.Observed, check.Cause = Fail, "invalid identity resource ID", err
		return Identity{}, check
	}
	client, err := c.clients().identities.get(strings.ToLower(id.SubscriptionID),
		func() (*armmsi.UserAssignedIdentitiesClient, error) {
			return armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, c.credential, c.options)
		})
	if err != nil {
		check.Cause = err
		return Identity{}, check
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return Identity{}, readFailure("resource-refs", resource.ID,
			"Microsoft.ManagedIdentity/userAssignedIdentities/read", err)
	}
	properties := response.Properties
	if response.ID == nil || properties == nil || authString(properties.PrincipalID) == "" ||
		authString(properties.ClientID) == "" || authString(properties.TenantID) == "" || tenant == "" {
		check.Observed = "identity ID, principal, client or owning tenant evidence is missing"
		return Identity{}, check
	}
	identity := Identity{ID: *response.ID, PrincipalID: *properties.PrincipalID}
	if !strings.EqualFold(resource.ID, identity.ID) || !strings.EqualFold(tenant, *properties.TenantID) {
		return Identity{}, resourceResult(check, Fail, "identity ID or owning tenant does not match")
	}
	return identity, resourceResult(check, Pass, "existing identity metadata matches")
}
