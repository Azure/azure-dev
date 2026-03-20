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

// DenyPolicy describes a deny-effect policy that targets a specific field on
// an Azure resource type (e.g. disableLocalAuth, allowSharedKeyAccess).
type DenyPolicy struct {
	// PolicyName is the display name of the policy assignment.
	PolicyName string
	// ResourceType is the Azure resource type targeted
	// (e.g. "Microsoft.CognitiveServices/accounts").
	ResourceType string
	// FieldPath is the full field path checked
	// (e.g. "Microsoft.CognitiveServices/accounts/disableLocalAuth").
	FieldPath string
}

// PolicyService queries Azure Policy assignments and definitions to detect
// deny-effect policies that would block resource deployment.
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

// FindDenyPolicies lists policy assignments on the subscription (including
// inherited assignments from management groups) and inspects their definitions
// for deny-effect rules that target resource property fields.
// It returns a list of matching policies with their target resource types.
func (s *PolicyService) FindDenyPolicies(
	ctx context.Context,
	subscriptionId string,
) ([]DenyPolicy, error) {
	credential, err := s.credentialProvider.CredentialForSubscription(
		ctx, subscriptionId,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"getting credential for subscription %s: %w",
			subscriptionId, err,
		)
	}

	assignmentsClient, err := armpolicy.NewAssignmentsClient(
		subscriptionId, credential, s.armClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"creating policy assignments client: %w", err,
		)
	}

	definitionsClient, err := armpolicy.NewDefinitionsClient(
		subscriptionId, credential, s.armClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"creating policy definitions client: %w", err,
		)
	}

	setDefinitionsClient, err := armpolicy.NewSetDefinitionsClient(
		subscriptionId, credential, s.armClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"creating policy set definitions client: %w", err,
		)
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
	log.Printf(
		"policy preflight: found %d policy assignments for subscription %s",
		len(assignments), subscriptionId,
	)

	var results []DenyPolicy

	// Cache fetched definitions/set definitions to avoid duplicate API
	// calls when multiple assignments reference the same definition.
	defCache := make(map[string]*armpolicy.Definition)
	setDefCache := make(map[string]*armpolicy.SetDefinition)

	for _, assignment := range assignments {
		if assignment.Properties == nil ||
			assignment.Properties.PolicyDefinitionID == nil {
			continue
		}

		defID := *assignment.Properties.PolicyDefinitionID
		assignmentName := ""
		if assignment.Properties.DisplayName != nil {
			assignmentName = *assignment.Properties.DisplayName
		}

		// Resolve the assignment's parameter values so we can evaluate
		// parameterized effects.
		assignmentParams := extractAssignmentParams(assignment)

		if isBuiltInPolicyDefinition(defID) ||
			isCustomPolicyDefinition(defID) {
			policies := s.checkPolicyDefinition(
				ctx, definitionsClient, defID, assignmentName,
				assignmentParams, defCache,
			)
			results = append(results, policies...)
		} else if isPolicySetDefinition(defID) {
			policies := s.checkPolicySetDefinition(
				ctx, setDefinitionsClient, definitionsClient,
				defID, assignmentName, assignmentParams,
				defCache, setDefCache,
			)
			results = append(results, policies...)
		}
	}

	return results, nil
}

// checkPolicyDefinition fetches a single policy definition (using cache) and
// inspects it for deny rules targeting resource property fields.
func (s *PolicyService) checkPolicyDefinition(
	ctx context.Context,
	client *armpolicy.DefinitionsClient,
	definitionID string,
	assignmentName string,
	assignmentParams map[string]any,
	cache map[string]*armpolicy.Definition,
) []DenyPolicy {
	definition, ok := cache[definitionID]
	if !ok {
		var err error
		definition, err = getPolicyDefinitionByID(
			ctx, client, definitionID,
		)
		if err != nil {
			log.Printf(
				"policy preflight: could not fetch policy definition %s: %v",
				definitionID, err,
			)
			cache[definitionID] = nil
			return nil
		}
		cache[definitionID] = definition
	}
	if definition == nil {
		return nil
	}

	return extractDenyPolicies(
		definition, assignmentName, assignmentParams,
	)
}

