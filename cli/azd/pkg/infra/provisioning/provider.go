// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"cuelang.org/go/pkg/strings"
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

type Options struct {
	Provider         ProviderKind   `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
	// Not expected to be defined at azure.yaml
	IgnoreDeploymentState bool `yaml:"-"`

	Stages []Options `yaml:"stages,omitempty"`
}

func (o *Options) GetStage(name string) (*Options, error) {
	stageNames := make([]string, 0, len(o.Stages)+1)
	stageNames = append(stageNames, o.Name)

	for _, stage := range o.Stages {
		stageNames = append(stageNames, stage.Name)
		if stage.Name == name {
			return &stage, nil
		}
	}

	return nil, fmt.Errorf("stage '%s' not found in azure.yaml. available stages: %s", strings.Join(stageNames, ", "))
}

func (o *Options) Validate() error {
	errWrap := func(err string) error {
		return fmt.Errorf("validating infra.stages: %s", err)
	}

	for _, stage := range o.Stages {
		if stage.Name == "" {
			return errWrap("name must be specified for each provisioning stage")
		}

		if stage.Name == "infra" {
			return errWrap("stage name 'infra' is reserved as the default stage and cannot be used")
		}

		if stage.Path == "" {
			return errWrap(fmt.Sprintf("%s: path must be specified", stage.Name))
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
