package provisioning

import (
	"context"
	"fmt"
	"time"

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

type ProvisionApplyResult struct {
	Operations []tools.AzCliResourceOperation
	Outputs    map[string]ProvisioningPlanOutputParameter
	Error      error
}

type ProvisionDestroyResult struct {
	Resources []tools.AzCliResource
	Outputs   map[string]ProvisioningPlanOutputParameter
	Error     error
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
	Plan(ctx context.Context) (*ProvisioningPlan, error)
	SaveTemplate(ctx context.Context, plan ProvisioningPlan) error
	Apply(ctx context.Context, plan *ProvisioningPlan, scope ProvisioningScope) (<-chan *ProvisionApplyResult, <-chan *ProvisionApplyProgress)
	Destroy(ctx context.Context, plan *ProvisioningPlan) (<-chan *ProvisionDestroyResult, <-chan *ProvisionDestroyProgress)
}

func NewInfraProvider(env *environment.Environment, projectPath string, options InfrastructureOptions, azCli tools.AzCli) (InfraProvider, error) {
	var provider InfraProvider
	bicepCli := tools.NewBicepCli(azCli)

	switch options.Provider {
	case Bicep:
		provider = NewBicepInfraProvider(env, projectPath, options, bicepCli, azCli)
	default:
		provider = NewBicepInfraProvider(env, projectPath, options, bicepCli, azCli)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", options.Provider)
}

var _ BicepInfraProvider = BicepInfraProvider{}