// checkPolicySetDefinition fetches a policy set definition (initiative) and
// checks each of its member policy definitions for deny rules.
func (s *PolicyService) checkPolicySetDefinition(
	ctx context.Context,
	setClient *armpolicy.SetDefinitionsClient,
	defClient *armpolicy.DefinitionsClient,
	setDefinitionID string,
	assignmentName string,
	assignmentParams map[string]any,
	defCache map[string]*armpolicy.Definition,
	setDefCache map[string]*armpolicy.SetDefinition,
) []DenyPolicy {
	setDef, ok := setDefCache[setDefinitionID]
	if !ok {
		var err error
		setDef, err = getPolicySetDefinitionByID(
			ctx, setClient, setDefinitionID,
		)
		if err != nil {
			log.Printf(
				"policy preflight: could not fetch policy set definition %s: %v",
				setDefinitionID, err,
			)
			setDefCache[setDefinitionID] = nil
			return nil
		}
		setDefCache[setDefinitionID] = setDef
	}
	if setDef == nil {
		return nil
	}

	if setDef.Properties == nil ||
		setDef.Properties.PolicyDefinitions == nil {
		return nil
	}

	// Build effective parameters by applying the set definition's defaults
	// for any parameters not explicitly set by the assignment. Without this,
	// "Opt In" policies whose assignment relies on the set's default (e.g.
	// "Audit") would incorrectly fall back to the member definition's
	// default (often "Deny").
	effectiveParams := applySetDefaults(
		assignmentParams, setDef.Properties.Parameters,
	)

	var results []DenyPolicy
	for _, member := range setDef.Properties.PolicyDefinitions {
		if member.PolicyDefinitionID == nil {
			continue
		}

		// Merge set-level parameters with member-level parameter values.
		memberParams := mergeParams(effectiveParams, member.Parameters)

		policies := s.checkPolicyDefinition(
			ctx, defClient, *member.PolicyDefinitionID,
			assignmentName, memberParams, defCache,
		)
		results = append(results, policies...)
	}

	return results
}

