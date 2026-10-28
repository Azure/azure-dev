// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type byoTarget struct {
	byoResourceOrigin
	apiVersion string
	tagged     bool
}

func byoTargets(plan onboarding.Plan) []byoTarget {
	var targets []byoTarget
	add := func(id, version string, supplied, tagged bool) {
		source := "created"
		if supplied {
			source = "supplied"
		}
		targets = append(targets, byoTarget{
			byoResourceOrigin: byoResourceOrigin{ID: id, Source: source, InitiallyAbsent: !supplied},
			apiVersion:        version, tagged: tagged,
		})
	}
	parent := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	add(parent, "2021-04-01", plan.ResourceGroupSupplied, true)
	add(plan.Cluster.ID, "2025-01-01", true, false)
	add(plan.Account.ID, "2026-07-15-preview", false, true)
	add(plan.Project.ID, "2026-07-15-preview", false, false)
	add(plan.Storage.ID, "2023-05-01", false, true)
	add(plan.Registry.ID, "2025-03-01-preview", false, true)
	add(plan.Hosting.ID, "2023-01-31", false, true)
	add(plan.Workload.ID, "2023-01-31", false, true)
	network := plan.Network
	add(network.VNetID, "2024-05-01", true, false)
	add(network.VNetID+"/subnets/"+network.AgentSubnetName, "2024-05-01", !network.CreateAgentSubnet, false)
	add(network.VNetID+"/subnets/"+network.PESubnetName, "2024-05-01", !network.CreatePESubnet, false)
	peID := parent + "/providers/Microsoft.Network/privateEndpoints/" + plan.Account.Name + "-private-endpoint"
	add(peID, "2024-05-01", false, false)
	add(peID+"/privateDnsZoneGroups/"+plan.Account.Name+"-dns-group", "2024-05-01", false, false)
	dnsParent := parent
	if network.DNSResourceGroup != "" {
		subscription := network.DNSSubscription
		if subscription == "" {
			subscription = plan.SubscriptionID
		}
		dnsParent = "/subscriptions/" + subscription + "/resourceGroups/" + network.DNSResourceGroup
	}
	token := strings.TrimPrefix(plan.Registry.Name, "cr")
	for _, zone := range []struct{ name, link string }{
		{"privatelink.services.ai.azure.com", "aiServices-"},
		{"privatelink.openai.azure.com", "aiServicesOpenAI-"},
		{"privatelink.cognitiveservices.azure.com", "aiServicesCognitiveServices-"},
	} {
		id := dnsParent + "/providers/Microsoft.Network/privateDnsZones/" + zone.name
		add(id, "2020-06-01", network.DNSResourceGroup != "", false)
		if network.DNSResourceGroup == "" {
			add(id+"/virtualNetworkLinks/"+zone.link+token+"-link", "2024-06-01", false, false)
		}
	}
	return targets
}

func (p *FoundryProvisioningProvider) observeBYOTargets(
	ctx context.Context, plan onboarding.Plan, previous *byoState,
) ([]byoResourceOrigin, error) {
	targets := byoTargets(plan)
	origins := make([]byoResourceOrigin, len(targets))
	for i, target := range targets {
		origins[i] = target.byoResourceOrigin
	}
	if previous != nil && !slices.Equal(origins, previous.Origins) {
		return nil, fmt.Errorf("BYO AKS original resource sources/absence evidence are incomplete or changed")
	}
	client, err := armresources.NewClient(p.subID, p.credential, onboarding.ARMOptions(p.armClientOptions))
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		resource, err := client.GetByID(ctx, target.ID, target.apiVersion, nil)
		if previous == nil && target.Source == "created" {
			if isNotFound(err) {
				continue
			}
			if err == nil {
				return nil, fmt.Errorf("planned BYO AKS resource %s already exists without an original deployment intent; "+
					"refusing adoption", target.ID)
			}
			return nil, fmt.Errorf("establish initial absence of %s: %w", target.ID, err)
		}
		if err != nil {
			return nil, fmt.Errorf("read exact BYO AKS resource %s: %w", target.ID, err)
		}
		if resource.ID == nil || !strings.EqualFold(*resource.ID, target.ID) {
			return nil, fmt.Errorf("BYO AKS resource response does not match %s", target.ID)
		}
		if previous != nil && target.ID == plan.Project.ID {
			principal, _ := previous.Ownership["projectPrincipalId"].(string)
			if resource.Identity == nil || resource.Identity.PrincipalID == nil ||
				!strings.EqualFold(*resource.Identity.PrincipalID, principal) {
				return nil, fmt.Errorf("recorded BYO AKS project principal changed; refusing replacement")
			}
		}
		if previous != nil && target.Source == "created" && target.tagged {
			marker := resource.Tags[byoOwnerTag]
			if marker == nil || *marker != plan.DeploymentID {
				return nil, fmt.Errorf("BYO AKS resource %s lacks the recorded deployment marker", target.ID)
			}
			if target.ID == plan.Hosting.ID || target.ID == plan.Workload.ID {
				properties, ok := resource.Properties.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("recorded BYO AKS identity properties are unavailable")
				}
				principal, _ := properties["principalId"].(string)
				key := "hostingPrincipalId"
				if target.ID == plan.Workload.ID {
					key = "workloadPrincipalId"
				}
				recorded, _ := previous.Ownership[key].(string)
				if principal == "" || !strings.EqualFold(principal, recorded) {
					return nil, fmt.Errorf("recorded BYO AKS identity principal changed; refusing replacement")
				}
			}
		}
	}
	if previous != nil {
		return previous.Origins, nil
	}
	return origins, nil
}

