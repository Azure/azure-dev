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

func (c *accountCandidate) evaluateName() {
	if c.nameEvaluated {
		return
	}
	c.name, c.nameMissing = planString(c.node.env.value(c.node.raw["name"]))
	c.nameEvaluated = true
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
	if unknown, ok := properties.(planUnknown); ok {
		c.configs = append(c.configs, hostingConfigCandidate{
			unresolved: node.path + ": " + unknown.reason,
		})
		return
	}
	object, ok := properties.(map[string]any)
	if !ok {
		c.configs = append(c.configs, hostingConfigCandidate{
			unresolved: node.path + ": account properties are not a locally resolved object",
		})
		return
	}
	configs, hasConfigs := planObjectField(object, "agentHostingConfigurations")
	if !hasConfigs || configs == nil {
		c.nonHosting = true
		return
	}
	if unknown, ok := configs.(planUnknown); ok {
		c.configs = append(c.configs, hostingConfigCandidate{
			unresolved: node.path + "/properties.agentHostingConfigurations: " + unknown.reason,
		})
		return
	}
	values, ok := configs.([]any)
	if !ok {
		c.configs = append(c.configs, hostingConfigCandidate{
			unresolved: node.path + "/properties.agentHostingConfigurations: value is not a locally resolved array",
		})
		return
	}
	for i, item := range values {
		itemOrigin := fmt.Sprintf("%s/properties.agentHostingConfigurations[%d]", node.path, i)
		if unknown, ok := item.(planUnknown); ok {
			c.configs = append(c.configs, hostingConfigCandidate{
				unresolved: itemOrigin + ": " + unknown.reason,
			})
			continue
		}
		configMap, ok := item.(map[string]any)
		if !ok {
			c.configs = append(c.configs, hostingConfigCandidate{
				unresolved: itemOrigin + ": configuration entry is not a locally resolved object",
			})
			continue
		}
		clusterField, hasCluster := planObjectField(configMap, "clusterResourceId")
		id, idMissing := "", ""
		if hasCluster && clusterField != nil {
			id, idMissing = planString(clusterField)
		}
		typeField, hasType := planObjectField(configMap, "hostingType")
		hostingType, typeMissing := "", "object has no hostingType property"
		typeUnknown := !hasType
		if hasType {
			_, isUnknown := typeField.(planUnknown)
			_, isPartial := typeField.(planPartialID)
			typeUnknown = isUnknown || isPartial
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
		if strings.EqualFold(hostingType, "ManagedCluster") {
			c.configs = append(c.configs, hostingConfigCandidate{config: configMap})
			continue
		}
		if idMissing != "" {
			c.configs = append(c.configs, hostingConfigCandidate{
				unresolved: itemOrigin + ": " + idMissing,
			})
			continue
		}
		if typeUnknown && hasType {
			c.configs = append(c.configs, hostingConfigCandidate{
				unresolved: itemOrigin + ": " + typeMissing,
			})
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
		rawValue, hasValue := planObjectField(definition, "value")
		if !hasValue {
			return "", "AZURE_AI_ACCOUNT_NAME: output has no local value"
		}
		value := root.value(rawValue)
		if value == nil {
			return "", ""
		}
		name, missing := planString(value)
		if missing != "" {
			return "", "AZURE_AI_ACCOUNT_NAME: " + missing
		}
		return name, ""
	}
	name, missing := planString(root.value(raw))
	if missing != "" {
		return "", "AZURE_AI_ACCOUNT_NAME: " + missing
	}
	return name, ""
}

func setMissingBYOResources(plan *onboarding.Plan, scopePath, reason string) {
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
}

func (r *templatePlanResolver) bindResources(plan *onboarding.Plan, root *planEnvironment) {
	outputName, outputUnresolved := resolveAccountNameOutput(root)
	var allAccounts []accountCandidate
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, "Microsoft.CognitiveServices/accounts") {
			continue
		}
		allAccounts = append(allAccounts, accountCandidate{node: node})
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
	collectCandidateReasons := func(candidate *accountCandidate) {
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

	if outputName != "" {
		var exactMatches []*accountCandidate
		var unknownName []*accountCandidate
		for i := range allAccounts {
			candidate := &allAccounts[i]
			candidate.evaluateName()
			if candidate.nameMissing != "" {
				unknownName = append(unknownName, candidate)
				continue
			}
			if strings.EqualFold(candidate.name, outputName) {
				exactMatches = append(exactMatches, candidate)
			}
		}
		switch {
		case len(exactMatches) == 1 && len(unknownName) == 0 && len(incompleteAccounts) == 0:
			exactMatches[0].evaluateHosting()
			selectedAccount = exactMatches[0]
		case len(exactMatches) == 0:
			appendReason(fmt.Sprintf("AZURE_AI_ACCOUNT_NAME %q did not match a local Foundry account", outputName))
			for _, candidate := range unknownName {
				collectCandidateReasons(candidate)
			}
			appendReason(incompleteAccounts...)
		default:
			appendReason(fmt.Sprintf(
				"AZURE_AI_ACCOUNT_NAME %q matched multiple or ambiguous Foundry account candidates", outputName))
			for _, candidate := range exactMatches {
				collectCandidateReasons(candidate)
			}
			for _, candidate := range unknownName {
				collectCandidateReasons(candidate)
			}
			appendReason(incompleteAccounts...)
		}
	} else {
		var hostingAccounts []*accountCandidate
		for i := range allAccounts {
			candidate := &allAccounts[i]
			candidate.evaluateHosting()
			if !candidate.nonHosting {
				hostingAccounts = append(hostingAccounts, candidate)
			}
		}
		if len(hostingAccounts) == 1 && len(incompleteAccounts) == 0 {
			selectedAccount = hostingAccounts[0]
		} else {
			appendReason(outputUnresolved)
			for _, candidate := range hostingAccounts {
				collectCandidateReasons(candidate)
			}
			appendReason(incompleteAccounts...)
		}
	}

	if selectedAccount == nil {
		reason := "no unique local Foundry account/agentHostingConfigurations association"
		if len(reasons) > 0 {
			reason += ": " + strings.Join(reasons, "; ")
		}
		setMissingBYOResources(plan, root.path, reason)
		plan.AgentSubnet = onboarding.Resource{
			Origin: "Microsoft.Network/virtualNetworks/subnets at " + root.path, Missing: reason,
		}
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
		setMissingBYOResources(plan, selectedAccount.node.path, reason)
		plan.AgentSubnet = onboarding.Resource{
			Origin: "Microsoft.Network/virtualNetworks/subnets at " + selectedAccount.node.path, Missing: reason,
		}
		return
	}
	if selectedAccount.node.missing != "" {
		var cfgReasons []string
		for _, cfg := range selectedAccount.configs {
			if cfg.unresolved != "" && !slices.Contains(cfgReasons, cfg.unresolved) {
				cfgReasons = append(cfgReasons, cfg.unresolved)
			}
		}
		reason := "no unique local Foundry account/agentHostingConfigurations association at " +
			selectedAccount.node.path
		if len(cfgReasons) > 0 {
			reason += ": " + strings.Join(cfgReasons, "; ")
		}
		reason += "; Foundry account selection: " + selectedAccount.node.missing
		setMissingBYOResources(plan, selectedAccount.node.path, reason)
		plan.AgentSubnet = onboarding.Resource{
			Origin: "Microsoft.Network/virtualNetworks/subnets at " + selectedAccount.node.path, Missing: reason,
		}
		return
	}

	r.account = selectedAccount.node
	if len(selectedAccount.configs) != 1 || selectedAccount.configs[0].unresolved != "" {
		var cfgReasons []string
		for _, cfg := range selectedAccount.configs {
			if cfg.unresolved != "" && !slices.Contains(cfgReasons, cfg.unresolved) {
				cfgReasons = append(cfgReasons, cfg.unresolved)
			}
		}
		reason := "no unique local Foundry account/agentHostingConfigurations association"
		if len(selectedAccount.configs) == 1 && len(cfgReasons) == 1 {
			reason += ": " + cfgReasons[0]
		} else if len(cfgReasons) > 0 {
			reason += " at " + selectedAccount.node.path + ": " + strings.Join(cfgReasons, "; ")
		} else {
			reason += " at " + selectedAccount.node.path
		}
		setMissingBYOResources(plan, selectedAccount.node.path, reason)
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
			singleHosting := true
			for i := range allAccounts {
				if &allAccounts[i] == selectedAccount {
					continue
				}
				allAccounts[i].evaluateHosting()
				if !allAccounts[i].nonHosting {
					singleHosting = false
					break
				}
			}
			r.singleAccountHosting = singleHosting
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
