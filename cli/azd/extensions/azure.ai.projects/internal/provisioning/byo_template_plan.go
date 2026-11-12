// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore ARMID managedclusters managedidentity storageaccounts userassignedidentities

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type templatePlanResolver struct {
	ctx                 context.Context
	provider            *FoundryProvisioningProvider
	evidence            *onboarding.AuthorizationEvidence
	scopes              map[string]planScope
	functions           map[string]any
	references          map[*planResource]bool
	owningSubscriptions map[string]bool
	nodes               []*planResource
	account             *planResource
	unresolved          []string
	err                 error
}

type planResource struct {
	raw       map[string]any
	env       *planEnvironment
	resolver  *templatePlanResolver
	path      string
	kind      string
	inactive  bool
	missing   string
	child     *planEnvironment
	childRead bool
}

func (r *templatePlanResolver) scope(subscription, group string) planScope {
	key := strings.ToLower(subscription + "/" + group)
	if scope, present := r.scopes[key]; present {
		return scope
	}
	scope := planScope{subscriptionID: subscription, group: group}
	if subscription == "" || strings.ContainsAny(subscription+group, "/\\?#%@:\x00 \t\r\n") {
		scope.missing = "template deployment subscription/resource-group scope is not resolved"
		r.err = errors.Join(r.err, fmt.Errorf("resource-plan: actual template subscription/resource-group scope is invalid"))
		return scope
	}
	subKey := strings.ToLower(subscription + "/")
	subscriptionScope, found := r.scopes[subKey]
	if !found {
		response, err := r.evidence.GetSubscription(r.ctx, subscription)
		if err == nil {
			var result armsubscriptions.Subscription
			err = runtime.UnmarshalAsJSON(response, &result)
			if err == nil {
				if result.ID == nil || !strings.EqualFold(*result.ID, "/subscriptions/"+subscription) {
					err = fmt.Errorf(
						"resource-refs: subscription lookup did not return the actual selected subscription ARM ID")
				} else {
					subscriptionScope = planScope{subscriptionID: subscription, subscriptionARMID: *result.ID}
				}
			}
		}
		if err != nil {
			r.err = errors.Join(r.err, fmt.Errorf("resource-refs: read actual template subscription scope: %w", err))
			subscriptionScope.missing = "actual subscription scope could not be read"
		}
		r.scopes[subKey] = subscriptionScope
	}
	scope.subscriptionARMID = subscriptionScope.subscriptionARMID
	scope.missing = subscriptionScope.missing
	if group == "" || scope.missing != "" {
		return scope
	}
	scope.groupARMID = scope.subscriptionARMID + "/resourceGroups/" + group
	client, err := armresources.NewResourceGroupsClient(subscription, r.provider.credential,
		byoARMOptions(r.provider.armClientOptions))
	if err == nil {
		var response armresources.ResourceGroupsClientGetResponse
		response, err = client.Get(r.ctx, group, nil)
		if err == nil {
			if response.ID == nil || !strings.EqualFold(*response.ID, scope.groupARMID) {
				err = fmt.Errorf(
					"resource-refs: resource-group lookup did not return the actual selected resource-group ARM ID")
			} else {
				scope.groupARMID = *response.ID
				scope.groupExists = true
				if response.Location != nil {
					scope.location = *response.Location
				}
			}
		}
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok && response.StatusCode == http.StatusNotFound {
		err = nil
	}
	if err != nil {
		r.err = errors.Join(r.err, fmt.Errorf("resource-refs: read actual template resource-group scope: %w", err))
		scope.missing = "actual resource-group scope could not be read"
	}
	r.scopes[key] = scope
	return scope
}

func (r *templatePlanResolver) function(name string, args []string) any {
	raw, err := json.Marshal(args)
	if err != nil {
		r.err = errors.Join(r.err, err)
		return planUnknown{"local Bicep arguments could not be encoded"}
	}
	key := name + string(raw)
	if value, present := r.functions[key]; present {
		return value
	}
	value, err := evaluateBYOPlanFunction(r.ctx, r.provider.bicepCli(), name, args)
	if err != nil {
		r.err = errors.Join(r.err, err)
		unknown := planUnknown{"azd-managed Bicep could not evaluate " + name + ": " + err.Error()}
		r.functions[key] = unknown
		return unknown
	}
	r.functions[key] = value
	return value
}

func (r *templatePlanResolver) environment(
	template map[string]any, parameters map[string]func() any, scope planScope, path string, deployment any,
) *planEnvironment {
	return &planEnvironment{
		template: template, parameters: parameters, scope: scope, path: path,
		deployment: deployment, function: r.function,
		variables: map[string]any{}, active: map[string]bool{}, resources: map[string]*planResource{},
	}
}

func (r *templatePlanResolver) walk(env *planEnvironment, inheritedMissing string, depth int) {
	if env.walked {
		return
	}
	env.walked = true
	if depth > 32 || len(r.nodes) > 2048 {
		r.unresolved = append(r.unresolved, env.path+": local module/resource enumeration limit reached")
		return
	}
	var entries []struct {
		symbol string
		raw    map[string]any
	}
	var modules []*planResource
	switch resources := env.template["resources"].(type) {
	case map[string]any:
		for _, symbol := range slices.Sorted(maps.Keys(resources)) {
			if resource, ok := resources[symbol].(map[string]any); ok {
				entries = append(entries, struct {
					symbol string
					raw    map[string]any
				}{symbol, resource})
			}
		}
	case []any:
		for index, value := range resources {
			if resource, ok := value.(map[string]any); ok {
				entries = append(entries, struct {
					symbol string
					raw    map[string]any
				}{fmt.Sprintf("resources[%d]", index), resource})
			}
		}
	}
	for _, entry := range entries {
		count := 1
		copyMissing := ""
		if copy, ok := entry.raw["copy"].(map[string]any); ok {
			value := env.value(copy["count"])
			number, ok := value.(float64)
			if !ok || number < 0 || number > 128 || number != float64(int(number)) {
				copyMissing = "resource copy count cannot be resolved within the local plan limit (128)"
			} else {
				count = int(number)
			}
		}
		for index := range count {
			if len(r.nodes) >= 2048 {
				r.unresolved = append(r.unresolved, env.path+": local module/resource enumeration limit reached")
				return
			}
			local := *env
			local.copyIndex = index
			local.variables, local.active = map[string]any{}, map[string]bool{}
			path := env.path + "/" + entry.symbol
			if count > 1 {
				path += fmt.Sprintf("[%d]", index)
			}
			resource := &planResource{
				raw: entry.raw, env: &local, resolver: r, path: path,
				missing: strings.Join(slices.DeleteFunc([]string{inheritedMissing, copyMissing},
					func(s string) bool { return s == "" }), "; "),
			}
			var typeMissing string
			resource.kind, typeMissing = planString(local.value(entry.raw["type"]))
			if condition, present := entry.raw["condition"]; present {
				value, missing := planBool(local.value(condition))
				if missing != "" {
					resource.missing = strings.Trim(resource.missing+"; creation condition: "+missing, "; ")
				} else {
					resource.inactive = !value
				}
			}
			if existing, _ := entry.raw["existing"].(bool); existing {
				resource.inactive = true
			}
			if !resource.inactive && typeMissing != "" {
				r.unresolved = append(r.unresolved, path+": resource type: "+typeMissing)
			}
			env.resources[entry.symbol] = resource
			r.nodes = append(r.nodes, resource)
			if !resource.inactive && strings.EqualFold(resource.kind, "Microsoft.Resources/deployments") {
				modules = append(modules, resource)
			}
		}
	}
	// Install all symbolic resources before resolving module-output references.
	for _, resource := range modules {
		if child := resource.module(); child != nil {
			r.walk(child, resource.missing, depth+1)
		}
	}
}

func (n *planResource) module() *planEnvironment {
	if n.childRead {
		return n.child
	}
	n.childRead = true
	properties, ok := n.raw["properties"].(map[string]any)
	if !ok {
		n.missing = "deployment module properties are not an inline object"
		return nil
	}
	template, ok := properties["template"].(map[string]any)
	if !ok {
		n.missing = "linked or runtime-generated deployment template is not locally available"
		n.resolver.unresolved = append(n.resolver.unresolved, n.path+": "+n.missing)
		return nil
	}
	evaluation, hasEvaluation := properties["expressionEvaluationOptions"]
	options, validEvaluation := evaluation.(map[string]any)
	evaluationScope, hasScope := options["scope"]
	if !hasScope && (!hasEvaluation || validEvaluation) && n.env.template["languageVersion"] == "2.0" {
		evaluationScope = "inner"
	}
	if evaluationScope != "inner" {
		n.missing = "nested template does not use supported inner expression evaluation"
		n.resolver.unresolved = append(n.resolver.unresolved, n.path+": "+n.missing)
		return nil
	}
	scope := n.env.scope
	if schema, _ := template["$schema"].(string); strings.Contains(schema, "subscriptionDeploymentTemplate") {
		scope.group, scope.groupARMID = "", ""
		scope.groupExists = false
	}
	if raw, present := n.raw["subscriptionId"]; present {
		subscription, missing := planString(n.env.value(raw))
		if missing != "" {
			scope.missing = "module subscription: " + missing
			scope.subscriptionID, scope.subscriptionARMID, scope.groupARMID = "", "", ""
			scope.groupExists = false
		} else {
			scope.subscriptionID = subscription
		}
	}
	if raw, present := n.raw["resourceGroup"]; present {
		group, missing := planString(n.env.value(raw))
		if missing != "" {
			scope.missing = "module resource group: " + missing
			scope.group, scope.groupARMID = "", ""
			scope.groupExists = false
		} else {
			scope.group = group
		}
	}
	if scope.missing == "" {
		scope = n.resolver.scope(scope.subscriptionID, scope.group)
		if scope.location == "" && !scope.groupExists && scope.groupARMID != "" {
			for _, resource := range n.env.resources {
				if resource.inactive || resource.missing != "" ||
					!strings.EqualFold(resource.kind, "Microsoft.Resources/resourceGroups") {
					continue
				}
				if id, missing := resource.id(); missing == "" && strings.EqualFold(id, scope.groupARMID) {
					scope.location, _ = planString(resource.env.value(resource.raw["location"]))
				}
			}
		}
	}
	parameters := map[string]func() any{}
	if supplied, ok := properties["parameters"].(map[string]any); ok {
		for key, envelope := range supplied {
			parameters[strings.ToLower(key)] = func() any {
				value := n.env.value(envelope)
				return planField(value, "value")
			}
		}
	}
	n.child = n.resolver.environment(template, parameters, scope, n.path, n.env.value(n.raw["name"]))
	return n.child
}

func (n *planResource) resourceScope() (string, string) {
	if n.env.scope.missing != "" {
		return "", n.env.scope.missing
	}
	if strings.EqualFold(n.kind, "Microsoft.Resources/resourceGroups") {
		return n.env.scope.subscriptionARMID, ""
	}
	scope := n.env.scope.groupARMID
	if raw, present := n.raw["scope"]; present {
		value, why := planString(n.env.value(raw))
		if why != "" {
			return "", "resource scope: " + why
		}
		if strings.HasPrefix(value, "/") {
			scope = value
		} else {
			scope += "/providers/" + value
		}
	}
	if scope == "" {
		return "", "resource type or deployment resource-group scope is not resolved"
	}
	return scope, ""
}

func (n *planResource) id() (string, string) {
	name, missing := planString(n.env.value(n.raw["name"]))
	if missing != "" {
		return "", "resource name: " + missing
	}
	scope, missing := n.resourceScope()
	if missing != "" {
		return "", missing
	}
	if strings.EqualFold(n.kind, "Microsoft.Resources/resourceGroups") {
		return scope + "/resourceGroups/" + name, ""
	}
	if n.kind == "" {
		return "", "resource type or deployment resource-group scope is not resolved"
	}
	types, names := strings.Split(n.kind, "/"), strings.Split(name, "/")
	if len(types)-1 != len(names) {
		return "", "resource type/name hierarchy is not resolved"
	}
	var result strings.Builder
	result.WriteString(scope + "/providers/" + types[0])
	for i, part := range names {
		result.WriteString("/" + types[i+1] + "/" + part)
	}
	return result.String(), ""
}

func (n *planResource) reference(full bool) any {
	if n.resolver.references[n] {
		return planUnknown{"cyclic local resource/module reference at " + n.path}
	}
	n.resolver.references[n] = true
	defer delete(n.resolver.references, n)
	if strings.EqualFold(n.kind, "Microsoft.Resources/deployments") {
		child := n.module()
		if child == nil {
			return planUnknown{n.missing}
		}
		if len(child.resources) == 0 {
			// Output evaluation can precede the main walk (e.g. one module uses another's output).
			n.resolver.walk(child, n.missing, 1)
		}
		outputs, _ := child.template["outputs"].(map[string]any)
		result := map[string]any{}
		for key, output := range outputs {
			result[key] = child.value(output)
		}
		properties := map[string]any{"outputs": result}
		if full {
			return map[string]any{"properties": properties}
		}
		return properties
	}
	id, missing := n.id()
	if missing != "" {
		return planUnknown{missing}
	}
	properties := planUnknown{"runtime resource properties are not local template values"}
	if !full {
		return properties
	}
	return map[string]any{
		"id":         id,
		"properties": properties,
		"location":   planUnknown{"runtime resource location is not a local template value"},
	}
}

func (r *templatePlanResolver) resource(value any, kind, origin string) onboarding.Resource {
	id, missing := planString(value)
	result := onboarding.Resource{ID: id, Origin: kind + " at " + origin, Missing: missing}
	if missing != "" {
		if partial, ok := value.(planPartialID); ok {
			if !strings.EqualFold(partial.kind, kind) {
				r.err = errors.Join(r.err,
					fmt.Errorf("resource-refs: actual template resource type differs from expected %s", kind))
				return result
			}
			if strings.ContainsAny(partial.scope, "?#%\\@:\x00 \t\r\n") {
				r.err = errors.Join(r.err, fmt.Errorf("resource-refs: actual template resource scope is malformed"))
				return result
			}
			if scope, err := arm.ParseResourceID(partial.scope); err == nil && scope.SubscriptionID != "" {
				result.Parent = partial.scope
				r.recordOwningSubscription(kind, scope.SubscriptionID)
				if strings.EqualFold(kind, "Microsoft.Network/virtualNetworks/subnets") &&
					len(partial.components) > 2 {
					var prefix []string
					for _, component := range partial.components[:len(partial.components)-2] {
						part, missing := planString(component)
						if missing != "" {
							prefix = nil
							break
						}
						prefix = append(prefix, part)
					}
					parent := strings.Join(prefix, "/")
					if _, err := onboarding.ParseID(parent, "Microsoft.Network/virtualNetworks"); err == nil {
						result.Parent = parent
					}
				}
			} else if partial.scope != "" {
				r.err = errors.Join(r.err, fmt.Errorf("resource-refs: actual template resource scope is malformed"))
			}
		} else if _, unknown := value.(planUnknown); !unknown {
			r.err = errors.Join(r.err, fmt.Errorf("resource-refs: actual template %s resource ID must be a string", kind))
		}
		return result
	}
	parsed, err := onboarding.ParseID(id, kind)
	if err != nil {
		r.err = errors.Join(r.err, fmt.Errorf("resource-refs: actual template %s: %w", kind, err))
		return result
	}
	result.Parent = parsed.Parent.String()
	r.recordOwningSubscription(kind, parsed.SubscriptionID)
	parentScope := parsed.Parent
	for range strings.Count(kind, "/") - 1 {
		parentScope = parentScope.Parent
	}
	uncertain := strings.Join(r.unresolved, "; ")
	for _, node := range r.nodes {
		if node.inactive || !strings.EqualFold(node.kind, kind) {
			continue
		}
		if _, override := node.raw["scope"]; !override {
			if node.env.scope.subscriptionID != "" &&
				!strings.EqualFold(node.env.scope.subscriptionID, parsed.SubscriptionID) {
				continue
			}
			if node.env.scope.group != "" && !strings.EqualFold(node.env.scope.group, parsed.ResourceGroupName) {
				continue
			}
		}
		if scope, why := node.resourceScope(); why == "" && !strings.EqualFold(scope, parentScope.String()) {
			continue
		}
		actual, why := node.id()
		if why != "" {
			uncertain = node.path + ": " + why
			continue
		}
		if !strings.EqualFold(actual, id) {
			continue
		}
		if node.missing != "" {
			uncertain = node.path + ": " + node.missing
			continue
		}
		if node.env.scope.groupExists {
			result.Parent = node.env.scope.groupARMID
		} else {
			result.Parent = node.env.scope.subscriptionARMID
		}
		return result
	}
	if uncertain != "" {
		result.LifecycleMissing = "cannot determine creation/reference selection: " + uncertain
	} else {
		result.Supplied = true
	}
	return result
}

func (r *templatePlanResolver) recordOwningSubscription(kind, subscription string) {
	switch strings.ToLower(kind) {
	case "microsoft.containerservice/managedclusters", "microsoft.storage/storageaccounts",
		"microsoft.managedidentity/userassignedidentities":
		r.owningSubscriptions[strings.ToLower(subscription)] = true
	}
}

func (p *FoundryProvisioningProvider) identifyBYOTemplatePlan(
	ctx context.Context, source *templateSource, evidence *onboarding.AuthorizationEvidence,
) (onboarding.Plan, error) {
	if evidence == nil {
		evidence = onboarding.NewAuthorizationEvidence(p.credential, p.armClientOptions)
	}
	resolver := &templatePlanResolver{
		ctx: ctx, provider: p, evidence: evidence, scopes: map[string]planScope{}, functions: map[string]any{},
		references: map[*planResource]bool{}, owningSubscriptions: map[string]bool{},
	}
	encoded, err := json.Marshal(source.parameters)
	if err != nil {
		return onboarding.Plan{}, fmt.Errorf("encode actual merged BYO plan parameters: %w", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(encoded, &merged); err != nil {
		return onboarding.Plan{}, fmt.Errorf("decode actual merged BYO plan parameters: %w", err)
	}
	parameters := map[string]func() any{}
	for key, envelope := range merged {
		parameters[strings.ToLower(key)] = func() any {
			if value, ok := envelope.(map[string]any); ok {
				if literal, present := value["value"]; present {
					return literal
				}
			}
			return planUnknown{"parameter " + key + " requires a runtime parameter reference"}
		}
	}
	scope := resolver.scope(p.subID, "")
	env := resolver.environment(source.armTemplate, parameters, scope, "compiled template", p.deploymentName())
	resolver.walk(env, "", 0)
	plan := onboarding.Plan{SubscriptionID: p.subID}
	resolver.owningSubscriptions[strings.ToLower(p.subID)] = true
	resolver.bindResources(&plan, env)
	_, missing := planString(env.parameter("resourceGroupName"))
	if resolver.account != nil {
		missing = resolver.account.env.scope.missing
		if subscription := resolver.account.env.scope.subscriptionID; subscription != "" {
			plan.SubscriptionID = subscription
			resolver.owningSubscriptions[strings.ToLower(subscription)] = true
		} else {
			missing = strings.Trim(missing+"; actual Foundry account subscription is not resolved", "; ")
			plan.SkippedChecks = append(plan.SkippedChecks, onboarding.SkippedCheck("resource-refs",
				resolver.account.path, "Foundry account subscription owning tenant", missing))
		}
	}
	if missing != "" {
		plan.SkippedChecks = append(plan.SkippedChecks, onboarding.SkippedCheck("resource-plan", env.path,
			"actual Foundry target resource-group scope", "actual Foundry target resource-group selection: "+missing))
	}
	resolver.bindNetwork(&plan)
	plan.OwningSubscriptions = append(plan.OwningSubscriptions, slices.Sorted(maps.Keys(resolver.owningSubscriptions))...)
	return plan, resolver.err
}
