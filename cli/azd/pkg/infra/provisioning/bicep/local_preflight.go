// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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

// PreflightCheckSeverity indicates the severity level of a preflight check result.
type PreflightCheckSeverity int

const (
	// PreflightCheckWarning indicates a non-blocking issue that should be reported to the user.
	PreflightCheckWarning PreflightCheckSeverity = iota
	// PreflightCheckError indicates a blocking issue that should prevent deployment.
	PreflightCheckError
)

// PreflightCheckResult holds the outcome of a single preflight check function.
type PreflightCheckResult struct {
	// Severity indicates whether this result is a warning or a blocking error.
	Severity PreflightCheckSeverity
	// Message is a human-readable description of the finding.
	Message string
}

// validationContext provides the data and utilities available to preflight check functions.
// It acts as a bag of convenient values that checks may inspect to produce their results.
type validationContext struct {
	// Console provides user interaction capabilities (prompts, messages).
	Console input.Console
	// Props contains derived properties from analyzing the ARM template resources.
	Props resourcesProperties
	// ResourcesSnapshot is the raw JSON output from `bicep snapshot`, containing the fully
	// resolved deployment graph. It may be nil if the Bicep CLI was not available.
	ResourcesSnapshot json.RawMessage
	// SnapshotResources is the parsed list of predicted resources from the Bicep snapshot.
	// Each entry represents a resource that would be deployed, with resolved values.
	// It may be nil if the Bicep CLI was not available.
	SnapshotResources []armTemplateResource
}

// snapshotResult represents the top-level structure of the Bicep snapshot JSON output.
type snapshotResult struct {
	PredictedResources []armTemplateResource `json:"predictedResources"`
}

// PreflightCheckFn is a function that performs a single preflight validation check.
// It receives the execution context and a validationContext containing the console,
// analyzed resource properties, and the deployment snapshot.
// It returns a result describing the finding (or nil if there is nothing to report)
// and an error if the check itself failed to execute.
type PreflightCheckFn func(
	ctx context.Context,
	valCtx *validationContext,
) (*PreflightCheckResult, error)

// localArmPreflight provides local (client-side) validation of an ARM template before deployment.
// It parses the template and parameters to build a comprehensive view of all resources that would
// be deployed, enabling early detection of issues without making Azure API calls.
//
// Callers can register additional check functions via AddCheck before calling validate. Each
// registered function is invoked with the analyzed resource properties, and the results are
// collected and returned alongside the resource properties.
type localArmPreflight struct {
	// modulePath is the absolute path to the source Bicep module (e.g. /project/infra/main.bicep).
	modulePath string
	// bicepCli is the Bicep CLI wrapper used to run bicep commands such as snapshot.
	bicepCli *bicep.Cli
	// target is the deployment scope (subscription or resource group) used to derive snapshot options.
	// It may be nil, in which case snapshot options are left empty.
	target infra.Deployment
	checks []PreflightCheckFn
}

// newLocalArmPreflight creates a new instance of localArmPreflight.
// modulePath is the path to the source Bicep module file (e.g. "infra/main.bicep").
// bicepCli is the Bicep CLI wrapper used to invoke bicep commands.
// target is the deployment scope used to populate snapshot options; it may be nil.
func newLocalArmPreflight(modulePath string, bicepCli *bicep.Cli, target infra.Deployment) *localArmPreflight {
	return &localArmPreflight{modulePath: modulePath, bicepCli: bicepCli, target: target}
}

// AddCheck registers a preflight check function to be executed during validate.
// Check functions are invoked in the order they are added.
func (l *localArmPreflight) AddCheck(fn PreflightCheckFn) {
	l.checks = append(l.checks, fn)
}

