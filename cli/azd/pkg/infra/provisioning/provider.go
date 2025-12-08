// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"strings"

	"dario.cat/mergo"
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

// Options for a provisioning provider.
type Options struct {
	Provider         ProviderKind   `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
	// Not expected to be defined at azure.yaml
	IgnoreDeploymentState bool `yaml:"-"`

	// Provisioning options for each individually defined layer.
	Layers []Options `yaml:"layers,omitempty"`
}

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

// GetLayers return the provisioning layers defined.
// When [Options.Layers] is not defined, it returns the single layer defined.
//
// The ordering is stable; and reflects the order defined in azure.yaml.
func (o *Options) GetLayers() []Options {
	if len(o.Layers) == 0 {
		return []Options{*o}
	}

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
	errWrap := func(err string) error {
		return fmt.Errorf("validating infra.layers: %s", err)
	}

	anyIncompatibleFieldsSet := func() bool {
		return o.Name != "" || o.Module != "" || o.Path != "" || o.DeploymentStacks != nil
	}

	if len(o.Layers) > 0 && anyIncompatibleFieldsSet() {
		return errWrap(
			"properties on 'infra' cannot be declared when 'infra.layers' is declared")
	}

	for _, layer := range o.Layers {
		if layer.Name == "" {
			return errWrap("name must be specified for each provisioning layer")
		}

		if layer.Path == "" {
			return errWrap(fmt.Sprintf("%s: path must be specified", layer.Name))
		}
	}

	return nil
}

type SkippedReasonType string

const DeploymentStateSkipped SkippedReasonType = "deployment State"

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
