// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// LocalAuthDenyPolicy describes a deny policy that requires disableLocalAuth to be true
// for a specific Azure resource type.
type LocalAuthDenyPolicy struct {
	// PolicyName is the display name of the policy assignment.
	PolicyName string
	// ResourceType is the Azure resource type targeted (e.g. "Microsoft.CognitiveServices/accounts").
	ResourceType string
	// FieldPath is the full field path checked (e.g. "Microsoft.CognitiveServices/accounts/disableLocalAuth").
	FieldPath string
}

// PolicyService queries Azure Policy assignments and definitions to detect
// policies that would block deployment of resources with local authentication enabled.
type PolicyService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

// NewPolicyService creates a new PolicyService.
func NewPolicyService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *PolicyService {
	return &PolicyService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

// FindLocalAuthDenyPolicies lists policy assignments on the subscription and inspects
// their definitions for deny-effect rules that require disableLocalAuth to be true.
// It returns a list of matching policies with their target resource types.
func (s *PolicyService) FindLocalAuthDenyPolicies(
	ctx context.Context,
	subscriptionId string,
) ([]LocalAuthDenyPolicy, error) {
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting credential for subscription %s: %w", subscriptionId, err)
	}

	assignmentsClient, err := armpolicy.NewAssignmentsClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating policy assignments client: %w", err)
	}

	definitionsClient, err := armpolicy.NewDefinitionsClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating policy definitions client: %w", err)
	}

	setDefinitionsClient, err := armpolicy.NewSetDefinitionsClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating policy set definitions client: %w", err)
	}

	// List all policy assignments for the subscription.
	var assignments []*armpolicy.Assignment
	pager := assignmentsClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing policy assignments: %w", err)
		}
		assignments = append(assignments, page.Value...)
	}

	var results []LocalAuthDenyPolicy

	for _, assignment := range assignments {
		if assignment.Properties == nil || assignment.Properties.PolicyDefinitionID == nil {
			continue
		}

		defID := *assignment.Properties.PolicyDefinitionID
		assignmentName := ""
		if assignment.Properties.DisplayName != nil {
			assignmentName = *assignment.Properties.DisplayName
		}

		// Resolve the assignment's parameter values so we can evaluate parameterized effects.
		assignmentParams := extractAssignmentParams(assignment)

		if isBuiltInPolicyDefinition(defID) || isCustomPolicyDefinition(defID) {
			// Single policy definition — check it directly.
			policies := s.checkPolicyDefinition(
				ctx, definitionsClient, defID, assignmentName, assignmentParams,
			)
			results = append(results, policies...)
		} else if isPolicySetDefinition(defID) {
			// Policy set (initiative) — enumerate its member definitions.
			policies := s.checkPolicySetDefinition(
				ctx, setDefinitionsClient, definitionsClient, defID, assignmentName, assignmentParams,
			)
			results = append(results, policies...)
		}
	}

	return results, nil
}

// checkPolicyDefinition fetches a single policy definition and inspects it for
// disableLocalAuth deny rules. Returns any matching policies found.
func (s *PolicyService) checkPolicyDefinition(
	ctx context.Context,
	client *armpolicy.DefinitionsClient,
	definitionID string,
	assignmentName string,
	assignmentParams map[string]any,
) []LocalAuthDenyPolicy {
	definition, err := getPolicyDefinitionByID(ctx, client, definitionID)
	if err != nil {
		log.Printf("policy preflight: could not fetch policy definition %s: %v", definitionID, err)
		return nil
	}

	return extractLocalAuthDenyPolicies(definition, assignmentName, assignmentParams)
}

// checkPolicySetDefinition fetches a policy set definition (initiative) and checks
// each of its member policy definitions for disableLocalAuth deny rules.
func (s *PolicyService) checkPolicySetDefinition(
	ctx context.Context,
	setClient *armpolicy.SetDefinitionsClient,
	defClient *armpolicy.DefinitionsClient,
	setDefinitionID string,
	assignmentName string,
	assignmentParams map[string]any,
) []LocalAuthDenyPolicy {
	setDef, err := getPolicySetDefinitionByID(ctx, setClient, setDefinitionID)
	if err != nil {
		log.Printf("policy preflight: could not fetch policy set definition %s: %v", setDefinitionID, err)
		return nil
	}

	if setDef.Properties == nil || setDef.Properties.PolicyDefinitions == nil {
		return nil
	}

	var results []LocalAuthDenyPolicy
	for _, member := range setDef.Properties.PolicyDefinitions {
		if member.PolicyDefinitionID == nil {
			continue
		}

		// Merge set-level parameters with member-level parameter values.
		memberParams := mergeParams(assignmentParams, member.Parameters)

		policies := s.checkPolicyDefinition(
			ctx, defClient, *member.PolicyDefinitionID, assignmentName, memberParams,
		)
		results = append(results, policies...)
	}

	return results
}

