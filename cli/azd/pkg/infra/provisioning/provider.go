package provisioning

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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
	Preview Preview
}

type PreviewProgress struct {
	Message   string
	Timestamp time.Time
}

type DeployResult struct {
	Operations []tools.AzCliResourceOperation
	Outputs    map[string]PreviewOutputParameter
}

type DestroyResult struct {
	Resources []tools.AzCliResource
	Outputs   map[string]PreviewOutputParameter
}

type DeployProgress struct {
	Timestamp  time.Time
	Operations []tools.AzCliResourceOperation
}

type DestroyProgress struct {
	Message   string
	Timestamp time.Time
}

type Provider interface {
	Name() string
	RequiredExternalTools() []tools.ExternalTool
	UpdatePlan(ctx context.Context, preview Preview) error
	Preview(ctx context.Context) *async.InteractiveTaskWithProgress[*PreviewResult, *PreviewProgress]
	Deploy(ctx context.Context, preview *Preview, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Destroy(ctx context.Context, preview *Preview) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress]
}

func NewProvider(env *environment.Environment, projectPath string, options Options, console input.Console, cliArgs tools.NewCliToolArgs) (Provider, error) {
	var provider Provider

	switch options.Provider {
	case Bicep:
		bicepArgs := tools.NewBicepCliArgs(cliArgs)
		provider = NewBicepProvider(env, projectPath, options, console, bicepArgs)
	case Test:
		provider = NewTestProvider(env, projectPath, options, console)
	default:
		bicepArgs := tools.NewBicepCliArgs(cliArgs)
		provider = NewBicepProvider(env, projectPath, options, console, bicepArgs)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", options.Provider)
}

var _ BicepProvider = BicepProvider{}
