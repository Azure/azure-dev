// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func (r *templatePlanResolver) bindResources(plan *onboarding.Plan, root *planEnvironment) {
	type accountSelection struct {
		node   *planResource
		config any
	}
	var accounts []accountSelection
	var unresolved string
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, "Microsoft.CognitiveServices/accounts") {
			continue
		}
		properties := node.env.value(node.raw["properties"])
		configs := planField(properties, "agentHostingConfigurations")
		if values, ok := configs.([]any); ok {
			for _, config := range values {
				id, missing := planString(planField(config, "clusterResourceId"))
				typeValue := planField(config, "hostingType")
				hostingType, typeMissing := planString(typeValue)
				if missing == "" && id != "" {
					if _, unknown := typeValue.(planUnknown); !unknown &&
						(typeMissing != "" || hostingType != "ManagedCluster") {
						r.err = errors.Join(r.err, fmt.Errorf(
							"I03: actual Foundry hostingType must be ManagedCluster at %s", node.path))
						continue
					}
				}
				if missing == "" && id != "" || strings.EqualFold(hostingType, "ManagedCluster") {
					accounts = append(accounts, accountSelection{node, config})
				}
			}
		} else if unknown, ok := configs.(planUnknown); ok {
			unresolved = node.path + ": " + unknown.reason
		}
	}
	if len(accounts) > 1 {
		outputs := root.value(root.template["outputs"])
		accountName, _ := planString(planField(planField(outputs, "AZURE_AI_ACCOUNT_NAME"), "value"))
		matching := slices.DeleteFunc(slices.Clone(accounts), func(account accountSelection) bool {
			name, missing := planString(account.node.env.value(account.node.raw["name"]))
			return missing != "" || !strings.EqualFold(name, accountName)
		})
		if len(matching) == 1 {
			accounts = matching
		}
	}
	if len(accounts) != 1 {
		reason := "no unique local Foundry account/agentHostingConfigurations association"
		if unresolved != "" {
			reason += ": " + unresolved
		}
		for _, entry := range []struct {
			kind string
			out  *onboarding.Resource
		}{
			{"Microsoft.ContainerService/managedClusters", &plan.Cluster},
			{"Microsoft.Storage/storageAccounts", &plan.Storage},
			{"Microsoft.ManagedIdentity/userAssignedIdentities (hosting)", &plan.Hosting},
			{"Microsoft.ManagedIdentity/userAssignedIdentities (workload)", &plan.Workload},
		} {
			*entry.out = onboarding.Resource{Origin: entry.kind + " at " + root.path, Missing: reason}
		}
		plan.SkippedChecks = append(plan.SkippedChecks,
			onboarding.SkippedCheck("N02", root.path, "selected Agent subnet configuration", reason),
			onboarding.SkippedCheck("P03", root.path, "selected network operations", reason),
			onboarding.SkippedCheck("P05", root.path, "native registry creation", reason),
			onboarding.SkippedCheck("P07", root.path, "kubelet pull grant authority", reason))
		return
	}
	account := accounts[0]
	r.account = account.node
	for _, entry := range []struct {
		field, kind string
		out         *onboarding.Resource
	}{
		{"clusterResourceId", "Microsoft.ContainerService/managedClusters", &plan.Cluster},
		{"storageAccountResourceId", "Microsoft.Storage/storageAccounts", &plan.Storage},
		{"hostingManagementIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Hosting},
		{"workloadIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Workload},
	} {
		value := planField(account.config, entry.field)
		if config, known := account.config.(map[string]any); known {
			present := false
			for name := range config {
				present = present || strings.EqualFold(name, entry.field)
			}
			if !present {
				r.err = errors.Join(r.err, fmt.Errorf("I02: actual Foundry hosting configuration is missing %s at %s",
					entry.field, account.node.path))
				continue
			}
		}
		*entry.out = r.resource(value, entry.kind,
			account.node.path+"/properties.agentHostingConfigurations."+entry.field)
		if account.node.missing != "" {
			entry.out.Missing = strings.Trim(entry.out.Missing+"; Foundry account selection: "+
				account.node.missing, "; ")
		}
	}
	r.bindRegistry(plan, account.node)
}

func (r *templatePlanResolver) bindRegistry(plan *onboarding.Plan, account *planResource) {
	accountID, accountMissing := account.id()
	var selections []onboarding.Resource
	for _, node := range r.nodes {
		if node.inactive ||
			!strings.EqualFold(node.kind, "Microsoft.CognitiveServices/accounts/projects/connections") {
			continue
		}
		properties := node.env.value(node.raw["properties"])
		id, missing := node.id()
		if missing == "" && accountMissing == "" &&
			!strings.HasPrefix(strings.ToLower(id), strings.ToLower(accountID)+"/projects/") {
			continue
		}
		category, categoryMissing := planString(planField(properties, "category"))
		if categoryMissing != "" {
			plan.SkippedChecks = append(plan.SkippedChecks,
				onboarding.SkippedCheck("P05", node.path, "native registry selection", categoryMissing),
				onboarding.SkippedCheck("P07", node.path, "native pull grant selection", categoryMissing))
			continue
		}
		if category != "ContainerRegistry" {
			continue
		}
		credentials := planField(properties, "credentials")
		registry := r.resource(planField(credentials, "resourceId"), "Microsoft.ContainerRegistry/registries",
			node.path+"/properties.credentials.resourceId")
		if missing != "" || accountMissing != "" {
			registry.LifecycleMissing = "registry-to-Foundry association cannot be resolved"
		} else if node.missing != "" {
			registry.LifecycleMissing = node.path + ": " + node.missing
		}
		if !slices.ContainsFunc(selections, func(existing onboarding.Resource) bool {
			return registry.ID != "" && strings.EqualFold(existing.ID, registry.ID)
		}) {
			selections = append(selections, registry)
		}
	}
	if len(selections) == 0 {
		for _, node := range r.nodes {
			if node.inactive || !strings.EqualFold(node.kind, "Microsoft.ContainerRegistry/registries") {
				continue
			}
			id, missing := node.id()
			var value any = id
			if missing != "" {
				value = planUnknown{node.path + ": " + missing}
			}
			registry := r.resource(value, node.kind, node.path)
			registry.LifecycleMissing = strings.Trim(registry.LifecycleMissing+"; "+node.missing, "; ")
			selections = append(selections, registry)
		}
	}
	if len(selections) == 1 {
		plan.Registry = selections[0]
		plan.RegistryGrantPlanned = new(false)
		if plan.Registry.Supplied && plan.Registry.LifecycleMissing == "" {
			reason := "the compiled template references an external registry; only native registry creation is covered"
			plan.SkippedChecks = append(plan.SkippedChecks,
				onboarding.SkippedCheck("P05", plan.Registry.ID, "native registry creation", reason),
				onboarding.SkippedCheck("P07", plan.Registry.ID, "native kubelet pull grant authority", reason))
			plan.Registry = onboarding.Resource{}
		}
	} else if len(selections) > 1 {
		reason := "multiple registry connections or creations prevent a unique native registry selection"
		plan.SkippedChecks = append(plan.SkippedChecks,
			onboarding.SkippedCheck("P05", account.path, "native registry creation", reason),
			onboarding.SkippedCheck("P07", account.path, "kubelet pull grant authority", reason))
	} else if len(r.unresolved) != 0 {
		for _, missing := range r.unresolved {
			plan.SkippedChecks = append(plan.SkippedChecks,
				onboarding.SkippedCheck("P05", account.path, "native registry selection", missing),
				onboarding.SkippedCheck("P07", account.path, "native pull grant selection", missing))
		}
	}
}

func (n *planResource) assignmentScope() (string, string) {
	if raw, present := n.raw["scope"]; present {
		scope, missing := planString(n.env.value(raw))
		if missing != "" {
			return "", missing
		}
		if strings.HasPrefix(scope, "/") {
			return scope, ""
		}
		if resource := n.env.resources[scope]; resource != nil {
			return resource.id()
		}
		if strings.Contains(scope, "/") && n.env.scope.groupARMID != "" {
			return n.env.scope.groupARMID + "/providers/" + scope, ""
		}
		return "", "role-assignment scope is not a resolved resource ID or local symbol"
	}
	if n.env.scope.groupARMID != "" {
		return n.env.scope.groupARMID, ""
	}
	if n.env.scope.group == "" && n.env.scope.missing == "" && n.env.scope.subscriptionARMID != "" {
		return n.env.scope.subscriptionARMID, ""
	}
	return "", "role-assignment deployment scope is not resolved"
}

func (r *templatePlanResolver) bindGrants(plan *onboarding.Plan) {
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, "Microsoft.Authorization/roleAssignments") {
			continue
		}
		scope, missing := node.assignmentScope()
		properties := node.env.value(node.raw["properties"])
		principal, symbolic := planField(properties, "principalId").(planPrincipal)
		role, _ := planString(planField(properties, "roleDefinitionId"))
		roleID, _ := arm.ParseResourceID(role)
		checkID := ""
		if roleID != nil {
			checkID = onboarding.GrantCheckIDForRole(roleID.Name)
		}
		associated := symbolic && (plan.Hosting.ID != "" && strings.EqualFold(principal.resourceID, plan.Hosting.ID) ||
			plan.Workload.ID != "" && strings.EqualFold(principal.resourceID, plan.Workload.ID) ||
			strings.EqualFold(principal.binding, "kubeletObjectId"))
		if symbolic && strings.EqualFold(principal.binding, "kubeletObjectId") {
			checkID = "P07"
		}
		var target onboarding.Resource
		for _, entry := range []struct {
			id       string
			resource onboarding.Resource
		}{
			{"G01", plan.Cluster}, {"G02", plan.Workload}, {"G03", plan.Storage}, {"P07", plan.Registry},
		} {
			if entry.resource.ID == "" || !strings.EqualFold(scope, entry.resource.ID) {
				continue
			}
			associated, checkID, target = true, entry.id, entry.resource
			break
		}
		if !associated && node.missing == "" && missing == "" {
			continue
		}
		if checkID == "" && symbolic && plan.Hosting.ID != "" &&
			strings.EqualFold(principal.resourceID, plan.Hosting.ID) {
			checkID = "G02"
		}
		if checkID == "" {
			checkID = "authored grant"
		}
		if target.ID == "" {
			for _, entry := range []struct {
				id       string
				resource onboarding.Resource
			}{{"G01", plan.Cluster}, {"G02", plan.Workload}, {"G03", plan.Storage}, {"P07", plan.Registry}} {
				if entry.id == checkID {
					target = entry.resource
					break
				}
			}
		}
		deploymentMissing := strings.Trim(node.missing+"; "+node.env.scope.missing, "; ")
		deploymentScope := node.env.scope.groupARMID
		if deploymentScope == "" && node.env.scope.group == "" && node.env.scope.missing == "" {
			deploymentScope = node.env.scope.subscriptionARMID
		}
		if deploymentScope == "" && deploymentMissing == "" {
			deploymentMissing = "actual nested grant deployment scope is not resolved"
		}
		grant := onboarding.PlannedGrant{
			CheckID: checkID, Target: target, DeploymentScope: deploymentScope,
			Missing: strings.Trim(node.missing+"; "+missing, "; "), DeploymentMissing: deploymentMissing,
		}
		if missing == "" && !strings.EqualFold(scope, target.ID) {
			parsed, err := arm.ParseResourceID(scope)
			if err != nil || parsed.SubscriptionID == "" || strings.ContainsAny(scope, "?#%\\@:\x00 \t\r\n") {
				r.err = errors.Join(r.err, fmt.Errorf("P06: actual role-assignment scope is not a valid ARM ID"))
				continue
			}
			switch strings.ToLower(parsed.ResourceType.String()) {
			case "microsoft.resources/subscriptions", "microsoft.resources/resourcegroups":
				current := r.scope(parsed.SubscriptionID, parsed.ResourceGroupName)
				grant.AuthorityScope = current.subscriptionARMID
				if current.groupExists {
					grant.AuthorityScope = current.groupARMID
				}
			default:
				grant.Target = r.resource(scope, parsed.ResourceType.String(), node.path+"/scope")
				if !grant.Target.Supplied && grant.Target.ID != plan.Cluster.ID &&
					grant.Target.ID != plan.Storage.ID && grant.Target.ID != plan.Workload.ID &&
					grant.Target.ID != plan.Registry.ID {
					grant.Missing = "current state of an additional authored grant target is not available to this check"
				}
			}
		}
		if checkID == "P07" {
			if grant.Missing != "" {
				plan.RegistryGrantMissing = grant.Missing
			} else {
				plan.RegistryGrantPlanned = new(true)
			}
			if deploymentMissing != "" || grant.DeploymentScope == "" {
				plan.SkippedChecks = append(plan.SkippedChecks,
					onboarding.SkippedCheck("P09", node.kind+" at "+node.path,
						"native registry grant deployment permissions", deploymentMissing))
			} else if plan.TargetScopeMissing != "" {
				plan.SkippedChecks = append(plan.SkippedChecks,
					onboarding.SkippedCheck("P09", grant.DeploymentScope,
						"cross-resource-group native registry grant deployment permissions", plan.TargetScopeMissing))
			} else {
				targetGroup := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
				if !strings.EqualFold(grant.DeploymentScope, targetGroup) {
					for _, action := range []string{"write", "read", "operations/read"} {
						plan.AddOperation("P09", "Microsoft.Resources/deployments/"+action, grant.DeploymentScope)
					}
				}
			}
			continue
		}
		if deploymentMissing != "" || grant.DeploymentScope == "" {
			plan.SkippedChecks = append(plan.SkippedChecks,
				onboarding.SkippedCheck("P09", node.kind+" at "+node.path,
					"nested grant deployment permissions", deploymentMissing))
		} else if plan.TargetScopeMissing != "" {
			plan.SkippedChecks = append(plan.SkippedChecks,
				onboarding.SkippedCheck("P09", grant.DeploymentScope,
					"cross-resource-group grant deployment permissions", plan.TargetScopeMissing))
		}
		plan.Grants = append(plan.Grants, grant)
	}
}

