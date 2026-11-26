// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/onboarding"
)

func accountHostingConfigs(properties any) (any, bool) {
	object, ok := properties.(map[string]any)
	if !ok {
		return nil, false
	}
	for name, value := range object {
		if strings.EqualFold(name, "agentHostingConfigurations") {
			return value, value != nil
		}
	}
	return nil, false
}

func configIndicatesBYOHosting(config map[string]any) bool {
	for name, value := range config {
		switch {
		case strings.EqualFold(name, "hostingType"):
			if kind, ok := value.(string); ok && strings.EqualFold(kind, "ManagedCluster") {
				return true
			}
			if _, unknown := value.(planUnknown); unknown {
				return true
			}
		case strings.EqualFold(name, "clusterResourceId"):
			if value == nil {
				continue
			}
			if id, ok := value.(string); ok {
				if strings.TrimSpace(id) != "" {
					return true
				}
				continue
			}
			return true
		}
	}
	return false
}

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
		configs, present := accountHostingConfigs(properties)
		if !present {
			continue
		}
		if values, ok := configs.([]any); ok {
			for _, config := range values {
				if config == nil {
					continue
				}
				if entry, isMap := config.(map[string]any); isMap {
					if configIndicatesBYOHosting(entry) {
						r.hasHosting = true
					}
				} else if unknown, isUnknown := config.(planUnknown); isUnknown {
					r.hasHosting = true
					unresolved = node.path + ": " + unknown.reason
					continue
				}
				id, missing := planString(planField(config, "clusterResourceId"))
				typeValue := planField(config, "hostingType")
				hostingType, typeMissing := planString(typeValue)
				if missing == "" && id != "" {
					if _, unknown := typeValue.(planUnknown); !unknown &&
						(typeMissing != "" || !strings.EqualFold(hostingType, "ManagedCluster")) {
						r.err = errors.Join(r.err, fmt.Errorf(
							"resource-plan: actual Foundry hostingType must be ManagedCluster at %s", node.path))
						continue
					}
				}
				if missing == "" && id != "" || strings.EqualFold(hostingType, "ManagedCluster") {
					accounts = append(accounts, accountSelection{node, config})
				}
			}
		} else if unknown, ok := configs.(planUnknown); ok {
			r.hasHosting = true
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
		plan.AgentSubnet = onboarding.Resource{
			Origin: "Microsoft.Network/virtualNetworks/subnets at " + root.path, Missing: reason,
		}
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
				r.err = errors.Join(r.err,
					fmt.Errorf("resource-refs: actual Foundry hosting configuration is missing %s at %s",
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
}

func (r *templatePlanResolver) bindNetwork(plan *onboarding.Plan) {
	node := r.account
	if node == nil || node.inactive {
		return
	}
	properties := node.env.value(node.raw["properties"])
	var injections any
	if object, ok := properties.(map[string]any); ok {
		for name, value := range object {
			if strings.EqualFold(name, "networkInjections") {
				injections = value
			}
		}
	} else if properties != nil {
		injections = planField(properties, "networkInjections")
	}
	if values, ok := injections.([]any); ok {
		for _, injection := range values {
			scenario, scenarioMissing := planString(planField(injection, "scenario"))
			managed, missing := planBool(planField(injection, "useMicrosoftManagedNetwork"))
			if scenarioMissing != "" || scenario == "agent" && missing != "" {
				reason := strings.Trim(scenarioMissing+"; "+missing, "; ")
				plan.AgentSubnet = onboarding.Resource{Origin: node.path + "/networkInjections", Missing: reason}
				continue
			}
			if scenario != "agent" || managed {
				continue
			}
			resource := r.resource(planField(injection, "subnetArmId"),
				"Microsoft.Network/virtualNetworks/subnets", node.path)
			if node.missing != "" {
				resource.Missing = strings.Trim(resource.Missing+"; selected network condition: "+node.missing, "; ")
			}
			plan.AgentSubnet = resource
		}
	} else if unknown, ok := injections.(planUnknown); ok {
		reason := "networkInjections cannot be resolved: " + unknown.reason
		plan.AgentSubnet = onboarding.Resource{Origin: node.path + "/networkInjections", Missing: reason}
	}
}