func (p *FoundryProvisioningProvider) verifyBYODeployment(
	ctx context.Context, plan onboarding.Plan, previous *byoState,
) error {
	client, err := p.deploymentsClient(ctx)
	if err != nil {
		return err
	}
	deployment, err := client.GetAtSubscriptionScope(ctx, p.deploymentName(), nil)
	if previous == nil {
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("establish initial absence of the BYO AKS deployment: %w", err)
		}
		return fmt.Errorf("the stable ARM deployment already exists without a BYO AKS intent; refusing to overwrite it")
	}
	if err != nil {
		return fmt.Errorf("read completed BYO AKS deployment: %w", err)
	}
	properties := deployment.Properties
	if deployment.ID == nil || !strings.EqualFold(*deployment.ID, plan.DeploymentID) ||
		properties == nil || properties.ProvisioningState == nil || *properties.ProvisioningState != "Succeeded" {
		return fmt.Errorf("the recorded BYO AKS deployment is not the exact completed deployment")
	}
	parameters, ok := properties.Parameters.(map[string]any)
	if !ok {
		return fmt.Errorf("recorded BYO AKS deployment parameter evidence is unavailable")
	}
	binding, err := byoBindingDigest(parameters)
	if err != nil {
		return err
	}
	if binding != previous.BindingDigest {
		return fmt.Errorf("the stable ARM deployment bindings changed outside the recorded BYO AKS intent")
	}
	exported, err := client.ExportTemplateAtSubscriptionScope(ctx, p.deploymentName(), nil)
	if err != nil {
		return fmt.Errorf("verify completed BYO AKS template: %w", err)
	}
	template, ok := exported.Template.(map[string]any)
	if !ok {
		return fmt.Errorf("recorded BYO AKS deployment template is unavailable")
	}
	digest, err := byoTemplateDigest(template)
	if err != nil {
		return err
	}
	if digest != previous.TemplateDigest {
		return fmt.Errorf("the stable ARM deployment template changed outside the recorded BYO AKS intent")
	}
	ownership, err := byoOwnership(properties.Outputs, plan, previous.Facts)
	if err != nil {
		return err
	}
	actual, err := onboarding.Digest(ownership)
	if err != nil {
		return err
	}
	expected, err := onboarding.Digest(previous.Ownership)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("completed BYO AKS identity/grant outputs differ from the saved intent")
	}
	return nil
}