// getPolicyDefinitionByID fetches a policy definition by its full resource ID.
// It handles built-in, subscription-scoped, and management-group-scoped
// definitions.
func getPolicyDefinitionByID(
	ctx context.Context,
	client *armpolicy.DefinitionsClient,
	definitionID string,
) (*armpolicy.Definition, error) {
	name := lastSegment(definitionID)
	if name == "" {
		return nil, fmt.Errorf(
			"invalid policy definition ID: %s", definitionID,
		)
	}

	if isBuiltInPolicyDefinition(definitionID) {
		resp, err := client.GetBuiltIn(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.Definition, nil
	}

	if mgID := extractManagementGroupID(definitionID); mgID != "" {
		resp, err := client.GetAtManagementGroup(ctx, mgID, name, nil)
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

// getPolicySetDefinitionByID fetches a policy set definition by its full
// resource ID. It handles built-in, subscription-scoped, and
// management-group-scoped set definitions.
func getPolicySetDefinitionByID(
	ctx context.Context,
	client *armpolicy.SetDefinitionsClient,
	setDefinitionID string,
) (*armpolicy.SetDefinition, error) {
	name := lastSegment(setDefinitionID)
	if name == "" {
		return nil, fmt.Errorf(
			"invalid policy set definition ID: %s", setDefinitionID,
		)
	}

	if isBuiltInPolicySetDefinition(setDefinitionID) {
		resp, err := client.GetBuiltIn(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.SetDefinition, nil
	}

	if mgID := extractManagementGroupID(setDefinitionID); mgID != "" {
		resp, err := client.GetAtManagementGroup(ctx, mgID, name, nil)
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

// extractDenyPolicies inspects a policy definition for deny-effect rules
// that target resource property fields. It returns any matching policies.
func extractDenyPolicies(
	def *armpolicy.Definition,
	assignmentName string,
	assignmentParams map[string]any,
) []DenyPolicy {
	if def.Properties == nil || def.Properties.PolicyRule == nil {
		return nil
	}

	ruleMap, ok := def.Properties.PolicyRule.(map[string]any)
	if !ok {
		log.Printf(
			"policy preflight: policy rule is not a map "+
				"for definition %s (type %T)",
			stringOrEmpty(def.Name), def.Properties.PolicyRule,
		)
		return nil
	}

	// Check if the effect is "deny" (literal or via parameter reference).
	if !isDenyEffect(
		ruleMap, def.Properties.Parameters, assignmentParams,
	) {
		return nil
	}

	log.Printf(
		"policy preflight: definition %q resolved as deny effect "+
			"(assignment=%q)",
		stringOrEmpty(def.Name), assignmentName,
	)

	// Parse the "if" condition to find field references.
	ifBlock, ok := ruleMap["if"]
	if !ok {
		return nil
	}

	results := findDenyConditions(ifBlock, assignmentName)
	if len(results) > 0 {
		log.Printf(
			"policy preflight: found %d deny condition(s) in policy %q",
			len(results), assignmentName,
		)
	}
	return results
}

// isDenyEffect checks whether the policy's effect resolves to "deny".
// Effects can be a literal string or a parameter reference like
// "[parameters('effect')]".
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
		log.Printf(
			"policy preflight: effect value is not a string (type %T): %v",
			effectVal, effectVal,
		)
		return false
	}

	// Check for literal deny.
	if strings.EqualFold(effectStr, "deny") {
		log.Printf("policy preflight: effect is literal deny")
		return true
	}

	// Check for parameter reference.
	paramName := extractParameterReference(effectStr)
	if paramName == "" {
		return false
	}

	// First check assignment-level parameters (override definition defaults).
	if v, ok := assignmentParams[paramName]; ok {
		if s, ok := v.(string); ok && strings.EqualFold(s, "deny") {
			log.Printf(
				"policy preflight: effect resolved to deny "+
					"via assignment param %q=%q",
				paramName, s,
			)
			return true
		}
		return false
	}

	// Fall back to the definition's default value.
	if definitionParams != nil {
		if paramDef, ok := definitionParams[paramName]; ok &&
			paramDef.DefaultValue != nil {
			if s, ok := paramDef.DefaultValue.(string); ok &&
				strings.EqualFold(s, "deny") {
				log.Printf(
					"policy preflight: effect resolved to deny "+
						"via definition default param %q=%q",
					paramName, s,
				)
				return true
			}
		}
	}

	return false
}

// findDenyConditions traverses the policy rule's "if" block to find
// conditions that reference resource property fields and extracts the target
// resource type.
func findDenyConditions(
	ifBlock any, assignmentName string,
) []DenyPolicy {
	condMap, ok := ifBlock.(map[string]any)
	if !ok {
		return nil
	}

	// Check for allOf / anyOf compound conditions.
	if allOf, ok := condMap["allOf"]; ok {
		return findInCompoundCondition(allOf, assignmentName, nil)
	}
	if anyOf, ok := condMap["anyOf"]; ok {
		return findInCompoundCondition(anyOf, assignmentName, nil)
	}

	// Single condition.
	return checkSingleCondition(condMap, assignmentName, nil)
}

// findInCompoundCondition processes an allOf/anyOf array looking for
// conditions that reference both a resource type and a property field.
// parentResourceTypes carries any resource types resolved by an ancestor
// compound condition.
func findInCompoundCondition(
	compound any, assignmentName string, parentResourceTypes []string,
) []DenyPolicy {
	conditions, ok := compound.([]any)
	if !ok {
		return nil
	}

	// First pass: find resource types from "field: type" conditions.
	var resourceTypes []string
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}

		fieldVal, _ := condMap["field"].(string)
		if strings.EqualFold(fieldVal, "type") {
			if eq, ok := condMap["equals"].(string); ok {
				resourceTypes = append(resourceTypes, eq)
			}
			if in, ok := condMap["in"].([]any); ok {
				for _, item := range in {
					if s, ok := item.(string); ok {
						resourceTypes = append(resourceTypes, s)
					}
				}
			}
		}
	}

	// Merge with parent resource types if this level didn't find any.
	if len(resourceTypes) == 0 {
		resourceTypes = parentResourceTypes
	}

	// Second pass: find property field references.
	var results []DenyPolicy
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}
		results = append(
			results,
			checkSingleCondition(
				condMap, assignmentName, resourceTypes,
			)...,
		)

		// Recurse into nested conditions, passing resolved resource types.
		if allOf, ok := condMap["allOf"]; ok {
			results = append(
				results,
				findInCompoundCondition(
					allOf, assignmentName, resourceTypes,
				)...,
			)
		}
		if anyOf, ok := condMap["anyOf"]; ok {
			results = append(
				results,
				findInCompoundCondition(
					anyOf, assignmentName, resourceTypes,
				)...,
			)
		}
	}

	return results
}

