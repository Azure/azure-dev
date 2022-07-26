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
	Bicep     ProviderKind = "Bicep"
	Arm       ProviderKind = "Arm"
	Terraform ProviderKind = "Terraform"
	Pulumi    ProviderKind = "Pulumi"
)

type Options struct {
	Provider ProviderKind `yaml:"provider"`
	Path     string       `yaml:"path"`
	Module   string       `yaml:"module"`
}

type PlanResult struct {
	Plan Plan
}

type PlanProgress struct {
	Message   string
	Timestamp time.Time
}

type ApplyResult struct {
	Operations []tools.AzCliResourceOperation
	Outputs    map[string]PlanOutputParameter
}

type DestroyResult struct {
	Resources []tools.AzCliResource
	Outputs   map[string]PlanOutputParameter
}

type ApplyProgress struct {
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
	UpdatePlan(ctx context.Context, plan Plan) error
	Plan(ctx context.Context) async.InteractiveTaskWithProgress[*PlanResult, *PlanProgress]
	Apply(ctx context.Context, plan *Plan, scope Scope) async.InteractiveTaskWithProgress[*ApplyResult, *ApplyProgress]
	Destroy(ctx context.Context, plan *Plan) async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress]
}

func NewProvider(env *environment.Environment, projectPath string, options Options, console input.Console, azCli tools.AzCli) (Provider, error) {
	var provider Provider

	switch options.Provider {
	case Bicep:
		provider = NewBicepProvider(env, projectPath, options, console, azCli)
	default:
		provider = NewBicepProvider(env, projectPath, options, console, azCli)
	}

	if provider != nil {
		return provider, nil
	}

	return nil, fmt.Errorf("provider '%s' is not supported", options.Provider)
}

var _ BicepProvider = BicepProvider{}
