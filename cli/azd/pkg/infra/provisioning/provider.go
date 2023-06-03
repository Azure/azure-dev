// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
)

type ProviderKind string

const (
	Bicep     ProviderKind = "bicep"
	Arm       ProviderKind = "arm"
	Terraform ProviderKind = "terraform"
	Pulumi    ProviderKind = "pulumi"
	Test      ProviderKind = "test"
)

type Options struct {
	Provider ProviderKind `yaml:"provider"`
	Path     string       `yaml:"path"`
	Module   string       `yaml:"module"`
}

type DeploymentPlan struct {
	Deployment Deployment

	// Additional information about deployment, provider-specific.
	Details interface{}
}

type DeployResult struct {
	Deployment *Deployment
}

type DestroyResult struct {
	// InvalidatedEnvKeys is a list of keys that should be removed from the environment after the destroy is complete.
	InvalidatedEnvKeys []string
}

type StateResult struct {
	State *State
}

type Provider interface {
	Name() string
	Initialize(ctx context.Context, projectPath string, options Options) error
	State(ctx context.Context) (*StateResult, error)
	Plan(ctx context.Context) (*DeploymentPlan, error)
	Deploy(ctx context.Context, plan *DeploymentPlan) (*DeployResult, error)
	Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error)
		env,
		projectPath,
		infraOptions,
		console,
		azCli,
		commandRunner,
		prompters,
		principalProvider,
		alphaFeatureManager,
	)
}