var byoPrincipalID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validateBYOFacts(plan onboarding.Plan, facts onboarding.Facts) error {
	role, err := arm.ParseResourceID(facts.FederatedIdentityRoleDefinitionID)
	if !byoPrincipalID.MatchString(facts.KubeletObjectID) || err != nil ||
		!strings.EqualFold(role.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") ||
		!strings.EqualFold(role.SubscriptionID, plan.SubscriptionID) ||
		role.ResourceGroupName != "" || !byoPrincipalID.MatchString(role.Name) {
		return fmt.Errorf("BYO AKS requires verified kubelet and built-in federation role bindings")
	}
	return nil
}

func byoOwnership(outputs any, plan onboarding.Plan, facts onboarding.Facts) (map[string]any, error) {
	if err := validateBYOFacts(plan, facts); err != nil {
		return nil, err
	}
	parameters, ok := outputs.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("BYO AKS ownership outputs are missing; retain the unresolved deployment intent")
	}
	var envelope map[string]any
	for name, value := range parameters {
		if canonicalizeOutputName(name) == "AZD_BYO_AKS_OWNERSHIP" {
			envelope, _ = value.(map[string]any)
			break
		}
	}
	if envelope == nil {
		return nil, fmt.Errorf("BYO AKS ownership output is missing")
	}
	ownership, ok := envelope["value"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("BYO AKS ownership output must be an object")
	}
	for key, expected := range map[string]string{
		"deploymentId": plan.DeploymentID, "hostingIdentityResourceId": plan.Hosting.ID,
		"workloadIdentityResourceId": plan.Workload.ID, "storageAccountResourceId": plan.Storage.ID,
		"registryResourceId": plan.Registry.ID,
	} {
		actual, _ := ownership[key].(string)
		if !strings.EqualFold(actual, expected) {
			return nil, fmt.Errorf("BYO AKS ownership %s differs from its write-ahead target", key)
		}
	}
	for _, key := range []string{"ownedStorage", "ownedRegistry", "ownedHostingIdentity", "ownedWorkloadIdentity"} {
		value, _ := ownership[key].(bool)
		if !value {
			return nil, fmt.Errorf("BYO AKS default-resource ownership %s is unverified", key)
		}
	}
	principals := map[string]string{}
	for _, key := range []string{"hostingPrincipalId", "workloadPrincipalId", "projectPrincipalId", "kubeletPrincipalId"} {
		value, _ := ownership[key].(string)
		if !byoPrincipalID.MatchString(value) {
			return nil, fmt.Errorf("BYO AKS ownership %s is unverified", key)
		}
		principals[key] = value
	}
	if strings.EqualFold(principals["hostingPrincipalId"], principals["workloadPrincipalId"]) {
		return nil, fmt.Errorf("BYO AKS hosting and workload principals must be distinct")
	}
	if !strings.EqualFold(principals["kubeletPrincipalId"], facts.KubeletObjectID) {
		return nil, fmt.Errorf("BYO AKS kubelet principal differs from its verified AKS binding")
	}
	var grants []struct {
		ID, Scope, PrincipalID, RoleDefinitionID string
	}
	raw, err := json.Marshal(ownership["roleAssignments"])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &grants); err != nil || len(grants) != 6 {
		return nil, fmt.Errorf("BYO AKS ownership requires all six role assignments")
	}
	expectedGrants := map[string]bool{}
	key := func(scope, principal, role string) string {
		return strings.ToLower(scope + "|" + principal + "|" + role)
	}
	for _, grant := range []struct{ scope, principal, role string }{
		{plan.Cluster.ID, principals["hostingPrincipalId"], "acdd72a7-3385-48ef-bd42-f606fba81ae7"},
		{plan.Cluster.ID, principals["hostingPrincipalId"], "85cb6faf-e071-4c9b-8136-154b5a04f717"},
		{plan.Storage.ID, principals["workloadPrincipalId"], "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
		{plan.Registry.ID, principals["projectPrincipalId"], "7f951dda-4ed3-4680-a7ca-43fe172d538d"},
		{plan.Registry.ID, principals["kubeletPrincipalId"], "7f951dda-4ed3-4680-a7ca-43fe172d538d"},
		{plan.Workload.ID, principals["hostingPrincipalId"],
			facts.FederatedIdentityRoleDefinitionID[strings.LastIndex(facts.FederatedIdentityRoleDefinitionID, "/")+1:]},
	} {
		expectedGrants[key(grant.scope, grant.principal, grant.role)] = true
	}
	for _, grant := range grants {
		id, err := arm.ParseResourceID(grant.ID)
		role, roleErr := arm.ParseResourceID(grant.RoleDefinitionID)
		if err != nil || roleErr != nil || !strings.EqualFold(id.Parent.String(), grant.Scope) ||
			!strings.EqualFold(id.ResourceType.String(), "Microsoft.Authorization/roleAssignments") ||
			!strings.EqualFold(role.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") ||
			!byoPrincipalID.MatchString(id.Name) || !byoPrincipalID.MatchString(role.Name) {
			return nil, fmt.Errorf("BYO AKS ownership contains an invalid role assignment")
		}
		binding := key(grant.Scope, grant.PrincipalID, role.Name)
		if !expectedGrants[binding] {
			return nil, fmt.Errorf("BYO AKS ownership contains an unexpected or duplicate role assignment")
		}
		delete(expectedGrants, binding)
	}
	return ownership, nil
}
