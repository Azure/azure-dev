// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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

// armField is a generic JSON field that gracefully handles both structured values and
// ARM template expression strings. ARM templates compiled from Bicep may emit fields as
// expression strings (e.g. "[if(equals(...))]") when conditional logic is used, instead
// of the expected typed object.
//
// Use [armField.Value] for typed access (returns ok=false if the raw JSON cannot be parsed
// as T), and [armField.Raw] for the underlying JSON regardless of shape.
type armField[T any] struct {
	raw json.RawMessage
}

// HasValue reports whether the field was present and non-null in the JSON input.
func (f armField[T]) HasValue() bool {
	return len(f.raw) > 0 && !bytes.Equal(f.raw, []byte("null"))
}

// Value attempts to unmarshal the raw JSON into the typed representation T.
// It returns the parsed value and true on success, or the zero value and false
// if the field is absent, null, or not representable as T (e.g. an ARM expression string).
func (f armField[T]) Value() (T, bool) {
	var v T
	if !f.HasValue() {
		return v, false
	}
	if err := json.Unmarshal(f.raw, &v); err != nil {
		return v, false
	}
	return v, true
}

// Raw returns the underlying JSON bytes exactly as they appeared in the input.
// Returns nil if the field was absent from the JSON.
func (f armField[T]) Raw() json.RawMessage {
	return f.raw
}

// UnmarshalJSON stores the raw JSON bytes for deferred parsing.
func (f *armField[T]) UnmarshalJSON(data []byte) error {
	f.raw = append(json.RawMessage(nil), data...)
	return nil
}

// IsZero reports whether the field is absent (no raw JSON stored).
// This is used by encoding/json's omitzero tag to omit the field during marshaling.
func (f armField[T]) IsZero() bool {
	return f.raw == nil
}

// MarshalJSON writes the stored raw JSON bytes, or null if no value was stored.
func (f armField[T]) MarshalJSON() ([]byte, error) {
	if f.raw == nil {
		return []byte("null"), nil
	}
	return f.raw, nil
}

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
	// Tags holds resource tags, which may be a map[string]string literal or an ARM expression string.
	Tags armField[map[string]string] `json:"tags,omitzero"`
	// DependsOn lists symbolic names or resource IDs of resources that must be deployed first.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Kind is the resource kind (e.g. "StorageV2" for storage or "app,linux" for web apps).
	Kind string `json:"kind,omitempty"`
	// SKU is the pricing tier / SKU for the resource.
	SKU armField[armTemplateSKU] `json:"sku,omitzero"`
	// Plan is the marketplace plan for the resource.
	Plan armField[armTemplatePlan] `json:"plan,omitzero"`
	// Identity is the managed identity configuration for the resource.
	Identity armField[armTemplateIdentity] `json:"identity,omitzero"`
	// Properties is the resource-specific configuration. Kept as json.RawMessage because each
	// resource type has a different properties schema with no single typed representation.
	Properties json.RawMessage `json:"properties,omitempty"`
	// Condition is an expression that evaluates to true/false controlling whether the resource is deployed.
	Condition any `json:"condition,omitempty"`
	// Copy defines iteration for deploying multiple instances.
	Copy armField[armTemplateCopy] `json:"copy,omitzero"`
	// Comments are optional authoring comments.
	Comments string `json:"comments,omitempty"`
	// Scope is used when deploying extension resources or cross-scope resources.
	Scope string `json:"scope,omitempty"`
	// Resources are child resources nested inside this resource declaration.
	// Uses armTemplateResources to handle both array and symbolic-name map formats.
	Resources armTemplateResources `json:"resources,omitempty"`
	// Zones lists Availability Zones for the resource.
	Zones armField[[]string] `json:"zones,omitzero"`
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
	// DiagnosticID is a unique, stable identifier for this specific finding type
	// (e.g. "role_assignment_missing"). Used in telemetry to correlate actioned
	// warnings with deployment outcomes and to track error frequency over time.
	DiagnosticID string
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
// It returns zero or more results describing findings (or nil/empty if there is
// nothing to report) and an error if the check itself failed to execute.
type PreflightCheckFn func(
	ctx context.Context,
	valCtx *validationContext,
) ([]PreflightCheckResult, error)

// PreflightCheck pairs a unique rule identifier with its check function.
// The RuleID is a stable, unique string used in telemetry to identify which rule
// produced a result (e.g. for crash tracking). Each rule may emit results with
// different DiagnosticIDs to distinguish specific finding types.
type PreflightCheck struct {
	// RuleID is a unique, stable identifier for the rule (e.g. "role_assignment_permissions").
	RuleID string
	// Fn is the check function that performs the validation.
	Fn PreflightCheckFn
}

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
	// envLocation is the Azure location from the environment (AZURE_LOCATION). It is used to
	// provide location context when the deployment target scope doesn't carry its own location
	// (e.g. resource group deployments), enabling Bicep to resolve resourceGroup().location.
	envLocation string
	checks      []PreflightCheck
}

// newLocalArmPreflight creates a new instance of localArmPreflight.
// modulePath is the path to the source Bicep module file (e.g. "infra/main.bicep").
// bicepCli is the Bicep CLI wrapper used to invoke bicep commands.
// target is the deployment scope used to populate snapshot options; it may be nil.
// envLocation is the Azure location from the environment (e.g. AZURE_LOCATION); it may be empty.
func newLocalArmPreflight(
	modulePath string, bicepCli *bicep.Cli, target infra.Deployment, envLocation string,
) *localArmPreflight {
	return &localArmPreflight{
		modulePath:  modulePath,
		bicepCli:    bicepCli,
		target:      target,
		envLocation: envLocation,
	}
}

