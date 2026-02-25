// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

// armTemplateResource represents a single resource declaration within an ARM template.
// It follows the schema defined at:
// https://learn.microsoft.com/azure/azure-resource-manager/templates/resource-declaration
type armTemplateResource struct {
	// Type is the resource type including namespace (e.g. "Microsoft.Storage/storageAccounts").
	Type string `json:"type"`
	// APIVersion is the REST API version to use for the resource (e.g. "2023-01-01").
	APIVersion string `json:"apiVersion"`
	// Name is the name of the resource, may contain ARM template expressions.
	Name string `json:"name"`
	// Location is the deployment location for the resource.
	Location string `json:"location,omitempty"`
	// Tags are resource tags. Stored as json.RawMessage because the value can be either
	// a map[string]string literal or an ARM expression string (e.g. "[variables('tags')]").
	Tags json.RawMessage `json:"tags,omitempty"`
	// DependsOn lists symbolic names or resource IDs of resources that must be deployed first.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Kind is the resource kind (e.g. "StorageV2" for storage or "app,linux" for web apps).
	Kind string `json:"kind,omitempty"`
	// SKU is the pricing tier / SKU for the resource.
	SKU *armTemplateSKU `json:"sku,omitempty"`
	// Plan is the marketplace plan for the resource.
	Plan *armTemplatePlan `json:"plan,omitempty"`
	// Identity is the managed identity configuration for the resource.
	Identity *armTemplateIdentity `json:"identity,omitempty"`
	// Properties is the resource-specific configuration.
	Properties json.RawMessage `json:"properties,omitempty"`
	// Condition is an expression that evaluates to true/false controlling whether the resource is deployed.
	Condition any `json:"condition,omitempty"`
	// Copy defines iteration for deploying multiple instances.
	Copy *armTemplateCopy `json:"copy,omitempty"`
	// Comments are optional authoring comments.
	Comments string `json:"comments,omitempty"`
	// Scope is used when deploying extension resources or cross-scope resources.
	Scope string `json:"scope,omitempty"`
	// Resources are child resources nested inside this resource declaration.
	// Uses armTemplateResources to handle both array and symbolic-name map formats.
	Resources armTemplateResources `json:"resources,omitempty"`
	// Zones lists Availability Zones for the resource (e.g. ["1","2","3"]).
	Zones []string `json:"zones,omitempty"`
}

// armTemplateSKU represents the SKU block of an ARM resource.
type armTemplateSKU struct {
	Name     string `json:"name"`
	Tier     string `json:"tier,omitempty"`
	Size     string `json:"size,omitempty"`
	Family   string `json:"family,omitempty"`
	Capacity *int   `json:"capacity,omitempty"`
}

// armTemplatePlan represents a marketplace plan block.
type armTemplatePlan struct {
	Name          string `json:"name"`
	Publisher     string `json:"publisher,omitempty"`
	Product       string `json:"product,omitempty"`
	PromotionCode string `json:"promotionCode,omitempty"`
	Version       string `json:"version,omitempty"`
}

// armTemplateIdentity represents the managed identity configuration.
type armTemplateIdentity struct {
	Type                   string                            `json:"type"`
	UserAssignedIdentities map[string]armTemplateIdentityRef `json:"userAssignedIdentities,omitempty"`
}

// armTemplateIdentityRef is an entry in userAssignedIdentities (value is typically empty).
type armTemplateIdentityRef struct {
	ClientID    string `json:"clientId,omitempty"`
	PrincipalID string `json:"principalId,omitempty"`
}

// armTemplateCopy describes the copy/iteration loop for a resource.
type armTemplateCopy struct {
	Name      string `json:"name"`
	Count     any    `json:"count"`               // can be int or expression string
	Mode      string `json:"mode,omitempty"`      // "serial" or "parallel" (default)
	BatchSize *int   `json:"batchSize,omitempty"` // used when mode is "serial"
}

// armTemplateVariable represents a variable value in the template. Variables can hold any JSON type.
type armTemplateVariable = json.RawMessage

// armTemplateFunction represents a user-defined function in an ARM template.
type armTemplateFunction struct {
	Namespace string                       `json:"namespace"`
	Members   map[string]armTemplateMember `json:"members"`
}

// armTemplateMember represents a single member in a user-defined function namespace.
type armTemplateMember struct {
	Parameters []armTemplateMemberParameter `json:"parameters,omitempty"`
	Output     armTemplateMemberOutput      `json:"output"`
}