// checkSingleCondition checks if a single condition references a resource
// property field. resourceTypes are the candidate resource types resolved
// from sibling conditions.
func checkSingleCondition(
	condMap map[string]any,
	assignmentName string,
	resourceTypes []string,
) []DenyPolicy {
	fieldVal, ok := condMap["field"].(string)
	if !ok {
		return nil
	}

	if !IsLocalAuthField(fieldVal) {
		return nil
	}

	// If we don't have resource types from a sibling condition, try to
	// derive one from the field path.
	if len(resourceTypes) == 0 {
		if rt := resourceTypeFromFieldPath(fieldVal); rt != "" {
			resourceTypes = []string{rt}
		}
	}

	if len(resourceTypes) == 0 {
		return nil
	}

	// Emit one result per resource type.
	results := make([]DenyPolicy, 0, len(resourceTypes))
	for _, rt := range resourceTypes {
		results = append(results, DenyPolicy{
			PolicyName:   assignmentName,
			ResourceType: rt,
			FieldPath:    fieldVal,
		})
	}
	return results
}

// IsLocalAuthField returns true if the field path references a local
// authentication property.
func IsLocalAuthField(field string) bool {
	lower := strings.ToLower(field)
	return strings.HasSuffix(lower, "/disablelocalauth") ||
		strings.HasSuffix(lower, "/allowsharedkeyaccess") ||
		strings.EqualFold(field, "disableLocalAuth") ||
		strings.EqualFold(field, "allowSharedKeyAccess")
}

// resourceTypeFromFieldPath extracts the resource type from a fully qualified
// field path. For example,
// "Microsoft.CognitiveServices/accounts/disableLocalAuth" returns
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

// extractParameterReference extracts the parameter name from an ARM template
// parameter reference expression like "[parameters('effect')]". Returns empty
// if not a parameter reference.
func extractParameterReference(expr string) string {
	trimmed := strings.TrimSpace(expr)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "[parameters('") ||
		!strings.HasSuffix(lower, "')]") {
		return ""
	}
	inner := trimmed[len("[parameters('"):]
	before, _, ok := strings.Cut(inner, "')]")
	if !ok {
		return ""
	}
	return before
}

// extractAssignmentParams extracts parameter values from a policy assignment
// into a simple map.
func extractAssignmentParams(
	assignment *armpolicy.Assignment,
) map[string]any {
	if assignment.Properties == nil ||
		assignment.Properties.Parameters == nil {
		return nil
	}

	params := make(
		map[string]any, len(assignment.Properties.Parameters),
	)
	for name, val := range assignment.Properties.Parameters {
		if val != nil && val.Value != nil {
			params[name] = val.Value
		}
	}
	return params
}

