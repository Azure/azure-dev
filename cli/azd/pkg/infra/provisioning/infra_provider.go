package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type InfrastructureProviderKind string

const (
	Bicep     InfrastructureProviderKind = "Bicep"
	Arm       InfrastructureProviderKind = "Arm"
	Terraform InfrastructureProviderKind = "Terraform"
	Pulumi    InfrastructureProviderKind = "Pulumi"
)

type InfrastructureOptions struct {
	Provider InfrastructureProviderKind `yaml:"provider"`
	Path     string                     `yaml:"path"`
	Module   string                     `yaml:"module"`
}

type ProvisionPlanResult struct {
	Plan ProvisioningPlan
}

type ProvisionPlanProgress struct {
	Message   string
	Timestamp time.Time
}

type ProvisionApplyResult struct {
	Operations []tools.AzCliResourceOperation
	Outputs    map[string]ProvisioningPlanOutputParameter
}

type ProvisionDestroyResult struct {
	Resources []tools.AzCliResource
	Outputs   map[string]ProvisioningPlanOutputParameter
}

type ProvisionApplyProgress struct {
	Timestamp  time.Time
	Operations []tools.AzCliResourceOperation
}

type ProvisionDestroyProgress struct {
	Message   string
	Timestamp time.Time
}

type InfraProvider interface {
	Name() string
	RequiredExternalTools() []tools.ExternalTool
	UpdatePlan(ctx context.Context, plan ProvisioningPlan) error
	Plan(ctx context.Context) async.AsyncTaskWithProgress[*ProvisionPlanResult, *ProvisionPlanProgress]
	Apply(ctx context.Context, plan *ProvisioningPlan, scope ProvisioningScope) async.AsyncTaskWithProgress[*ProvisionApplyResult, *ProvisionApplyProgress]
	Destroy(ctx context.Context, plan *ProvisioningPlan) async.AsyncTaskWithProgress[*ProvisionDestroyResult, *ProvisionDestroyProgress]
}

func NewInfraProvider(env *environment.Environment, projectPath string, options InfrastructureOptions, azCli tools.AzCli) (InfraProvider, error) {
	var provider InfraProvider

	switch options.Provider {
	case Bicep:
		provider = NewBicepInfraProvider(env, projectPath, options, azCli)
	default:
		provider = NewBicepInfraProvider(env, projectPath, options, azCli)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", options.Provider)
}

var _ BicepInfraProvider = BicepInfraProvider{}
