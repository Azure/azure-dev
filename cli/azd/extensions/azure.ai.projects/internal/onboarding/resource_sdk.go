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

func readFailure(id, scope, action string, err error) Check {
	c := resourceCheck(id, scope, action)
	c.Cause = err
	c.Missing = action + " at " + scope
	c.Observed = "resource could not be read; inspect the original service error"
	c.Remediation = "allow the stated read operation and retry; do not replace an unreadable resource"
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if response.StatusCode == http.StatusNotFound {
			c.Status, c.Observed = Fail, "referenced resource does not exist (HTTP 404)"
			c.Missing = "existing resource at " + scope
			c.Remediation = "verify the resource ID and existence with its administrator; " +
				"correct the reference or ensure the selected resource exists before retrying"
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
			identities[0], identityReports[0] = c.readIdentity(ctx, plan.Hosting, tenant, "hosting identity")
		})
	}
	if plan.Workload.Supplied || plan.Workload.PrincipalRequired {
		tasks = append(tasks, func() {
			identities[1], identityReports[1] = c.readIdentity(ctx, plan.Workload, tenant, "workload identity")
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
			c.reportProgress("validating Storage configuration")
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
	done := c.progress.start("AKS")
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	done()
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
	defer c.progress.start("Storage")()
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
	ctx context.Context, resource Resource, tenant, description string,
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
	defer c.progress.start(description)()
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