// mergeParams merges assignment-level parameters with member-level parameter
// values from a policy set definition reference. Member parameters may contain
// parameter references like "[parameters('effect')]" that resolve against the
// assignment parameters.
func mergeParams(
	assignmentParams map[string]any,
	memberParams map[string]*armpolicy.ParameterValuesValue,
) map[string]any {
	if len(memberParams) == 0 {
		return assignmentParams
	}

	merged := make(
		map[string]any, len(assignmentParams)+len(memberParams),
	)
	maps.Copy(merged, assignmentParams)

	for name, val := range memberParams {
		if val == nil || val.Value == nil {
			continue
		}

		// Check if the member parameter value is itself a reference to
		// an assignment parameter.
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

// applySetDefaults fills in default values from the policy set definition's
// parameter declarations for any parameters not explicitly set by the
// assignment. This ensures that "Opt In" initiatives whose assignments rely
// on the set's default value (e.g. "Audit") are correctly resolved instead
// of falling through to the member definition's default (often "Deny").
func applySetDefaults(
	assignmentParams map[string]any,
	setParams map[string]*armpolicy.ParameterDefinitionsValue,
) map[string]any {
	if len(setParams) == 0 {
		return assignmentParams
	}

	effective := make(map[string]any, len(assignmentParams)+len(setParams))
	maps.Copy(effective, assignmentParams)

	for name, paramDef := range setParams {
		if _, alreadySet := effective[name]; alreadySet {
			continue
		}
		if paramDef != nil && paramDef.DefaultValue != nil {
			effective[name] = paramDef.DefaultValue
		}
	}

	return effective
}

// isBuiltInPolicyDefinition returns true if the definition ID is a built-in
// policy.
func isBuiltInPolicyDefinition(id string) bool {
	return strings.HasPrefix(
		strings.ToLower(id),
		"/providers/microsoft.authorization/policydefinitions/",
	)
}

// isCustomPolicyDefinition returns true if the definition ID is a
// subscription-scoped custom policy.
func isCustomPolicyDefinition(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(
		lower,
		"/providers/microsoft.authorization/policydefinitions/",
	) && !isBuiltInPolicyDefinition(id)
}

// isPolicySetDefinition returns true if the definition ID references a policy
// set (initiative).
func isPolicySetDefinition(id string) bool {
	return strings.Contains(
		strings.ToLower(id),
		"/providers/microsoft.authorization/policysetdefinitions/",
	)
}

// isBuiltInPolicySetDefinition returns true if the set definition ID is a
// built-in policy set.
func isBuiltInPolicySetDefinition(id string) bool {
	return strings.HasPrefix(
		strings.ToLower(id),
		"/providers/microsoft.authorization/policysetdefinitions/",
	)
}

// lastSegment returns the last path segment of a resource ID.
func lastSegment(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// extractManagementGroupID extracts the management group ID from a resource ID
// like "/providers/Microsoft.Management/managementGroups/{mgId}/providers/...".
// Returns empty string if the ID is not management-group-scoped.
func extractManagementGroupID(resourceID string) string {
	lower := strings.ToLower(resourceID)
	const prefix = "/providers/microsoft.management/managementgroups/"
	idx := strings.Index(lower, prefix)
	if idx < 0 {
		return ""
	}
	rest := resourceID[idx+len(prefix):]
	before, _, ok := strings.Cut(rest, "/")
	if !ok {
		return rest
	}
	return before
}

// ResourceHasLocalAuthDisabled checks whether a resource's properties JSON has
// the disableLocalAuth property set to true (or allowSharedKeyAccess set to
// false for storage accounts).
func ResourceHasLocalAuthDisabled(
	resourceType string, properties json.RawMessage,
) bool {
	if len(properties) == 0 {
		return false
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return false
	}

	// Storage accounts use allowSharedKeyAccess (inverted logic).
	if strings.EqualFold(
		resourceType, "Microsoft.Storage/storageAccounts",
	) {
		if v, ok := props["allowSharedKeyAccess"]; ok {
			if b, ok := v.(bool); ok {
				return !b
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

// stringOrEmpty safely dereferences a string pointer, returning "" if nil.
func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