// validate performs local preflight validation on the given ARM template and parameters.
// It parses the template, resolves parameters, analyzes the resources, and then runs all
// registered check functions. It returns the collected results from all checks and an error
// if template parsing fails.
func (l *localArmPreflight) validate(
	ctx context.Context,
	console input.Console,
	armTemplate azure.RawArmTemplate,
	armParameters azure.ArmParameters,
) ([]PreflightCheckResult, error) {
	_, err := l.parseTemplate(armTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing ARM template: %w", err)
	}

	// Determine the .bicepparam file to use for the snapshot.
	// If the module path already points to a .bicepparam file, use it directly.
	// Otherwise, create a temporary .bicepparam file next to the .bicep module with the resolved parameters.
	var bicepParamFile string
	if filepath.Ext(l.modulePath) == ".bicepparam" {
		bicepParamFile = l.modulePath
	} else {
		bicepFileName := filepath.Base(l.modulePath)
		moduleDir := filepath.Dir(l.modulePath)

		bicepParamContent := generateBicepParam(bicepFileName, armParameters)

		tmpFile, err := os.CreateTemp(moduleDir, "preflight-*.bicepparam")
		if err != nil {
			return nil, fmt.Errorf("creating temp bicepparam file: %w", err)
		}
		bicepParamFile = tmpFile.Name()
		defer func() {
			tmpFile.Close()
			os.Remove(bicepParamFile)
		}()

		if _, err := tmpFile.WriteString(bicepParamContent); err != nil {
			return nil, fmt.Errorf("writing temp bicepparam file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return nil, fmt.Errorf("closing temp bicepparam file: %w", err)
		}
	}

	// Build snapshot options from the deployment target scope.
	snapshotOpts := bicep.NewSnapshotOptions()
	if l.target != nil {
		snapshotOpts = snapshotOpts.WithSubscriptionID(l.target.SubscriptionId())

		switch t := l.target.(type) {
		case *infra.ResourceGroupDeployment:
			snapshotOpts = snapshotOpts.WithResourceGroup(t.ResourceGroupName())
		case *infra.SubscriptionDeployment:
			snapshotOpts = snapshotOpts.WithLocation(t.Location())
		}
	}

	// Run the Bicep snapshot command to produce a deployment snapshot from the bicepparam file.
	// The snapshot contains the fully resolved deployment graph with expressions evaluated,
	// conditions applied, and copy loops expanded.
	data, err := l.bicepCli.Snapshot(ctx, bicepParamFile, snapshotOpts)
	if err != nil {
		return nil, fmt.Errorf("running bicep snapshot: %w", err)
	}

	var snapshot snapshotResult
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("parsing bicep snapshot: %w", err)
	}

	props := analyzeResources(snapshot.PredictedResources)

	valCtx := &validationContext{
		Console:           console,
		Props:             props,
		ResourcesSnapshot: json.RawMessage(data),
		SnapshotResources: snapshot.PredictedResources,
	}

	var results []PreflightCheckResult
	for _, check := range l.checks {
		result, err := check(ctx, valCtx)
		if err != nil {
			return results, fmt.Errorf("preflight check failed: %w", err)
		}
		if result != nil {
			results = append(results, *result)
		}
	}

	return results, nil
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

// generateBicepParam produces a .bicepparam file content string from the given ARM parameters
// and the name of the Bicep file they target. The output follows the Bicep parameters file
// format:
//
//	using '<bicepFile>'
//
//	param <name> = <value>
//
// Parameter names are emitted in sorted order for deterministic output. Values are serialized
// as Bicep literals: strings use single quotes, arrays and objects use JSON-like syntax with
// single-quoted string values, booleans and numbers are written as-is, and null produces the
// Bicep null keyword. Key Vault references are skipped because they cannot be represented
// directly as Bicep parameter values.
func generateBicepParam(bicepFile string, params azure.ArmParameters) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("using '%s'\n", bicepFile))

	for _, name := range slices.Sorted(maps.Keys(params)) {
		param := params[name]

		// Key Vault references cannot be expressed as bicepparam values; skip them.
		if param.KeyVaultReference != nil {
			continue
		}

		sb.WriteString(fmt.Sprintf("\nparam %s = %s\n", name, toBicepValue(param.Value)))
	}

	return sb.String()
}

// toBicepValue converts a Go value into its Bicep literal representation.
// Supported types: string→'single-quoted', bool→true/false, nil→null,
// numeric types→number literal, arrays→[...], maps/objects→{key: value}.
func toBicepValue(v any) string {
	if v == nil {
		return "null"
	}

	switch val := v.(type) {
	case string:
		// Bicep strings use single quotes; escape embedded single quotes by doubling them.
		escaped := strings.ReplaceAll(val, "'", "''")
		return fmt.Sprintf("'%s'", escaped)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case json.Number:
		return val.String()
	case float64:
		// JSON numbers decode as float64 by default.
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case float32:
		if val == float32(int32(val)) {
			return fmt.Sprintf("%d", int32(val))
		}
		return fmt.Sprintf("%g", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case []any:
		items := make([]string, 0, len(val))
		for _, item := range val {
			items = append(items, toBicepValue(item))
		}
		return fmt.Sprintf("[\n  %s\n]", strings.Join(items, "\n  "))
	case map[string]any:
		if len(val) == 0 {
			return "{}"
		}
		entries := make([]string, 0, len(val))
		for _, k := range slices.Sorted(maps.Keys(val)) {
			entries = append(entries, fmt.Sprintf("  %s: %s", k, toBicepValue(val[k])))
		}
		return fmt.Sprintf("{\n%s\n}", strings.Join(entries, "\n"))
	default:
		// Fallback: marshal to JSON.
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("'%v'", val)
		}
		return string(b)
	}
}

// resourcesProperties contains derived properties from analyzing the collected preflight resources.
type resourcesProperties struct {
	// HasRoleAssignments indicates whether the deployment includes one or more
	// Microsoft.Authorization/roleAssignments resources.
	HasRoleAssignments bool
}

// analyzeResources inspects the list of snapshot resources and returns a resourcesProperties
// summarizing key characteristics of the deployment.
func analyzeResources(resources []armTemplateResource) resourcesProperties {
	props := resourcesProperties{}
	for _, r := range resources {
		if strings.EqualFold(r.Type, "Microsoft.Authorization/roleAssignments") {
			props.HasRoleAssignments = true
			break
		}
	}
	return props
}
