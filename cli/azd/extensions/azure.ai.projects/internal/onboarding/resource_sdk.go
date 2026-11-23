// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armstorage kubelet

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type sdkResourceChecker struct {
	credential  azcore.TokenCredential
	options     *arm.ClientOptions
	readClients *resourceClients
	reads       *AuthorizationEvidence
	onProgress  func(string)
	progress    *resourceProgress
}

// NewResourceChecker constructs read-only ARM adapters using an already resolved user-access-tenant credential.
// The optional progress callback receives current activity, not check results.
func NewResourceChecker(
	credential azcore.TokenCredential, options *arm.ClientOptions, evidence *AuthorizationEvidence,
	progress func(string),
) ResourceChecker {
	return &sdkResourceChecker{
		credential: credential, options: onboardingARMOptions(options), reads: evidence, onProgress: progress,
	}
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

func readFailure(id, scope, resourceType, operation string, err error) Check {
	c := resourceCheck(id, scope, resourceType, operation+" could not be read")
	c.Cause = err
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if response.StatusCode == http.StatusNotFound {
			c = resourceResult(c, Fail, operation+" does not exist")
		} else if response.StatusCode == http.StatusForbidden {
			c.FailureReasons = []string{"Permission to read " + operation + " was denied"}
		} else if response.StatusCode == http.StatusUnauthorized {
			c.FailureReasons = []string{"Authentication is required to read " + operation}
		}
	}
	return c
}

func resourceReadiness(scope, returnedID, state, resourceType string) Check {
	c := resourceCheck("resource-refs", scope, resourceType)
	addFailureReason(&c, returnedID != "", strings.EqualFold(scope, returnedID),
		"Returned resource identity could not be verified", "Returned resource does not match the selected resource")
	addFailureReason(&c, state != "", state == "Succeeded",
		"Resource provisioning state could not be verified", "Resource provisioning has not succeeded")
	if returnedID == "" || state == "" {
		return c
	}
	return resourceKnown(c, true, strings.EqualFold(scope, returnedID) && state == "Succeeded")
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
			subscriptionResourceType), Fail, "Target subscription is absent or malformed"))
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
				report = append(report, SkippedCheck("resource-refs",
					item.resource.diagnosticScope(), subscriptionResourceType))
			}
			continue
		}
		id, err := ParseID(item.resource.ID, item.kind)
		if err != nil {
			check := resourceResult(resourceCheck("resource-refs", item.resource.ID, subscriptionResourceType), Fail,
				"A resource's owning subscription could not be identified from its resource ID")
			check.Cause = err
			report = append(report, check)
			continue
		}
		subscriptions[strings.ToLower(id.SubscriptionID)] = true
	}
	_, err := c.reads.armClient()
	if err != nil {
		return "", append(report, readFailure("resource-refs", plan.SubscriptionID,
			subscriptionResourceType, "Subscription", err))
	}
	keys := slices.Sorted(maps.Keys(subscriptions))
	checks := make(Report, len(keys))
	tenants := make([]string, len(keys))
	tasks := make([]func(), 0, len(keys))
	for i, subscription := range keys {
		tasks = append(tasks, func() {
			defer c.progress.start("subscription tenants")()
			tenants[i], checks[i] = readSubscriptionTenant(ctx, c.reads, subscription)
		})
	}
	parallelReads(tasks...)
	var tenant string
	for i, subscription := range keys {
		if strings.EqualFold(subscription, plan.SubscriptionID) {
			tenant = tenants[i]
		}
	}
	for i, check := range checks {
		if tenants[i] != "" {
			if tenant == "" {
				tenant = tenants[i]
			}
			check = resourceKnown(check, true, strings.EqualFold(tenant, tenants[i]),
				"Referenced and target subscriptions do not belong to the same tenant")
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
		return "", readFailure("resource-refs", scope, subscriptionResourceType, "Subscription", err)
	}
	var result struct {
		TenantID string `json:"tenantId"`
	}
	err = runtime.UnmarshalAsJSON(response, &result)
	if err != nil {
		return "", readFailure("resource-refs", scope, subscriptionResourceType, "Subscription", err)
	}
	check := resourceCheck("resource-refs", scope, subscriptionResourceType,
		"Subscription owning tenant could not be verified")
	return result.TenantID, check
}