// getPolicyDefinitionByID fetches a policy definition by its full resource ID.
// It handles both built-in (/providers/Microsoft.Authorization/...) and subscription-scoped definitions.
func getPolicyDefinitionByID(
	ctx context.Context,
	client *armpolicy.DefinitionsClient,
	definitionID string,
) (*armpolicy.Definition, error) {
	name := lastSegment(definitionID)
	if name == "" {
		return nil, fmt.Errorf("invalid policy definition ID: %s", definitionID)
	}

	if isBuiltInPolicyDefinition(definitionID) {
		resp, err := client.GetBuiltIn(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.Definition, nil
	}

	resp, err := client.Get(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return &resp.Definition, nil
}

// getPolicySetDefinitionByID fetches a policy set definition by its full resource ID.
func getPolicySetDefinitionByID(
	ctx context.Context,
	client *armpolicy.SetDefinitionsClient,
	setDefinitionID string,
) (*armpolicy.SetDefinition, error) {
	name := lastSegment(setDefinitionID)
	if name == "" {
		return nil, fmt.Errorf("invalid policy set definition ID: %s", setDefinitionID)
	}

	if isBuiltInPolicySetDefinition(setDefinitionID) {
		resp, err := client.GetBuiltIn(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.SetDefinition, nil
	}

	resp, err := client.Get(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return &resp.SetDefinition, nil
}

// extractLocalAuthDenyPolicies inspects a policy definition for deny-effect rules
// that target disableLocalAuth fields. It returns any matching policies.
func extractLocalAuthDenyPolicies(
	def *armpolicy.Definition,
	assignmentName string,
	assignmentParams map[string]any,
) []LocalAuthDenyPolicy {
	if def.Properties == nil || def.Properties.PolicyRule == nil {
		return nil
	}

	ruleMap, ok := def.Properties.PolicyRule.(map[string]any)
	if !ok {
		return nil
	}

	// Check if the effect is "deny" (either literal or via parameter reference).
	if !isDenyEffect(ruleMap, def.Properties.Parameters, assignmentParams) {
		return nil
	}

	// Parse the "if" condition to find disableLocalAuth field references.
	ifBlock, ok := ruleMap["if"]
	if !ok {
		return nil
	}

	return findLocalAuthConditions(ifBlock, assignmentName)
}

// isDenyEffect checks whether the policy's effect resolves to "deny".
// Effects can be a literal string or a parameter reference like "[parameters('effect')]".
func isDenyEffect(
	ruleMap map[string]any,
	definitionParams map[string]*armpolicy.ParameterDefinitionsValue,
	assignmentParams map[string]any,
) bool {
	thenBlock, ok := ruleMap["then"]
	if !ok {
		return false
	}

	thenMap, ok := thenBlock.(map[string]any)
	if !ok {
		return false
	}

	effectVal, ok := thenMap["effect"]
	if !ok {
		return false
	}

	effectStr, ok := effectVal.(string)
	if !ok {
		return false
	}

	// Check for literal deny.
	if strings.EqualFold(effectStr, "deny") {
		return true
	}

	// Check for parameter reference: "[parameters('effect')]" or "[parameters('effectName')]".
	paramName := extractParameterReference(effectStr)
	if paramName == "" {
		return false
	}

	// First check assignment-level parameters (these override definition defaults).
	if v, ok := assignmentParams[paramName]; ok {
		if s, ok := v.(string); ok && strings.EqualFold(s, "deny") {
			return true
		}
		return false
	}

	// Fall back to the definition's default value.
	if definitionParams != nil {
		if paramDef, ok := definitionParams[paramName]; ok && paramDef.DefaultValue != nil {
			if s, ok := paramDef.DefaultValue.(string); ok && strings.EqualFold(s, "deny") {
				return true
			}
		}
	}

	return false
}

// findLocalAuthConditions traverses the policy rule's "if" block to find conditions
// that reference disableLocalAuth fields and extracts the target resource type.
func findLocalAuthConditions(ifBlock any, assignmentName string) []LocalAuthDenyPolicy {
	condMap, ok := ifBlock.(map[string]any)
	if !ok {
		return nil
	}

	// Check for allOf / anyOf compound conditions.
	if allOf, ok := condMap["allOf"]; ok {
		return findInCompoundCondition(allOf, assignmentName)
	}
	if anyOf, ok := condMap["anyOf"]; ok {
		return findInCompoundCondition(anyOf, assignmentName)
	}

	// Single condition — unlikely to be the full pattern but check anyway.
	return checkSingleCondition(condMap, assignmentName, "")
}

// findInCompoundCondition processes an allOf/anyOf array looking for conditions that
// reference both a resource type and a disableLocalAuth field.
func findInCompoundCondition(compound any, assignmentName string) []LocalAuthDenyPolicy {
	conditions, ok := compound.([]any)
	if !ok {
		return nil
	}

	// First pass: find the resource type from "field: type, equals: ..." conditions.
	resourceType := ""
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}

		// Handle nested allOf/anyOf.
		if allOf, ok := condMap["allOf"]; ok {
			if results := findInCompoundCondition(allOf, assignmentName); len(results) > 0 {
				return results
			}
		}
		if anyOf, ok := condMap["anyOf"]; ok {
			if results := findInCompoundCondition(anyOf, assignmentName); len(results) > 0 {
				return results
			}
		}

		fieldVal, _ := condMap["field"].(string)
		if strings.EqualFold(fieldVal, "type") {
			if eq, ok := condMap["equals"].(string); ok {
				resourceType = eq
			}
			if in, ok := condMap["in"].([]any); ok && len(in) > 0 {
				// Multiple resource types — take the first one for now.
				if s, ok := in[0].(string); ok {
					resourceType = s
				}
			}
		}
	}

	// Second pass: find disableLocalAuth field references.
	var results []LocalAuthDenyPolicy
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}
		results = append(results, checkSingleCondition(condMap, assignmentName, resourceType)...)

		// Also recurse into nested conditions.
		if allOf, ok := condMap["allOf"]; ok {
			results = append(results, findInCompoundCondition(allOf, assignmentName)...)
		}
		if anyOf, ok := condMap["anyOf"]; ok {
			results = append(results, findInCompoundCondition(anyOf, assignmentName)...)
		}
	}

	return results
}

