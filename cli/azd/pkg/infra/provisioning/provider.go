// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"path/filepath"
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

type Mode string

const (
	// Default mode for deploying or previewing the deployment.
	ModeDeploy Mode = ""
	// Mode for destroying the deployment.
	ModeDestroy Mode = "destroy"
)

// Options for a provisioning provider.
type Options struct {
	Provider         ProviderKind   `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	Hooks            HooksConfig    `yaml:"hooks,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
	// Provisioning options for each individually defined layer.
	Layers []Options `yaml:"layers,omitempty"`

	// Runtime options

	// IgnoreDeploymentState when true, skips the deployment state check.
	IgnoreDeploymentState bool `yaml:"-"`
	// The mode in which the deployment is being run.
	Mode Mode `yaml:"-"`
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
	if len(o.Hooks) > 0 {
		return validateErr("infra", "'hooks' can only be declared under 'infra.layers[]'")
	}

	if len(o.Layers) > 0 {
		anyIncompatibleFieldsSet := func() bool {
			return o.Name != "" || o.Module != "" || o.Path != "" || o.DeploymentStacks != nil
		}

		if anyIncompatibleFieldsSet() {
			return validateErr("infra", "properties on 'infra' cannot be declared when 'infra.layers' is declared")
		}

		if err := o.validateLayers(); err != nil {
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

func (o *Options) validateLayers() error {
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

		if layer.Path == "" {
			return fmt.Errorf("%s: path must be specified", layer.Name)
		}

		if err := validateHooks(layer.Name, layer.Hooks); err != nil {
			return err
		}
	}

	return nil
}

type SkippedReasonType string

const (
	DeploymentStateSkipped  SkippedReasonType = "deployment State"
	PreflightAbortedSkipped SkippedReasonType = "preflight aborted"
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

type Provider interface {
	Name() string
	Initialize(ctx context.Context, projectPath string, options Options) error
	State(ctx context.Context, options *StateOptions) (*StateResult, error)
	Deploy(ctx context.Context) (*DeployResult, error)
	Preview(ctx context.Context) (*DeployPreviewResult, error)
	Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error)
	EnsureEnv(ctx context.Context) error
	Parameters(ctx context.Context) ([]Parameter, error)
}