// AddCheck registers a preflight check to be executed during validate.
// Checks are invoked in the order they are added.
func (l *localArmPreflight) AddCheck(check PreflightCheck) {
	l.checks = append(l.checks, check)
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
			// ResourceGroupScope doesn't carry a location, but the Bicep snapshot needs
			// one to resolve resourceGroup().location. Use the environment location if
			// available so resources referencing resourceGroup().location get resolved.
			if l.envLocation != "" {
				snapshotOpts = snapshotOpts.WithLocation(l.envLocation)
			}
		case *infra.SubscriptionDeployment:
			snapshotOpts = snapshotOpts.WithLocation(t.Location())
		}
	}

	// Run the Bicep snapshot command to produce a deployment snapshot from the bicepparam file.
	// The snapshot contains the fully resolved deployment graph with expressions evaluated,
	// conditions applied, and copy loops expanded.
	// If the snapshot fails (e.g., older Bicep binary without snapshot support), skip local
	// preflight rather than blocking the deployment.
	data, err := l.bicepCli.Snapshot(ctx, bicepParamFile, snapshotOpts)
	if err != nil {
		log.Printf("local preflight: skipping checks, bicep snapshot unavailable: %v", err)
		return nil, nil
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
		checkResults, err := check.Fn(ctx, valCtx)
		if err != nil {
			return results, fmt.Errorf("preflight check %q failed: %w", check.RuleID, err)
		}
		results = append(results, checkResults...)
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
	// CognitiveDeployments lists AI model deployments found in the template,
	// with extracted model, SKU, capacity, and location information.
	CognitiveDeployments []cognitiveDeploymentInfo
}

// cognitiveDeploymentInfo holds the extracted properties of a single
// Microsoft.CognitiveServices/accounts/deployments resource needed for quota validation.
type cognitiveDeploymentInfo struct {
	// AccountName is the name of the parent cognitive services account.
	AccountName string
	// Name is the name of the model deployment.
	Name string
	// ModelName is the AI model name (e.g. "gpt-4o").
	ModelName string
	// ModelFormat is the model format (e.g. "OpenAI").
	ModelFormat string
	// ModelVersion is the model version string.
	ModelVersion string
	// SkuName is the SKU tier name (e.g. "GlobalStandard", "Standard").
	SkuName string
	// Capacity is the requested deployment capacity in units.
	Capacity int
	// Location is the Azure region for this deployment (inherited from parent account).
	Location string
}

// cognitiveDeploymentModelProperties mirrors the ARM template properties.model object
// for a Microsoft.CognitiveServices/accounts/deployments resource.
type cognitiveDeploymentModelProperties struct {
	Model struct {
		Name    string `json:"name"`
		Format  string `json:"format"`
		Version string `json:"version"`
	} `json:"model"`
}

// analyzeResources inspects the list of snapshot resources and returns a resourcesProperties
// summarizing key characteristics of the deployment.
func analyzeResources(resources []armTemplateResource) resourcesProperties {
	props := resourcesProperties{}

	// First pass: build a map of account name → location for cognitive services accounts.
	accountLocations := map[string]string{}
	for _, r := range resources {
		if strings.EqualFold(r.Type, "Microsoft.Authorization/roleAssignments") {
			props.HasRoleAssignments = true
		}
		if strings.EqualFold(r.Type, "Microsoft.CognitiveServices/accounts") {
			accountLocations[r.Name] = r.Location
		}
	}

	// Second pass: extract cognitive deployment info.
	for _, r := range resources {
		if !strings.EqualFold(r.Type, "Microsoft.CognitiveServices/accounts/deployments") {
			continue
		}
		info := extractCognitiveDeployment(r, accountLocations)
		if info.ModelName != "" {
			props.CognitiveDeployments = append(props.CognitiveDeployments, info)
		}
	}

	return props
}

// extractCognitiveDeployment parses a Microsoft.CognitiveServices/accounts/deployments
// resource and returns the extracted deployment info. accountLocations maps account names
// to their deployment locations for inheriting the location.
func extractCognitiveDeployment(
	r armTemplateResource, accountLocations map[string]string,
) cognitiveDeploymentInfo {
	info := cognitiveDeploymentInfo{
		Name:     r.Name,
		Location: r.Location,
	}

	// Parse the parent account name from the deployment resource name.
	// In ARM templates, child resource names are formatted as "parentName/childName".
	if parts := strings.SplitN(r.Name, "/", 2); len(parts) == 2 {
		info.AccountName = parts[0]
		info.Name = parts[1]
	}

	// Inherit location from the parent account if the deployment doesn't have its own.
	if info.Location == "" && info.AccountName != "" {
		info.Location = accountLocations[info.AccountName]
	}

	// Extract model properties from the resource.
	if len(r.Properties) > 0 {
		var modelProps cognitiveDeploymentModelProperties
		if err := json.Unmarshal(r.Properties, &modelProps); err == nil {
			info.ModelName = modelProps.Model.Name
			info.ModelFormat = modelProps.Model.Format
			info.ModelVersion = modelProps.Model.Version
		}
	}

	// Extract SKU name and capacity.
	if sku, ok := r.SKU.Value(); ok {
		info.SkuName = sku.Name
		if sku.Capacity != nil {
			info.Capacity = *sku.Capacity
		}
	}

	return info
}