func (c *sdkResourceChecker) Check(ctx context.Context, plan Plan) (Facts, Report, error) {
	local := *c
	local.readClients = &resourceClients{}
	local.progress = &resourceProgress{notify: c.onProgress, active: map[string]int{}}
	if local.reads == nil {
		local.reads = NewAuthorizationEvidence(c.credential, c.options)
	}
	c = &local
	defer c.reportProgress("")
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
			identities[0], identityReports[0] = c.readIdentity(ctx, plan.Hosting, tenant,
				"hosting identity", hostingIdentityResourceType)
		})
	}
	if plan.Workload.Supplied || plan.Workload.PrincipalRequired {
		tasks = append(tasks, func() {
			identities[1], identityReports[1] = c.readIdentity(ctx, plan.Workload, tenant,
				"workload identity", workloadIdentityResourceType)
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
	c.reportProgress("checking AKS agent pool and network")
	selected, networkReport := c.selectAgentPool(ctx, plan, cluster, pools)
	if cluster != nil {
		c.reportProgress("validating AKS hosting configuration")
		kubelet, checks := aksResourceChecks(*cluster, pools, poolsKnown, selected)
		facts.KubeletObjectID = kubelet
		report = append(report, checks...)
	} else {
		for _, id := range aksCheckIDs {
			if plan.Cluster.Missing != "" {
				report = append(report, SkippedCheck(id, plan.Cluster.diagnosticScope(), aksResourceType))
			} else {
				report = append(report, resourceCheck(id, plan.Cluster.ID,
					aksResourceType, "AKS configuration could not be read"))
			}
		}
	}
	if missing := plan.Storage.Missing + plan.Storage.LifecycleMissing; missing != "" {
		for _, id := range []string{"storage-type", "storage-security"} {
			report = append(report, SkippedCheck(id, plan.Storage.diagnosticScope(), storageResourceType))
		}
	} else if plan.Storage.Supplied {
		if storage != nil {
			c.reportProgress("validating Storage configuration")
			report = append(report, storageResourceChecks(*storage)...)
		} else {
			for _, id := range []string{"storage-type", "storage-security"} {
				report = append(report, resourceCheck(id, plan.Storage.ID, storageResourceType,
					"Storage configuration could not be read"))
			}
		}
	} else {
		for _, id := range []string{"storage-type", "storage-security"} {
			report = append(report, resourceResult(resourceCheck(id, plan.Storage.Parent,
				storageResourceType), NotApplicable))
		}
	}
	report = append(report, networkReport...)
	if err := ctx.Err(); err != nil {
		return facts, report, err
	}
	return facts, report, report.Err()
}

func (c *sdkResourceChecker) readCluster(
	ctx context.Context, resource Resource,
) (*armcontainerservice.ManagedCluster, []*armcontainerservice.AgentPool, bool, Report) {
	if resource.Missing != "" {
		return nil, nil, false, Report{SkippedCheck("resource-refs", resource.diagnosticScope(), aksResourceType)}
	}
	id, err := existingResourceID(resource.ID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		check := resourceCheck("resource-refs", resource.ID, aksResourceType, "AKS resource ID is invalid")
		check.Cause = err
		return nil, nil, false, Report{check}
	}
	client, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, nil, false, Report{readFailure("resource-refs", resource.ID, aksResourceType, "AKS cluster", err)}
	}
	done := c.progress.start("AKS")
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	done()
	if err != nil {
		return nil, nil, false, Report{readFailure("resource-refs", resource.ID, aksResourceType, "AKS cluster", err)}
	}
	report := Report{resourceKnown(resourceCheck("resource-refs", resource.ID, aksResourceType,
		"Returned AKS resource identity could not be verified"),
		response.ID != nil, strings.EqualFold(resource.ID, resourceValue(response.ID)),
		"Returned AKS resource does not match the selected cluster")}
	pools, poolErr := c.readAgentPools(ctx, id)
	if poolErr != nil {
		report = append(report, readFailure("resource-refs", resource.ID, aksResourceType, "Node pool list", poolErr))
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
	defer c.progress.start("node pools")()
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

func (c *sdkResourceChecker) readStorage(
	ctx context.Context, resource Resource,
) (*armstorage.Account, Report) {
	if resource.Missing != "" {
		return nil, Report{SkippedCheck("resource-refs", resource.diagnosticScope(), storageResourceType)}
	}
	id, err := ParseID(resource.ID, "Microsoft.Storage/storageAccounts")
	if err != nil {
		check := resourceCheck("resource-refs", resource.ID, storageResourceType, "Storage resource ID is invalid")
		check.Cause = err
		return nil, Report{check}
	}
	client, err := armstorage.NewAccountsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, Report{readFailure("resource-refs", resource.ID, storageResourceType, "Storage account", err)}
	}
	defer c.progress.start("Storage")()
	response, err := client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure("resource-refs", resource.ID, storageResourceType, "Storage account", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	return &response.Account, Report{
		resourceReadiness(resource.ID, resourceValue(response.ID), state, storageResourceType),
	}
}

func (c *sdkResourceChecker) readIdentity(
	ctx context.Context, resource Resource, tenant, description, resourceType string,
) (Identity, Check) {
	if resource.Missing != "" {
		return Identity{}, SkippedCheck("resource-refs", resource.diagnosticScope(), resourceType)
	}
	check := resourceCheck("resource-refs", resource.ID, resourceType, "Managed identity metadata could not be read")
	id, err := ParseID(resource.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
	if err != nil {
		check = resourceResult(check, Fail, "Managed identity resource ID is invalid")
		check.Cause = err
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
	defer c.progress.start(description)()
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return Identity{}, readFailure("resource-refs", resource.ID, resourceType, "Managed identity", err)
	}
	properties := response.Properties
	if properties == nil {
		check.FailureReasons = []string{"Managed identity properties could not be verified"}
		return Identity{}, check
	}
	if response.ID == nil || authString(properties.PrincipalID) == "" ||
		authString(properties.ClientID) == "" || authString(properties.TenantID) == "" || tenant == "" {
		check.FailureReasons = nil
		addFailureReason(&check, response.ID != nil, true, "Returned managed identity resource identity is missing", "")
		addFailureReason(&check, authString(properties.PrincipalID) != "", true, "Managed identity principal is missing", "")
		addFailureReason(&check, authString(properties.ClientID) != "", true, "Managed identity client is missing", "")
		addFailureReason(&check, authString(properties.TenantID) != "", true,
			"Managed identity owning tenant is missing", "")
		addFailureReason(&check, tenant != "", true, "Owning tenant consistency could not be verified", "")
		return Identity{}, check
	}
	identity := Identity{ID: *response.ID, PrincipalID: *properties.PrincipalID}
	if !strings.EqualFold(resource.ID, identity.ID) || !strings.EqualFold(tenant, *properties.TenantID) {
		check.FailureReasons = nil
		addFailureReason(&check, true, strings.EqualFold(resource.ID, identity.ID), "",
			"Returned managed identity does not match the selected resource")
		addFailureReason(&check, true, strings.EqualFold(tenant, *properties.TenantID), "",
			"Managed identity does not belong to the owning tenant")
		return Identity{}, resourceResult(check, Fail)
	}
	return identity, resourceResult(check, Pass)
}

func (c *sdkResourceChecker) reportProgress(detail string) {
	if c.onProgress != nil {
		c.onProgress(detail)
	}
}

type resourceProgress struct {
	mu     sync.Mutex
	active map[string]int
	notify func(string)
}

func (p *resourceProgress) start(resource string) func() {
	if p == nil || p.notify == nil {
		return func() {}
	}
	update := func(delta int) {
		p.mu.Lock()
		defer p.mu.Unlock()

		p.active[resource] += delta
		if p.active[resource] == 0 {
			delete(p.active, resource)
		}
		detail := ""
		if len(p.active) > 0 {
			detail = "reading " + strings.Join(slices.Sorted(maps.Keys(p.active)), ", ")
		}
		// Serialize delivery as well as state changes so an older snapshot cannot overwrite a newer one.
		p.notify(detail)
	}
	update(1)
	return func() { update(-1) }
}