func (r *templatePlanResolver) ancestor(node *planResource) string {
	if node.env.scope.groupExists {
		return node.env.scope.groupARMID
	}
	return node.env.scope.subscriptionARMID
}

func (r *templatePlanResolver) networkOperation(
	plan *onboarding.Plan, node *planResource, action, scope, missing string,
) {
	if missing == "" {
		missing = node.missing
	}
	if missing == "" && scope == "" {
		missing = "actual network deployment scope is not resolved"
	}
	if missing != "" {
		diagnostic := node.kind + " at " + node.path
		if id, reason := node.id(); reason == "" && id != "" {
			diagnostic = id + " at " + node.path
		}
		plan.SkippedChecks = append(plan.SkippedChecks,
			onboarding.SkippedCheck("P03", diagnostic, action, missing))
		return
	}
	plan.AddOperation("P03", action, scope)
}

func (r *templatePlanResolver) selectedSubnet(
	plan *onboarding.Plan, node *planResource, value any, agent bool,
) onboarding.Resource {
	resource := r.resource(value, "Microsoft.Network/virtualNetworks/subnets", node.path)
	if node.missing != "" {
		resource.Missing = strings.Trim(resource.Missing+"; selected network condition: "+node.missing, "; ")
	}
	if resource.Missing != "" {
		id := "P03"
		if agent {
			id = "N02"
		}
		plan.SkippedChecks = append(plan.SkippedChecks,
			onboarding.SkippedCheck(id, resource.Origin, "selected subnet configuration", resource.Missing))
		if parent, err := arm.ParseResourceID(resource.Parent); err == nil &&
			strings.EqualFold(parent.ResourceType.String(), "Microsoft.Network/virtualNetworks") {
			plan.AddOperation("P03", "Microsoft.Network/virtualNetworks/read", resource.Parent)
		}
		plan.SkippedChecks = append(plan.SkippedChecks, onboarding.SkippedCheck("P03", resource.Origin,
			"selected subnet creation/join permissions", resource.Missing))
		return resource
	}
	subnet, err := arm.ParseResourceID(resource.ID)
	if err != nil || !strings.EqualFold(subnet.ResourceType.String(), "Microsoft.Network/virtualNetworks/subnets") {
		r.err = errors.Join(r.err, fmt.Errorf("N02/P03: actual selected subnet ID is malformed"))
		return resource
	}
	vnet := subnet.Parent.String()
	plan.AddOperation("P03", "Microsoft.Network/virtualNetworks/read", vnet)
	joinScope := resource.ID
	if !resource.Supplied {
		joinScope = vnet
		if resource.LifecycleMissing == "" {
			plan.AddOperation("P03", "Microsoft.Network/virtualNetworks/subnets/write", vnet)
		}
	}
	r.networkOperation(plan, node, "Microsoft.Network/virtualNetworks/subnets/join/action",
		joinScope, resource.LifecycleMissing)
	return resource
}

