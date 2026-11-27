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

type hostingConfigCandidate struct {
	config     any
	unresolved string
}

type accountCandidate struct {
	node              *planResource
	name              string
	nameMissing       string
	nameEvaluated     bool
	hostingEvaluated  bool
	configs           []hostingConfigCandidate
	nonHosting        bool
	invalidTypeErrors []error
}

func planObjectField(object map[string]any, key string) (any, bool) {
	for name, field := range object {
		if strings.EqualFold(name, key) {
			return field, true
		}
	}
	return nil, false
}

func hasUnexpandedHostingCopy(properties map[string]any) bool {
	rawCopy, hasCopy := planObjectField(properties, "copy")
	if !hasCopy || rawCopy == nil {
		return false
	}
	copies, ok := rawCopy.([]any)
	if !ok {
		return true
	}
	return slices.ContainsFunc(copies, func(item any) bool {
		entry, ok := item.(map[string]any)
		if !ok {
			return true
		}
		nameField, _ := planObjectField(entry, "name")
		name, nameMissing := planString(nameField)
		if nameMissing == "" && !strings.EqualFold(name, "agentHostingConfigurations") {
			return false
		}
		countField, hasCount := planObjectField(entry, "count")
		num, isNum := countField.(float64)
		return !hasCount || !isNum || num > 0
	})
}

func (c *accountCandidate) evaluateName() {
	if c.nameEvaluated {
		return
	}
	c.name, c.nameMissing = planString(c.node.env.value(c.node.raw["name"]))
	c.nameEvaluated = true
}

func (c *accountCandidate) addUnresolved(reason string) {
	c.configs = append(c.configs, hostingConfigCandidate{unresolved: reason})
}

func planUnknownReason(value any, prefix, fallback string) string {
	if unknown, ok := value.(planUnknown); ok {
		return prefix + unknown.reason
	}
	return prefix + fallback
}

func (c *accountCandidate) evaluateHosting() {
	if c.hostingEvaluated {
		return
	}
	c.hostingEvaluated = true
	node := c.node
	properties := node.env.value(node.raw["properties"])
	if properties == nil {
		c.nonHosting = true
		return
	}
	object, ok := properties.(map[string]any)
	if !ok {
		c.addUnresolved(planUnknownReason(properties, node.path+": ",
			"account properties are not a locally resolved object"))
		return
	}
	configs, hasConfigs := planObjectField(object, "agentHostingConfigurations")
	if !hasConfigs || configs == nil {
		if hasUnexpandedHostingCopy(object) {
			c.addUnresolved(node.path +
				"/properties.copy: property copy loop for agentHostingConfigurations is not locally expanded")
			return
		}
		c.nonHosting = true
		return
	}
	values, ok := configs.([]any)
	if !ok {
		c.addUnresolved(planUnknownReason(configs, node.path+"/properties.agentHostingConfigurations: ",
			"value is not a locally resolved array"))
		return
	}
	for i, item := range values {
		itemOrigin := fmt.Sprintf("%s/properties.agentHostingConfigurations[%d]: ", node.path, i)
		configMap, ok := item.(map[string]any)
		if !ok {
			c.addUnresolved(planUnknownReason(item, itemOrigin, "configuration entry is not a locally resolved object"))
			continue
		}
		clusterField, hasCluster := planObjectField(configMap, "clusterResourceId")
		id, idMissing := "", ""
		if hasCluster && clusterField != nil {
			id, idMissing = planString(clusterField)
		}
		typeField, hasType := planObjectField(configMap, "hostingType")
		hostingType, typeMissing := "", "object has no hostingType property"
		_, isUnknown := typeField.(planUnknown)
		_, isPartial := typeField.(planPartialID)
		typeUnknown := !hasType || isUnknown || isPartial
		if hasType {
			hostingType, typeMissing = planString(typeField)
		}
		if idMissing == "" && id != "" {
			if !typeUnknown && (typeMissing != "" || !strings.EqualFold(hostingType, "ManagedCluster")) {
				c.invalidTypeErrors = append(c.invalidTypeErrors, fmt.Errorf(
					"resource-plan: actual Foundry hostingType must be ManagedCluster at %s", node.path))
				continue
			}
			c.configs = append(c.configs, hostingConfigCandidate{config: configMap})
			continue
		}
		switch {
		case strings.EqualFold(hostingType, "ManagedCluster"):
			c.configs = append(c.configs, hostingConfigCandidate{config: configMap})
		case idMissing != "":
			c.addUnresolved(itemOrigin + idMissing)
		case typeUnknown && hasType:
			c.addUnresolved(itemOrigin + typeMissing)
		}
	}
	if len(c.configs) == 0 && len(c.invalidTypeErrors) == 0 {
		c.nonHosting = true
	}
}