// checkSingleCondition checks if a single condition references a disableLocalAuth field.
func checkSingleCondition(
	condMap map[string]any,
	assignmentName string,
	resourceType string,
) []LocalAuthDenyPolicy {
	fieldVal, ok := condMap["field"].(string)
	if !ok {
		return nil
	}

	if !isLocalAuthField(fieldVal) {
		return nil
	}

	// If we don't have the resource type from a sibling condition, try to derive it
	// from the field path (e.g. "Microsoft.Storage/storageAccounts/allowSharedKeyAccess").
	if resourceType == "" {
		resourceType = resourceTypeFromFieldPath(fieldVal)
	}

	if resourceType == "" {
		return nil
	}

	return []LocalAuthDenyPolicy{{
		PolicyName:   assignmentName,
		ResourceType: resourceType,
		FieldPath:    fieldVal,
	}}
}

// isLocalAuthField returns true if the field path references a local authentication property.
func isLocalAuthField(field string) bool {
	lower := strings.ToLower(field)
	return strings.HasSuffix(lower, "/disablelocalauth") ||
		strings.HasSuffix(lower, "/allowsharedkeyaccess") ||
		strings.HasSuffix(lower, "/localauthenabled") ||
		strings.EqualFold(field, "disableLocalAuth") ||
		strings.EqualFold(field, "allowSharedKeyAccess")
}

