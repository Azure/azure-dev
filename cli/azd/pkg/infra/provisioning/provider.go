// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
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

type PreviewResult struct {
	Deployment Deployment
}

type PreviewProgress struct {
	Message   string
	Timestamp time.Time
}

type DeployResult struct {
	Operations []azcli.AzCliResourceOperation
	Deployment *Deployment
}

type DestroyResult struct {
	Resources []azcli.AzCliResource
	Outputs   map[string]OutputParameter
}

type DeployProgress struct {
	Message    string
	Timestamp  time.Time
	Operations []azcli.AzCliResourceOperation
}

type DestroyProgress struct {
	Message   string
	Timestamp time.Time
}

type Provider interface {
	Name() string
	RequiredExternalTools() []tools.ExternalTool
	GetDeployment(ctx context.Context, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Preview(ctx context.Context) *async.InteractiveTaskWithProgress[*PreviewResult, *PreviewProgress]
	Deploy(ctx context.Context, deployment *Deployment, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress]
}

func NewProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) (Provider, error) {
	var provider Provider

	switch infraOptions.Provider {
	case Bicep:
		provider = NewBicepProvider(ctx, env, projectPath, infraOptions)
	case Test:
		provider = NewTestProvider(ctx, env, projectPath, infraOptions)
	default:
		provider = NewBicepProvider(ctx, env, projectPath, infraOptions)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", infraOptions.Provider)
}

var _ BicepProvider = BicepProvider{}