func resolveAccountNameOutput(root *planEnvironment) (string, string) {
	definitions, ok := root.template["outputs"].(map[string]any)
	if !ok {
		return "", ""
	}
	raw, found := planObjectField(definitions, "AZURE_AI_ACCOUNT_NAME")
	if !found || raw == nil {
		return "", ""
	}
	valExpr := raw
	if definition, ok := raw.(map[string]any); ok {
		if condition, hasCond := planObjectField(definition, "condition"); hasCond {
			active, missing := planBool(root.value(condition))
			if missing != "" {
				return "", "AZURE_AI_ACCOUNT_NAME: output condition: " + missing
			}
			if !active {
				return "", ""
			}
		}
		var hasValue bool
		if valExpr, hasValue = planObjectField(definition, "value"); !hasValue {
			return "", "AZURE_AI_ACCOUNT_NAME: output has no local value"
		}
	}
	value := root.value(valExpr)
	if value == nil {
		return "", ""
	}
	name, missing := planString(value)
	if missing != "" {
		return "", "AZURE_AI_ACCOUNT_NAME: " + missing
	}
	return name, ""
}

func setMissingBYOPlan(plan *onboarding.Plan, scopePath, reason string, includeSubnet bool) {
	for _, entry := range []struct {
		kind string
		out  *onboarding.Resource
	}{
		{"Microsoft.ContainerService/managedClusters", &plan.Cluster},
		{"Microsoft.Storage/storageAccounts", &plan.Storage},
		{"Microsoft.ManagedIdentity/userAssignedIdentities (hosting)", &plan.Hosting},
		{"Microsoft.ManagedIdentity/userAssignedIdentities (workload)", &plan.Workload},
	} {
		*entry.out = onboarding.Resource{Origin: entry.kind + " at " + scopePath, Missing: reason}
	}
	if includeSubnet {
		plan.AgentSubnet = onboarding.Resource{
			Origin: "Microsoft.Network/virtualNetworks/subnets at " + scopePath, Missing: reason,
		}
	}
}