// armTemplateMemberParameter is a parameter declaration inside a user-defined function.
type armTemplateMemberParameter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// armTemplateMemberOutput is the output declaration of a user-defined function.
type armTemplateMemberOutput struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// armTemplateParameterDef is the parser's own representation of an ARM template parameter definition.
// This is intentionally separate from azure.ArmTemplateParameterDefinition to keep the parser self-contained.
type armTemplateParameterDef struct {
	Type          string                     `json:"type"`
	DefaultValue  any                        `json:"defaultValue,omitempty"`
	AllowedValues []any                      `json:"allowedValues,omitempty"`
	MinValue      *int                       `json:"minValue,omitempty"`
	MaxValue      *int                       `json:"maxValue,omitempty"`
	MinLength     *int                       `json:"minLength,omitempty"`
	MaxLength     *int                       `json:"maxLength,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
	Description   string                     `json:"-"` // extracted from metadata
}

// armTemplateOutputDef represents an output declaration in the ARM template.
type armTemplateOutputDef struct {
	Type      string           `json:"type"`
	Value     any              `json:"value"`
	Condition any              `json:"condition,omitempty"`
	Copy      *armTemplateCopy `json:"copy,omitempty"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

// armTemplateResources is a custom type that handles both ARM template resource formats:
//   - Traditional (languageVersion 1.0): resources is a JSON array of resource objects.
//   - Symbolic name (languageVersion 2.0): resources is a JSON object keyed by symbolic names.
//
// Bicep compiles modules using languageVersion 2.0, so both formats must be supported.
type armTemplateResources []armTemplateResource

// UnmarshalJSON implements custom unmarshalling to handle both array and map resource formats.
func (r *armTemplateResources) UnmarshalJSON(data []byte) error {
	// Try array format first (traditional ARM templates)
	var arr []armTemplateResource
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = arr
		return nil
	}

	// Try map/object format (languageVersion 2.0 symbolic name resources)
	var m map[string]armTemplateResource
	if err := json.Unmarshal(data, &m); err == nil {
		resources := make([]armTemplateResource, 0, len(m))
		for _, res := range m {
			resources = append(resources, res)
		}
		*r = resources
		return nil
	}

	return fmt.Errorf("resources must be a JSON array or object, got: %.40s", string(data))
}

// armTemplate is the parser's own comprehensive representation of a full ARM/JSON deployment template.
// Follows https://learn.microsoft.com/azure/azure-resource-manager/templates/syntax
type armTemplate struct {
	Schema          string                             `json:"$schema"`
	ContentVersion  string                             `json:"contentVersion"`
	LanguageVersion string                             `json:"languageVersion,omitempty"`
	APIProfile      string                             `json:"apiProfile,omitempty"`
	Parameters      map[string]armTemplateParameterDef `json:"parameters,omitempty"`
	Variables       map[string]armTemplateVariable     `json:"variables,omitempty"`
	Functions       []armTemplateFunction              `json:"functions,omitempty"`
	Resources       armTemplateResources               `json:"resources"`
	Outputs         map[string]armTemplateOutputDef    `json:"outputs,omitempty"`
}

// preflightResource is a flattened resource entry produced by the parser, representing a single resource
// that would be deployed. Nested/child resources are resolved to top-level entries with their full type
// and name path.
type preflightResource struct {
	// Type is the fully-qualified resource type (e.g. "Microsoft.Storage/storageAccounts/blobServices").
	Type string
	// Name is the full name path (e.g. "myStorage/default").
	Name string
	// APIVersion is the REST API version used for the resource.
	APIVersion string
	// Location is the deployment location.
	Location string
	// Kind is the optional resource kind.
	Kind string
	// SKU is the optional SKU configuration.
	SKU *armTemplateSKU
	// DependsOn lists the dependencies.
	DependsOn []string
	// HasCondition indicates whether the resource has a condition expression.
	HasCondition bool
	// HasCopyLoop indicates whether the resource uses a copy/loop.
	HasCopyLoop bool
	// Properties is the raw JSON of the resource-specific properties.
	Properties json.RawMessage
}

// localArmPreflight provides local (client-side) validation of an ARM template before deployment.
// It parses the template and parameters to build a comprehensive view of all resources that would
// be deployed, enabling early detection of issues without making Azure API calls.
type localArmPreflight struct{}

// newLocalArmPreflight creates a new instance of localArmPreflight.
func newLocalArmPreflight() *localArmPreflight {
	return &localArmPreflight{}
}

// validate performs local preflight validation on the given ARM template and parameters.
// It parses the template, resolves parameters, and returns resourcesProperties summarizing
// key characteristics of the deployment (such as whether it contains role assignments)
// along with any validation errors detected locally.
func (l *localArmPreflight) validate(
	armTemplate azure.RawArmTemplate,
	_ azure.ArmParameters,
) (resourcesProperties, error) {
	parsed, err := l.parseTemplate(armTemplate)
	if err != nil {
		return resourcesProperties{}, fmt.Errorf("parsing ARM template: %w", err)
	}

	resources := l.collectResources(parsed.Resources, "" /* parentType */, "" /* parentName */)
	props := analyzeResources(resources)

	return props, nil
}

