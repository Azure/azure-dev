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

// Sentinel value representing the unnamed, root-level provisioning layer.
//
// Backend for deployment states treats this identically to empty string (preserving legacy behavior).
// "infra" is reserved and rejected during input validation.
const LayerEmpty = "infra"

type Options struct {
	Provider         ProviderKind   `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
	// Not expected to be defined at azure.yaml
	IgnoreDeploymentState bool `yaml:"-"`

	Layers []Options `yaml:"layers,omitempty"`
}

func (o *Options) GetLayer(name string) (Options, error) {
	layers := []Options{*o}
	layers = append(layers, o.Layers...)

	names := make([]string, 0, len(layers))
	for _, layer := range layers {
		if layer.Name == name {
			return layer, nil
		}

		names = append(names, layer.Name)
	}

	return Options{}, fmt.Errorf(
		"layer '%s' not found in azure.yaml. available layers: %s", name, strings.Join(names, ", "))
}

func (o *Options) Validate() error {
	errWrap := func(err string) error {
		return fmt.Errorf("validating infra.layers: %s", err)
	}

	for _, layer := range o.Layers {
		if layer.Name == "" {
			return errWrap("name must be specified for each provisioning layer")
		}

		if layer.Name == LayerEmpty {
			return errWrap(
				fmt.Sprintf(
					"layer name '%s' is reserved as the default layer and cannot be used",
					LayerEmpty))
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
