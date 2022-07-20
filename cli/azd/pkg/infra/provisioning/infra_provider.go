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

type InfraDeploymentResult struct {
	Operations []tools.AzCliResourceOperation
	Outputs    []InfraDeploymentOutputParameter
	Error      error
}

type InfraDeploymentOutputParameter struct {
	Name  string
	Type  string
	Value interface{}
}

type InfraDeploymentProgress struct {
	Timestamp  time.Time
	Operations []tools.AzCliResourceOperation
}

type InfraProvider interface {
	Name() string
	Compile(ctx context.Context) (*CompiledTemplate, error)
	SaveTemplate(ctx context.Context, template CompiledTemplate) error
	Deploy(ctx context.Context, scope ProvisioningScope) (<-chan *InfraDeploymentResult, <-chan *InfraDeploymentProgress)
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
