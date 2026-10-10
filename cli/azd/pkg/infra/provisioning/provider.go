// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"dario.cat/mergo"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
)

type ProviderKind string

const (
	NotSpecified ProviderKind = ""
	Bicep        ProviderKind = "bicep"
	Arm          ProviderKind = "arm"
	Terraform    ProviderKind = "terraform"
	Pulumi       ProviderKind = "pulumi"
	Test         ProviderKind = "test"
)

// Arm, Pulumi and Test are omitted because azd defines the kinds but registers no implementation.
// Keep in sync with the providers registered in pkg/azd.
var builtInProviderKinds = []ProviderKind{
	Bicep,
	Terraform,
}

// BuiltInProviderKinds returns the provisioning providers implemented by azd itself, as opposed to
// those supplied by an extension.
func BuiltInProviderKinds() []ProviderKind {
	return builtInProviderKinds
}

type Mode string

const (
	// Default mode for deploying or previewing the deployment.
	ModeDeploy Mode = ""
	// Mode for destroying the deployment.
	ModeDestroy Mode = "destroy"
)

// Options for a provisioning provider.
type Options struct {
	Provider ProviderKind `yaml:"provider,omitempty"`
	Path     string       `yaml:"path,omitempty"`
	Module   string       `yaml:"module,omitempty"`
	Name     string       `yaml:"name,omitempty"`
	// Layer is assigned from the containing project layer.
	Layer            string                  `yaml:"-" json:"layer,omitempty"`
	Hooks            HooksConfig             `yaml:"hooks,omitempty"`
	DeploymentStacks *DeploymentStacksConfig `yaml:"deploymentStacks,omitempty"`
	// Config holds provider-specific configuration options
	Config map[string]any `yaml:"config,omitempty"`
	// ParamAliases maps environment variable names in the provider view to names in the project view.
	ParamAliases map[string]string `yaml:"paramAliases,omitempty" json:"paramAliases,omitempty"`
	// OutputAliases maps output names in the provider view to variable names in the project view.
	OutputAliases map[string]string `yaml:"outputAliases,omitempty" json:"outputAliases,omitempty"`
	// DependsOn lists the names of other infrastructure entries this entry must wait for
	// before being provisioned. Use this to declare hook-mediated edges
	// (for example, when a postprovision hook in another entry writes an
	// env var that this entry's bicepparam reads at provision time)
	// that the static analyzer cannot infer from .bicep / .bicepparam /
	// .parameters.json contents alone. Valid under both `infra.layers[]`
	// and `layers[].infra[]`.
	DependsOn []string `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	// Provisioning options for each individually defined layer.
	Layers []Options `yaml:"layers,omitempty"`

	// Runtime options

	// IgnoreDeploymentState when true, skips the deployment state check.
	IgnoreDeploymentState bool `yaml:"-"`
	// The mode in which the deployment is being run.
	Mode Mode `yaml:"-"`
	// Environment variables that should be considered as resolved when prompting for parameters.
	//
	// This is used when planning multiple layers, and would be set to plan-time outputs
	// from previous layers.
	// Callers supply project-view variable names; Manager.Initialize maps them to provider-view names.
	VirtualEnv map[string]string `yaml:"-"`
}

// HooksConfig aliases ext.HooksConfig for compatibility with existing provisioning package references.
type HooksConfig = ext.HooksConfig

// GetWithDefaults merges the provided infra options with the default provisioning options
func (o Options) GetWithDefaults(other ...Options) (Options, error) {
	mergedOptions := Options{}

	// Merge in the provided infra options first
	if err := mergo.Merge(&mergedOptions, o); err != nil {
		return Options{}, fmt.Errorf("merging infra options: %w", err)
	}

	// Merge in any other provided options
	for _, opt := range other {
		if err := mergo.Merge(&mergedOptions, opt); err != nil {
			return Options{}, fmt.Errorf("merging other options: %w", err)
		}
	}

	// Finally, merge in the default provisioning options
	if err := mergo.Merge(&mergedOptions, defaultOptions); err != nil {
		return Options{}, fmt.Errorf("merging default infra options: %w", err)
	}

	return mergedOptions, nil
}

// AbsolutePath returns the layer path resolved against the project path when needed.
func (o Options) AbsolutePath(projectPath string) string {
	if filepath.IsAbs(o.Path) {
		return o.Path
	}

	return filepath.Join(projectPath, o.Path)
}

// ApplyOutputAliases maps output names in the provider view to variable names in the project view.
func ApplyOutputAliases(
	outputs map[string]OutputParameter,
	aliases map[string]string,
) (map[string]OutputParameter, error) {
	sharedOutputs := make(map[string]OutputParameter, len(outputs))
	sources := make(map[string]string, len(outputs))

	for _, localName := range slices.Sorted(maps.Keys(outputs)) {
		sharedName := localName
		if alias, has := aliases[localName]; has {
			sharedName = alias
		}

		if previous, has := sources[sharedName]; has && previous != localName {
			return nil, fmt.Errorf(
				"provider outputs %q and %q both target project variable %q",
				previous, localName, sharedName,
			)
		}

		sources[sharedName] = localName
		sharedOutputs[sharedName] = outputs[localName]
	}

	return sharedOutputs, nil
}

// GetLayers return the provisioning layers defined.
// When [Options.Layers] is not defined, it returns the single layer defined.
//
// The ordering is stable; and reflects the order defined in azure.yaml.
func (o *Options) GetLayers() []Options {
	if len(o.Layers) == 0 {
		return []Options{*o}
	}

	tracing.AppendUsageAttributeUnique(fields.FeaturesKey.String(fields.FeatLayers))
	return o.Layers
}

// GetLayer returns the provisioning layer with the provided name.
// When [Options.Layers] is not defined, an empty name returns the single layer defined.
func (o *Options) GetLayer(name string) (Options, error) {
	if name == "" && len(o.Layers) == 0 {
		return *o, nil
	}

	if len(o.Layers) == 0 {
		return Options{}, fmt.Errorf("no layers defined in azure.yaml")
	}

	names := make([]string, 0, len(o.Layers))
	for _, layer := range o.Layers {
		if layer.Name == name {
			return layer, nil
		}

		names = append(names, layer.Name)
	}

	return Options{}, fmt.Errorf(
		"layer '%s' not found in azure.yaml. available layers: %s", name, strings.Join(names, ", "))
}

// Validate validates the current loaded config for correctness.
//
// This should be called immediately right after Unmarshal() before any defaulting is performed.
func (o *Options) Validate() error {
	return o.validate(false)
}

// ValidateProjectLayers validates infrastructure entries declared under top-level project layers.
func (o *Options) ValidateProjectLayers() error {
	return o.validate(true)
}

func (o *Options) validate(allowPathlessExtensionProviders bool) error {
	if len(o.Hooks) > 0 {
		return validateErr("infra", "'hooks' can only be declared under 'infra.layers[]'")
	}
	if len(o.ParamAliases) > 0 || len(o.OutputAliases) > 0 {
		return validateErr("infra", "'paramAliases' and 'outputAliases' can only be declared under 'infra.layers[]'")
	}

	if len(o.Layers) > 0 {
		anyIncompatibleFieldsSet := func() bool {
			return o.Name != "" || o.Module != "" || o.Path != "" || o.DeploymentStacks != nil ||
				len(o.ParamAliases) > 0 || len(o.OutputAliases) > 0
		}

		if anyIncompatibleFieldsSet() {
			return validateErr("infra", "properties on 'infra' cannot be declared when 'infra.layers' is declared")
		}

		if err := o.validateLayers(allowPathlessExtensionProviders); err != nil {
			return wrapValidateErr("infra.layers", err)
		}
	}

	return nil
}

func wrapValidateErr(scope string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("validating %s: %w", scope, err)
}

func validateErr(scope, format string, args ...any) error {
	return wrapValidateErr(scope, fmt.Errorf(format, args...))
}

func (o *Options) validateLayers(allowPathlessExtensionProviders bool) error {
	validateHooks := func(scope string, hooks HooksConfig) error {
		for hookName := range hooks {
			hookType, eventName := ext.InferHookType(hookName)
			if hookType == ext.HookTypeNone || eventName != "provision" {
				return fmt.Errorf("%s: only 'preprovision' and 'postprovision' hooks are supported", scope)
			}
		}

		return nil
	}

	seenLayers := map[string]struct{}{}
	for _, layer := range o.Layers {
		if layer.Name == "" {
			return fmt.Errorf("name must be specified for each provisioning layer")
		}

		if _, has := seenLayers[layer.Name]; has {
			return fmt.Errorf("duplicate layer name '%s' is not allowed", layer.Name)
		}

		seenLayers[layer.Name] = struct{}{}

		// NOTE: I'm treating 'NotSpecified' as 'bicep' - there's some downstream code that does that in
		// 'provisioning/manager'.
		// It might be nice to think about doing this earlier, or having that validation occurring in the providers instead.
		if layer.Path == "" && (!allowPathlessExtensionProviders ||
			layer.Provider == NotSpecified || slices.Contains(builtInProviderKinds, layer.Provider)) {
			return fmt.Errorf("%s: path must be specified", layer.Name)
		}

		if err := validateHooks(layer.Name, layer.Hooks); err != nil {
			return err
		}
		if err := validateLayerAliases(layer); err != nil {
			return fmt.Errorf("%s: %w", layer.Name, err)
		}
	}

	return nil
}

var aliasNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateLayerAliases(layer Options) error {
	for localName, sharedName := range layer.ParamAliases {
		if localName == "" || sharedName == "" {
			return errors.New("input alias names cannot be empty in the provider view or project view")
		}
		if !aliasNamePattern.MatchString(localName) || !aliasNamePattern.MatchString(sharedName) {
			return fmt.Errorf("input alias %q to %q must use names matching %s", localName, sharedName, aliasNamePattern)
		}
	}

	// YAML does not declare provider outputs, so collisions with unaliased outputs need a runtime check.
	destinations := make(map[string]string, len(layer.OutputAliases))
	for localName, sharedName := range layer.OutputAliases {
		if localName == "" || sharedName == "" {
			return errors.New("output alias names cannot be empty in the provider view or project view")
		}
		if !aliasNamePattern.MatchString(localName) || !aliasNamePattern.MatchString(sharedName) {
			return fmt.Errorf("output alias %q to %q must use names matching %s", localName, sharedName, aliasNamePattern)
		}
		if previous, has := destinations[sharedName]; has && previous != localName {
			return fmt.Errorf(
				"output aliases %q and %q cannot both target %q in the project view",
				previous, localName, sharedName,
			)
		}
		destinations[sharedName] = localName
	}

	return nil
}

type SkippedReasonType string

const (
	DeploymentStateSkipped             SkippedReasonType = "deployment state"
	ProvisionValidationCanceledSkipped SkippedReasonType = "provision validation canceled"
)

type DeployResult struct {
	Deployment    *Deployment
	SkippedReason SkippedReasonType
}

// DeployPreviewResult defines one deployment in preview mode, displaying what changes would it be performed, without
// applying the changes.
type DeployPreviewResult struct {
	Preview *DeploymentPreview
}

type DestroyResult struct {
	// InvalidatedEnvKeys is a list of keys that should be removed from the environment after the destroy is complete.
	InvalidatedEnvKeys []string
	// SkippedDeletion is true when the provider intentionally did not delete any resources (for example, when
	// running with --no-prompt in a CI/CD environment without --force, where only a destroy preview is shown).
	SkippedDeletion bool
}

type StateResult struct {
	State *State
}

type Parameter struct {
	Name          string
	Secret        bool
	Value         any
	EnvVarMapping []string
	// true when the parameter value was set by the user from the command line (prompt)
	LocalPrompt        bool
	UsingEnvVarMapping bool
}

// PlannedOutput represents a plan-time output.
// It does not contain the actual output value.
type PlannedOutput struct {
	// The name of the planned output
	Name string
}

type Provider interface {
	Name() string
	Initialize(ctx context.Context, projectPath string, options Options) error
	State(ctx context.Context, options *StateOptions) (*StateResult, error)
	Deploy(ctx context.Context) (*DeployResult, error)
	Preview(ctx context.Context) (*DeployPreviewResult, error)
	Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error)
	EnsureEnv(ctx context.Context) error
	Parameters(ctx context.Context) ([]Parameter, error)
	PlannedOutputs(ctx context.Context) ([]PlannedOutput, error)
}
