// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type sdkResourceChecker struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
}

// ARMOptions disables automatic provider registration so validation remains read-only.
func ARMOptions(options *arm.ClientOptions) *arm.ClientOptions {
	var result arm.ClientOptions
	if options != nil {
		result = *options
	}
	result.DisableRPRegistration = true
	return &result
}

// NewResourceChecker validates the existing cluster using the user-access-tenant credential.
func NewResourceChecker(credential azcore.TokenCredential, options *arm.ClientOptions) ResourceChecker {
	return &sdkResourceChecker{credential: credential, options: ARMOptions(options)}
}

func readFailure(id, scope, action string, err error) Check {
	check := resourceCheck(id, scope, action)
	check.Cause = err
	check.Observed = "resource read failed; grant the stated read permission and retry"
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		check.Observed = fmt.Sprintf("resource read failed (HTTP %d; code %s)", response.StatusCode, response.ErrorCode)
		if response.StatusCode == http.StatusNotFound {
			check.Status = Fail
		}
	}
	return check
}

func resourceReadiness(scope, id, state, action string) Check {
	return resourceKnown(resourceCheck("I02", scope, action), id != "" && state != "",
		strings.EqualFold(scope, id) && state == "Succeeded",
		"matching resource ID="+fmt.Sprint(strings.EqualFold(scope, id))+"; provisioningState="+state)
}

func (c *sdkResourceChecker) Check(ctx context.Context, input Input) (Facts, Report, error) {
	var facts Facts
	report := c.subscriptionTenants(ctx, input)
	cluster, pools, skus, checks := c.readCluster(ctx, input.ClusterID)
	report = append(report, checks...)
	if cluster == nil {
		return facts, report, report.Err()
	}
	kubelet, selected, checks := aksResourceChecks(*cluster, pools, skus)
	facts.KubeletObjectID = kubelet
	report = append(report, checks...)
	if err := report.Err(); err != nil {
		return facts, report, err
	}
	report = append(report, c.networkChecks(ctx, input, cluster, selected)...)
	if err := report.Err(); err != nil {
		return facts, report, err
	}
	role, err := c.federationRole(ctx, input.SubscriptionID)
	if err != nil {
		report = append(report, readFailure("G02", "/subscriptions/"+input.SubscriptionID,
			"Managed Identity Federated Identity Credential Contributor built-in role", err))
	}
	facts.FederatedIdentityRoleDefinitionID = role
	if ctx.Err() != nil {
		return facts, report, errors.Join(report.Err(), ctx.Err())
	}
	return facts, report, report.Err()
}

func (c *sdkResourceChecker) subscriptionTenants(ctx context.Context, input Input) Report {
	subscriptions := []string{input.SubscriptionID}
	for _, resource := range []struct{ id, kind string }{
		{input.ClusterID, "Microsoft.ContainerService/managedClusters"},
		{input.Network.VNetID, "Microsoft.Network/virtualNetworks"},
	} {
		id, err := ParseID(resource.id, resource.kind)
		if err != nil {
			return Report{readFailure("I02", resource.id, "complete resource ID", err)}
		}
		subscriptions = append(subscriptions, id.SubscriptionID)
	}
	if input.Network.DNSResourceGroup != "" && input.Network.DNSSubscription != "" {
		subscriptions = append(subscriptions, input.Network.DNSSubscription)
	}
	slices.Sort(subscriptions)
	subscriptions = slices.Compact(subscriptions)
	client, err := armsubscriptions.NewClient(c.credential, c.options)
	if err != nil {
		return Report{readFailure("I02", input.SubscriptionID, "subscriptions/read", err)}
	}
	var tenant string
	var report Report
	for _, subscription := range subscriptions {
		result, err := client.Get(ctx, subscription, nil)
		scope := "/subscriptions/" + subscription
		if err != nil {
			report = append(report, readFailure("I02", scope, "subscriptions/read", err))
			continue
		}
		current := resourceValue(result.TenantID)
		if tenant == "" {
			tenant = current
		}
		report = append(report, resourceKnown(resourceCheck("I02", scope,
			"target, AKS, network and DNS subscriptions have the same owning tenant"),
			current != "" && resourceValue(result.ID) != "",
			strings.EqualFold(tenant, current) && strings.EqualFold(resourceValue(result.ID), scope),
			"owning subscription tenant evidence"))
	}
	return report
}

func (c *sdkResourceChecker) readCluster(
	ctx context.Context, clusterID string,
) (*armcontainerservice.ManagedCluster, []*armcontainerservice.AgentPool, []*armcompute.ResourceSKU, Report) {
	id, err := ParseID(clusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return nil, nil, nil, Report{readFailure("I01", clusterID, "complete managed cluster ID", err)}
	}
	client, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, nil, nil, Report{readFailure("I02", clusterID, "managedClusters/read", err)}
	}
	cluster, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return nil, nil, nil, Report{readFailure("I02", clusterID, "managedClusters/read", err)}
	}
	state := ""
	if cluster.Properties != nil {
		state = resourceValue(cluster.Properties.ProvisioningState)
	}
	report := Report{resourceReadiness(clusterID, resourceValue(cluster.ID), state, "managedClusters/read")}
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
		report = append(report, readFailure("A10", clusterID, "managedClusters/agentPools/read", err))
		pools = nil
	}
	skus, err := c.readSKUs(ctx, id.SubscriptionID, resourceValue(cluster.Location))
	if err != nil {
		report = append(report, readFailure("A05", clusterID, "Microsoft.Compute/skus/read", err))
	}
	return &cluster.ManagedCluster, pools, skus, report
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

func (c *sdkResourceChecker) federationRole(ctx context.Context, subscription string) (string, error) {
	const name = "Managed Identity Federated Identity Credential Contributor"
	client, err := armauthorization.NewRoleDefinitionsClient(c.credential, c.options)
	if err != nil {
		return "", err
	}
	pager := client.NewListPager("/subscriptions/"+subscription,
		&armauthorization.RoleDefinitionsClientListOptions{Filter: new("roleName eq '" + name + "'")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, role := range page.Value {
			if role == nil || role.Properties == nil || resourceValue(role.Properties.RoleName) != name ||
				!strings.EqualFold(resourceValue(role.Properties.RoleType), "BuiltInRole") {
				continue
			}
			id, err := arm.ParseResourceID(resourceValue(role.ID))
			if err != nil || id.Name == "" ||
				!strings.EqualFold(id.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") {
				return "", fmt.Errorf("built-in federation role has an invalid ID")
			}
			return "/subscriptions/" + subscription + "/providers/Microsoft.Authorization/roleDefinitions/" + id.Name, nil
		}
	}
	return "", fmt.Errorf("required built-in role %q is unavailable", name)
}