// parseTemplate unmarshals a raw ARM template into the parser's own armTemplate structure.
func (l *localArmPreflight) parseTemplate(raw azure.RawArmTemplate) (*armTemplate, error) {
	var tmpl armTemplate
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		return nil, fmt.Errorf("unmarshalling ARM template JSON: %w", err)
	}

	if tmpl.Schema == "" {
		return nil, fmt.Errorf("ARM template is missing required '$schema' property")
	}

	if tmpl.ContentVersion == "" {
		return nil, fmt.Errorf("ARM template is missing required 'contentVersion' property")
	}

	if len(tmpl.Resources) == 0 {
		return nil, fmt.Errorf("ARM template contains no resources")
	}

	return &tmpl, nil
}

// armDeploymentProperties represents the properties block of a Microsoft.Resources/deployments resource.
// This is used to extract inner template resources from nested deployments.
type armDeploymentProperties struct {
	Template *armTemplate `json:"template,omitempty"`
	Mode     string       `json:"mode,omitempty"` // "Incremental" or "Complete"
}

// isNestedDeployment returns true if the resource type is Microsoft.Resources/deployments,
// which is how Bicep compiles modules into ARM templates.
func isNestedDeployment(resourceType string) bool {
	return strings.EqualFold(resourceType, "Microsoft.Resources/deployments")
}

// collectResources recursively walks the ARM template resource tree and produces a flat list of
// preflightResource entries. It handles:
//   - Child resources nested via the "resources" array (type/name are combined with parent).
//   - Nested deployments (Microsoft.Resources/deployments) where actual resources live inside
//     properties.template.resources. These are transparently expanded so callers see the
//     real resources rather than the deployment wrapper.
func (l *localArmPreflight) collectResources(
	resources []armTemplateResource,
	parentType string,
	parentName string,
) []preflightResource {
	var result []preflightResource

	for _, r := range resources {
		fullType := r.Type
		fullName := r.Name

		// For child resources nested inside a parent, the type and name are relative.
		// We need to combine them with the parent's type and name.
		// e.g. parent type "Microsoft.Storage/storageAccounts" + child type "blobServices"
		//   => "Microsoft.Storage/storageAccounts/blobServices"
		if parentType != "" {
			fullType = parentType + "/" + r.Type
			fullName = parentName + "/" + r.Name
		}

		// For Microsoft.Resources/deployments (nested deployments produced by Bicep modules),
		// extract the inner template resources instead of treating the deployment as a leaf resource.
		if isNestedDeployment(fullType) && len(r.Properties) > 0 {
			innerResources := l.extractNestedDeploymentResources(r)
			if len(innerResources) > 0 {
				result = append(result, innerResources...)
				continue
			}
			// If we couldn't extract inner resources (e.g. templateLink or expression-based),
			// fall through and include the deployment resource itself.
		}

		pr := preflightResource{
			Type:         fullType,
			Name:         fullName,
			APIVersion:   r.APIVersion,
			Location:     r.Location,
			Kind:         r.Kind,
			SKU:          r.SKU,
			DependsOn:    r.DependsOn,
			HasCondition: r.Condition != nil,
			HasCopyLoop:  r.Copy != nil,
			Properties:   r.Properties,
		}

		result = append(result, pr)

		// Recursively collect nested child resources
		if len(r.Resources) > 0 {
			children := l.collectResources(r.Resources, fullType, fullName)
			result = append(result, children...)
		}
	}

	return result
}

// extractNestedDeploymentResources parses the properties.template of a Microsoft.Resources/deployments
// resource and recursively collects the resources declared within the inner template.
// This is the primary mechanism by which Bicep modules are expanded — each module compiles to a
// nested deployment whose inner template contains the actual resources.
func (l *localArmPreflight) extractNestedDeploymentResources(
	r armTemplateResource,
) []preflightResource {
	var props armDeploymentProperties
	if err := json.Unmarshal(r.Properties, &props); err != nil {
		// Can't parse properties — might be expression-based or use templateLink.
		return nil
	}

	if props.Template == nil || len(props.Template.Resources) == 0 {
		return nil
	}

	// Recursively collect resources from the inner template.
	// Inner template resources are top-level within their own scope, so we pass empty parent type/name.
	return l.collectResources(props.Template.Resources, "", "")
}

// resourcesProperties contains derived properties from analyzing the collected preflight resources.
type resourcesProperties struct {
	// HasRoleAssignments indicates whether the deployment includes one or more
	// Microsoft.Authorization/roleAssignments resources.
	HasRoleAssignments bool
}

// analyzeResources inspects the list of preflight resources and returns a resourcesProperties
// summarizing key characteristics of the deployment.
func analyzeResources(resources []preflightResource) resourcesProperties {
	props := resourcesProperties{}
	for _, r := range resources {
		if strings.EqualFold(r.Type, "Microsoft.Authorization/roleAssignments") {
			props.HasRoleAssignments = true
			break
		}
	}
	return props
}
