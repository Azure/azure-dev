// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type ProviderKind string

type NewProviderFn func(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) (Provider, error)

var (
	//providersMutex guards providers. the lock should be taken before interacting with the map.
	providersMutex sync.Mutex                     = sync.Mutex{}
	providers      map[ProviderKind]NewProviderFn = make(map[ProviderKind]NewProviderFn)
)

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

type DeploymentPlanningProgress struct {
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
	GetDeployment(ctx context.Context, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Plan(ctx context.Context) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]
	Deploy(ctx context.Context, plan *DeploymentPlan, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress]
}

// Registers a provider creation function for the specified provider kind
func RegisterProvider(kind ProviderKind, newFn NewProviderFn) error {
	if newFn == nil {
		return errors.New("NewProviderFn is required")
	}

	providersMutex.Lock()
	providers[kind] = newFn
	providersMutex.Unlock()
	return nil
}

func NewProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) (Provider, error) {
	var provider Provider

	if infraOptions.Provider == "" {
		infraOptions.Provider = Bicep
	}

	providersMutex.Lock()
	newProviderFn, ok := providers[infraOptions.Provider]
	providersMutex.Unlock()

	if !ok {
		return nil, fmt.Errorf("provider '%s' is not supported", infraOptions.Provider)
	}

	provider, err := newProviderFn(ctx, env, projectPath, infraOptions)
	if err != nil {
		return nil, fmt.Errorf("error creating provider for type '%s' : %w", infraOptions.Provider, err)
	}

	return provider, nil
}