func (r *templatePlanResolver) bindNetwork(plan *onboarding.Plan) {
	accountID := ""
	vnets := map[string]bool{}
	var privateEndpoints []string
	optionalField := func(properties any, key string) any {
		if properties == nil {
			return nil
		}
		if object, ok := properties.(map[string]any); ok {
			for name, value := range object {
				if strings.EqualFold(name, key) {
					return value
				}
			}
			return nil
		}
		return planField(properties, key)
	}
	for _, node := range []*planResource{r.account} {
		if node == nil || node.inactive {
			continue
		}
		properties := node.env.value(node.raw["properties"])
		accountID, _ = node.id()
		injections := optionalField(properties, "networkInjections")
		if injections == nil {
			continue
		}
		if values, ok := injections.([]any); ok {
			for _, injection := range values {
				scenario, scenarioMissing := planString(planField(injection, "scenario"))
				managed, missing := planBool(planField(injection, "useMicrosoftManagedNetwork"))
				if scenarioMissing != "" || scenario == "agent" && missing != "" {
					reason := strings.Trim(scenarioMissing+"; "+missing, "; ")
					plan.AgentSubnet = onboarding.Resource{Origin: node.path + "/networkInjections", Missing: reason}
					plan.SkippedChecks = append(plan.SkippedChecks, onboarding.SkippedCheck("P03",
						plan.AgentSubnet.Origin, "Agent subnet operation selection", reason))
					continue
				}
				if scenario != "agent" || managed {
					continue
				}
				plan.AgentSubnet = r.selectedSubnet(plan, node, planField(injection, "subnetArmId"), true)
				if id, err := arm.ParseResourceID(plan.AgentSubnet.ID); err == nil {
					vnets[strings.ToLower(id.Parent.String())] = true
				}
			}
		} else if unknown, ok := injections.(planUnknown); ok {
			reason := "networkInjections cannot be resolved: " + unknown.reason
			plan.AgentSubnet = onboarding.Resource{Origin: node.path + "/networkInjections", Missing: reason}
			r.networkOperation(plan, node, "Agent subnet read/create/join operation selection", "", reason)
		}
	}
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, "Microsoft.Network/privateEndpoints") {
			continue
		}
		properties := node.env.value(node.raw["properties"])
		connectionList := optionalField(properties, "privateLinkServiceConnections")
		connections, _ := connectionList.([]any)
		var associationMissing string
		if unknown, ok := connectionList.(planUnknown); ok {
			associationMissing = unknown.reason
		}
		if len(connections) == 0 && associationMissing == "" {
			continue
		}
		associated := slices.ContainsFunc(connections, func(connection any) bool {
			target, missing := planString(planField(planField(connection, "properties"), "privateLinkServiceId"))
			if missing != "" {
				associationMissing = missing
			}
			return accountID != "" && strings.EqualFold(target, accountID)
		})
		if !associated {
			if accountID != "" && associationMissing == "" {
				continue
			}
			reason := "privateLinkServiceConnections cannot resolve the private endpoint's Foundry account association"
			if accountID == "" {
				reason += ": actual Foundry account ID is not resolved"
			}
			if associationMissing != "" {
				reason += ": " + associationMissing
			}
			r.networkOperation(plan, node, "private endpoint Foundry association", "", reason)
		}
		r.networkOperation(plan, node, "Microsoft.Network/privateEndpoints/write", r.ancestor(node), "")
		subnet := r.selectedSubnet(plan, node, planField(planField(properties, "subnet"), "id"), false)
		if id, err := arm.ParseResourceID(subnet.ID); err == nil {
			vnets[strings.ToLower(id.Parent.String())] = true
		}
		if id, missing := node.id(); missing == "" {
			privateEndpoints = append(privateEndpoints, id)
		}
	}
	for _, node := range r.nodes {
		if node.inactive {
			continue
		}
		switch strings.ToLower(node.kind) {
		case "microsoft.network/privateendpoints/privatednszonegroups":
			id, missing := node.id()
			parsed, err := arm.ParseResourceID(id)
			if missing != "" || err != nil {
				r.networkOperation(plan, node, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write", "",
					"private-DNS group/endpoint association is not resolved")
				continue
			}
			if !slices.ContainsFunc(privateEndpoints, func(endpoint string) bool {
				return strings.EqualFold(endpoint, parsed.Parent.String())
			}) {
				continue
			}
			r.networkOperation(plan, node, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write",
				r.ancestor(node), "")
			configList := optionalField(node.env.value(node.raw["properties"]), "privateDnsZoneConfigs")
			if unknown, ok := configList.(planUnknown); ok {
				reason := "privateDnsZoneConfigs cannot resolve the selected DNS zones: " + unknown.reason
				for _, action := range []string{"write", "join/action"} {
					r.networkOperation(plan, node, "Microsoft.Network/privateDnsZones/"+action, "", reason)
				}
				continue
			}
			configs, _ := configList.([]any)
			for _, config := range configs {
				zone := r.resource(planField(planField(config, "properties"), "privateDnsZoneId"),
					"Microsoft.Network/privateDnsZones", node.path+"/privateDnsZoneConfigs")
				joinScope := zone.ID
				if !zone.Supplied {
					joinScope = zone.Parent
					r.networkOperation(plan, node, "Microsoft.Network/privateDnsZones/write",
						zone.Parent, zone.Missing+zone.LifecycleMissing)
				}
				r.networkOperation(plan, node, "Microsoft.Network/privateDnsZones/join/action",
					joinScope, zone.Missing+zone.LifecycleMissing)
				if parsed, err := arm.ParseResourceID(zone.ID); err == nil {
					plan.OwningSubscriptions = append(plan.OwningSubscriptions, parsed.SubscriptionID)
				}
			}
		case "microsoft.network/privatednszones/virtualnetworklinks":
			vnet, missing := planString(planField(planField(node.env.value(node.raw["properties"]),
				"virtualNetwork"), "id"))
			if missing != "" {
				r.networkOperation(plan, node, "Microsoft.Network/privateDnsZones/virtualNetworkLinks/write", "", missing)
				continue
			}
			if vnets[strings.ToLower(vnet)] {
				r.networkOperation(plan, node, "Microsoft.Network/privateDnsZones/virtualNetworkLinks/write",
					r.ancestor(node), "")
				r.networkOperation(plan, node, "Microsoft.Network/virtualNetworks/join/action", vnet, "")
			}
		}
	}
}
