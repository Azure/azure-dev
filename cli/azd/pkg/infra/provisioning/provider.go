// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"strings"
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

// Options for a provisioning provider as defined in azure.yaml.
type Options struct {
	Provider         ProviderKind   `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
	// Not expected to be defined at azure.yaml
	IgnoreDeploymentState bool `yaml:"-"`

	// Provisioning options when split into layers.
	Layers []Options `yaml:"layers,omitempty"`
}

// GetLayers return the provisioning layers defined.
//
// The ordering is stable; and reflects the order defined in azure.yaml.
func (o *Options) GetLayers() []Options {
	if len(o.Layers) == 0 {
		return []Options{*o}
	}

	return o.Layers
}

// GetLayer returns the named provisioning layer.
func (o *Options) GetLayer(name string) (Options, error) {
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

// anyFieldsSet returns true if any options fields were set to a non-empty value.
func (o *Options) anyFieldsSet() bool {
	return o.Name != "" || o.Module != "" || o.Path != "" || o.Provider != "" || o.DeploymentStacks != nil
}

// Validate validates the current loaded config for correctness.
//
// This should be called immediately right after Unmarshal() before any defaulting is performed.
func (o *Options) Validate() error {
	errWrap := func(err string) error {
		return fmt.Errorf("validating infra.layers: %s", err)
	}

	if len(o.Layers) > 0 && o.anyFieldsSet() {
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