// resourceTypeFromFieldPath extracts the resource type from a fully qualified field path.
// For example, "Microsoft.CognitiveServices/accounts/disableLocalAuth" returns
// "Microsoft.CognitiveServices/accounts".
func resourceTypeFromFieldPath(field string) string {
	idx := strings.LastIndex(field, "/")
	if idx <= 0 {
		return ""
	}
	candidate := field[:idx]
	// Basic validation: resource types contain at least one "/".
	if !strings.Contains(candidate, "/") {
		return ""
	}
	return candidate
}

// extractParameterReference extracts the parameter name from an ARM template parameter
// reference expression like "[parameters('effect')]". Returns empty if not a parameter reference.
func extractParameterReference(expr string) string {
	lower := strings.ToLower(strings.TrimSpace(expr))
	if !strings.HasPrefix(lower, "[parameters('") || !strings.HasSuffix(lower, "')]") {
		return ""
	}
	// Extract between [parameters(' and ')]
	inner := expr[len("[parameters('"):]
	before, _, ok := strings.Cut(inner, "')]")
	if !ok {
		return ""
	}
	return before
}

// extractAssignmentParams extracts parameter values from a policy assignment into a simple map.
func extractAssignmentParams(assignment *armpolicy.Assignment) map[string]any {
	if assignment.Properties == nil || assignment.Properties.Parameters == nil {
		return nil
	}

	params := make(map[string]any, len(assignment.Properties.Parameters))
	for name, val := range assignment.Properties.Parameters {
		if val != nil && val.Value != nil {
			params[name] = val.Value
		}
	}
	return params
}

// mergeParams merges assignment-level parameters with member-level parameter values
// from a policy set definition reference. Member parameters may contain parameter
// references like "[parameters('effect')]" that resolve against the assignment parameters.
func mergeParams(assignmentParams map[string]any, memberParams map[string]*armpolicy.ParameterValuesValue) map[string]any {
	if len(memberParams) == 0 {
		return assignmentParams
	}

	merged := make(map[string]any, len(assignmentParams)+len(memberParams))
	maps.Copy(merged, assignmentParams)

	for name, val := range memberParams {
		if val == nil || val.Value == nil {
			continue
		}

		// Check if the member parameter value is itself a reference to an assignment parameter.
		if s, ok := val.Value.(string); ok {
			if refName := extractParameterReference(s); refName != "" {
				if resolved, ok := assignmentParams[refName]; ok {
					merged[name] = resolved
					continue
				}
			}
		}

		merged[name] = val.Value
	}

	return merged
}

// isBuiltInPolicyDefinition returns true if the definition ID is a built-in policy.
func isBuiltInPolicyDefinition(id string) bool {
	return strings.HasPrefix(strings.ToLower(id), "/providers/microsoft.authorization/policydefinitions/")
}

// isCustomPolicyDefinition returns true if the definition ID is a subscription-scoped custom policy.
func isCustomPolicyDefinition(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(lower, "/providers/microsoft.authorization/policydefinitions/") &&
		!isBuiltInPolicyDefinition(id)
}

// isPolicySetDefinition returns true if the definition ID references a policy set (initiative).
func isPolicySetDefinition(id string) bool {
	return strings.Contains(strings.ToLower(id), "/providers/microsoft.authorization/policysetdefinitions/")
}

// isBuiltInPolicySetDefinition returns true if the set definition ID is a built-in policy set.
func isBuiltInPolicySetDefinition(id string) bool {
	return strings.HasPrefix(strings.ToLower(id), "/providers/microsoft.authorization/policysetdefinitions/")
}

// lastSegment returns the last path segment of a resource ID.
func lastSegment(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ResourceHasLocalAuthDisabled checks whether a resource's properties JSON has
// the disableLocalAuth property set to true (or allowSharedKeyAccess set to false
// for storage accounts).
func ResourceHasLocalAuthDisabled(resourceType string, properties json.RawMessage) bool {
	if len(properties) == 0 {
		return false
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return false
	}

	// Storage accounts use allowSharedKeyAccess (inverted logic).
	if strings.EqualFold(resourceType, "Microsoft.Storage/storageAccounts") {
		if v, ok := props["allowSharedKeyAccess"]; ok {
			if b, ok := v.(bool); ok {
				return !b // allowSharedKeyAccess=false means local auth is disabled
			}
		}
		return false
	}

	// Most resource types use disableLocalAuth.
	if v, ok := props["disableLocalAuth"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}

	return false
}