func (r *templatePlanResolver) bindResources(plan *onboarding.Plan, root *planEnvironment) {
	outputName, outputUnresolved := resolveAccountNameOutput(root)
	var allAccounts []accountCandidate
	var existingNodes []*planResource
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, "Microsoft.CognitiveServices/accounts") {
			continue
		}
		if node.existing {
			existingNodes = append(existingNodes, node)
			continue
		}
		allAccounts = append(allAccounts, accountCandidate{node: node})
	}
	if len(existingNodes) > 0 {
		seenExistingIDs := map[string]bool{}
		for _, node := range existingNodes {
			if id, missing := node.id(); missing == "" && id != "" {
				scope, _ := node.resourceScope()
				key := strings.ToLower(id)
				deployedMatch := slices.ContainsFunc(allAccounts, func(c accountCandidate) bool {
					if c.node.existing || c.node.missing != "" {
						return false
					}
					if dScope, why := c.node.resourceScope(); why == "" && !strings.EqualFold(dScope, scope) {
						return false
					}
					dID, dMissing := c.node.id()
					return dMissing == "" && strings.EqualFold(dID, id)
				})
				if deployedMatch || (node.missing == "" && seenExistingIDs[key]) {
					continue
				}
				if node.missing == "" {
					seenExistingIDs[key] = true
				}
			}
			allAccounts = append(allAccounts, accountCandidate{
				node: node, hostingEvaluated: true, nonHosting: true,
			})
		}
	}

	incompleteAccounts := append(slices.Clone(r.unresolved), r.unresolvedAccounts...)
	var selectedAccount *accountCandidate
	var reasons []string
	appendReason := func(items ...string) {
		for _, item := range items {
			if item != "" && !slices.Contains(reasons, item) {
				reasons = append(reasons, item)
			}
		}
	}
	collectReasons := func(candidates ...*accountCandidate) {
		for _, candidate := range candidates {
			candidate.evaluateName()
			candidate.evaluateHosting()
			if candidate.nameMissing != "" {
				appendReason(candidate.node.path + ": account name: " + candidate.nameMissing)
			}
			if candidate.node.missing != "" {
				appendReason(candidate.node.path + ": " + candidate.node.missing)
			}
			for _, cfg := range candidate.configs {
				appendReason(cfg.unresolved)
			}
		}
	}

	if outputName != "" {
		var exactMatches, unknownName []*accountCandidate
		for i := range allAccounts {
			candidate := &allAccounts[i]
			candidate.evaluateName()
			if candidate.nameMissing != "" {
				unknownName = append(unknownName, candidate)
			} else if strings.EqualFold(candidate.name, outputName) {
				exactMatches = append(exactMatches, candidate)
			}
		}
		switch {
		case len(exactMatches) == 1 && len(unknownName) == 0 && len(incompleteAccounts) == 0:
			exactMatches[0].evaluateHosting()
			selectedAccount = exactMatches[0]
		case len(exactMatches) == 0:
			appendReason(fmt.Sprintf("AZURE_AI_ACCOUNT_NAME %q did not match a local Foundry account", outputName))
			collectReasons(unknownName...)
			appendReason(incompleteAccounts...)
		default:
			appendReason(fmt.Sprintf(
				"AZURE_AI_ACCOUNT_NAME %q matched multiple or ambiguous Foundry account candidates", outputName))
			collectReasons(exactMatches...)
			collectReasons(unknownName...)
			appendReason(incompleteAccounts...)
		}
	} else {
		var hostingAccounts []*accountCandidate
		for i := range allAccounts {
			if allAccounts[i].evaluateHosting(); !allAccounts[i].nonHosting {
				hostingAccounts = append(hostingAccounts, &allAccounts[i])
			}
		}
		if len(hostingAccounts) == 1 && len(incompleteAccounts) == 0 &&
			(outputUnresolved == "" || len(allAccounts) == 1) {
			selectedAccount = hostingAccounts[0]
		} else {
			appendReason(outputUnresolved)
			if outputUnresolved != "" && len(allAccounts) > 1 {
				for i := range allAccounts {
					collectReasons(&allAccounts[i])
				}
			} else {
				collectReasons(hostingAccounts...)
			}
			appendReason(incompleteAccounts...)
		}
	}

	if selectedAccount == nil {
		reason := "no unique local Foundry account/agentHostingConfigurations association"
		if len(reasons) > 0 {
			reason += ": " + strings.Join(reasons, "; ")
		}
		setMissingBYOPlan(plan, root.path, reason, true)
		return
	}

	for _, err := range selectedAccount.invalidTypeErrors {
		r.err = errors.Join(r.err, err)
	}
	if selectedAccount.nonHosting || len(selectedAccount.configs) == 0 {
		reason := fmt.Sprintf("selected Foundry account at %s has no ManagedCluster agentHostingConfigurations",
			selectedAccount.node.path)
		if selectedAccount.node.missing != "" {
			reason += "; Foundry account selection: " + selectedAccount.node.missing
		}
		setMissingBYOPlan(plan, selectedAccount.node.path, reason, true)
		return
	}

	var cfgReasons []string
	for _, cfg := range selectedAccount.configs {
		if cfg.unresolved != "" && !slices.Contains(cfgReasons, cfg.unresolved) {
			cfgReasons = append(cfgReasons, cfg.unresolved)
		}
	}
	if selectedAccount.node.missing != "" {
		reason := "no unique local Foundry account/agentHostingConfigurations association at " +
			selectedAccount.node.path
		if len(cfgReasons) > 0 {
			reason += ": " + strings.Join(cfgReasons, "; ")
		}
		reason += "; Foundry account selection: " + selectedAccount.node.missing
		setMissingBYOPlan(plan, selectedAccount.node.path, reason, true)
		return
	}

	r.account = selectedAccount.node
	if len(selectedAccount.configs) != 1 || selectedAccount.configs[0].unresolved != "" {
		reason := "no unique local Foundry account/agentHostingConfigurations association"
		if len(selectedAccount.configs) == 1 && len(cfgReasons) == 1 {
			reason += ": " + cfgReasons[0]
		} else if len(cfgReasons) > 0 {
			reason += " at " + selectedAccount.node.path + ": " + strings.Join(cfgReasons, "; ")
		} else {
			reason += " at " + selectedAccount.node.path
		}
		setMissingBYOPlan(plan, selectedAccount.node.path, reason, false)
		return
	}

	r.accountHostingProven = true
	accountConfig := selectedAccount.configs[0].config
	for _, entry := range []struct {
		field, kind string
		out         *onboarding.Resource
	}{
		{"clusterResourceId", "Microsoft.ContainerService/managedClusters", &plan.Cluster},
		{"storageAccountResourceId", "Microsoft.Storage/storageAccounts", &plan.Storage},
		{"hostingManagementIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Hosting},
		{"workloadIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Workload},
	} {
		value := planField(accountConfig, entry.field)
		if config, known := accountConfig.(map[string]any); known {
			if _, present := planObjectField(config, entry.field); !present {
				r.err = errors.Join(r.err,
					fmt.Errorf("resource-refs: actual Foundry hosting configuration is missing %s at %s",
						entry.field, selectedAccount.node.path))
				continue
			}
		}
		*entry.out = r.resource(value, entry.kind,
			selectedAccount.node.path+"/properties.agentHostingConfigurations."+entry.field)
	}
	if len(incompleteAccounts) == 0 {
		if len(allAccounts) == 1 {
			r.singleAccountHosting = true
		} else if plan.Workload.ID == "" && plan.Workload.Parent == "" {
			r.singleAccountHosting = !slices.ContainsFunc(allAccounts, func(c accountCandidate) bool {
				if c.node == selectedAccount.node {
					return false
				}
				c.evaluateHosting()
				return !c.nonHosting
			})
		}
	}
}

func (r *templatePlanResolver) bindNetwork(plan *onboarding.Plan) {
	node := r.account
	if node == nil || node.inactive || node.existing {
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
