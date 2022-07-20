package provisioning

import (
	"context"
	"fmt"
	"strings"
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

type CompiledTemplate struct {
	Parameters []CompiledTemplateParameter
	Outputs    []InfraDeploymentOutputParameter
}

type CompiledTemplateParameter struct {
	Name         string
	DefaultValue interface{}
	Value        interface{}
}

func (p *CompiledTemplateParameter) HasValue() bool {
	return strings.TrimSpace(fmt.Sprint(p.Value)) != ""
}

func (p *CompiledTemplateParameter) HasDefaultValue() bool {
	return strings.TrimSpace(fmt.Sprint(p.DefaultValue)) != ""
}

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

type ProvisioningScope struct {
	name           string
	subscriptionId string
	location       string
	resourceGroup  string
}

func (s *ProvisioningScope) Name() string {
	return s.name
}

func (s *ProvisioningScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *ProvisioningScope) ResourceGroup() string {
	return s.resourceGroup
}

func (s *ProvisioningScope) Location() string {
	return s.location
}

func NewSubscriptionProvisioningScope(name string, subscriptionId string, location string) *ProvisioningScope {
	return &ProvisioningScope{
		name:           name,
		subscriptionId: subscriptionId,
		location:       location,
	}
}

func NewResourceGroupProvisioningScope(name string, subscriptionId string, resourceGroup string) *ProvisioningScope {
	return &ProvisioningScope{
		name:           name,
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
	}
}

type InfraProvider interface {
	Name() string
	Compile(ctx context.Context) (*CompiledTemplate, error)
	SaveTemplate(ctx context.Context, template *CompiledTemplate) error
	Deploy(ctx context.Context, scope *ProvisioningScope) (<-chan *InfraDeploymentResult, <-chan *InfraDeploymentProgress)
}

func NewInfraProvider(env *environment.Environment, projectPath string, options InfrastructureOptions, azCli tools.AzCli) (InfraProvider, error) {
	var provider InfraProvider
	bicepCli := tools.NewBicepCli(azCli)

	switch options.Module {
	case string(Bicep):
		provider = NewBicepInfraProvider(env, projectPath, options, bicepCli, azCli)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", options.Provider)
}

var _ BicepInfraProvider = BicepInfraProvider{}
