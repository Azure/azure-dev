// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cdk

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	bicepProvider "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/benbjohnson/clock"
	"golang.org/x/exp/slices"
)

type CdkProvider struct {
	console       input.Console
	dotNetCli     dotnet.DotNetCli
	bicepProvider Provider
	env           *environment.Environment
	envManager    environment.Manager
	curPrincipal  CurrentPrincipalIdProvider
	subResolver   account.SubscriptionTenantResolver
	prompters     prompt.Prompter
}

func (p *CdkProvider) Name() string {
	return "Cdk"
}

func isSupported(project appdetect.Project) bool {
	return slices.Contains([]appdetect.Language{
		appdetect.DotNet,
	}, project.Language)
}

func (p *CdkProvider) externalTools(project appdetect.Project) (tools []tools.ExternalTool) {
	switch project.Language {
	case appdetect.DotNet:
		tools = append(tools, p.dotNetCli)
	default:
	}
	return tools
}

func (p *CdkProvider) generate(ctx context.Context, project appdetect.Project) error {
	cdkOutput := filepath.Join(project.Path, "out")
	azdEnv := p.env.Environ()

	// append principalID (not stored to .env by default)
	if _, exists := p.env.LookupEnv(environment.PrincipalIdEnvVarName); !exists {
		currentPrincipalId, err := p.curPrincipal.CurrentPrincipalId(ctx)
		if err != nil {
			return fmt.Errorf("fetching current principal id for cdk: %w", err)
		}
		azdEnv = append(azdEnv, fmt.Sprintf("%s=%s", environment.PrincipalIdEnvVarName, currentPrincipalId))
	}
	// append TenantId (not stored to .env by default)
	if _, exists := p.env.LookupEnv(environment.TenantIdEnvVarName); !exists {
		tenantId, err := p.subResolver.LookupTenant(ctx, p.env.GetSubscriptionId())
		if err != nil {
			return fmt.Errorf("fetching tenant id for cdk: %w", err)
		}
		azdEnv = append(azdEnv, fmt.Sprintf("%s=%s", environment.TenantIdEnvVarName, tenantId))
	}

	switch project.Language {
	case appdetect.DotNet:
		return p.dotNetCli.Run(ctx, project.Path, []string{cdkOutput}, azdEnv)
	default:
	}
	return fmt.Errorf("cdk - not implemented.")
}

func (p *CdkProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
	infraPath := filepath.Join(projectPath, options.Path)

	if options.HideOutput {
		return nil
	}

	msg := "Detecting cdk language"
	p.console.ShowSpinner(ctx, msg, input.Step)

	cdkProjects, err := appdetect.Detect(infraPath)
	if err == nil && len(cdkProjects) == 1 {
		msg = fmt.Sprintf("%s (%s)", msg, cdkProjects[0].Language)
	}
	p.console.StopSpinner(ctx, msg, input.GetStepResultFormat(err))
	if err != nil {
		return fmt.Errorf("detecting cdk language: %w", err)
	}

	if len(cdkProjects) > 1 {
		return fmt.Errorf("detecting cdk language: found more than one project")
	}

	cdkProject := cdkProjects[0]
	if !isSupported(cdkProject) {
		return fmt.Errorf("cdk provider: language is not supported")
	}

	if err := tools.EnsureInstalled(ctx, p.externalTools(cdkProject)...); err != nil {
		return err
	}

	if err := EnsureSubscriptionAndLocation(ctx, p.envManager, p.env, p.prompters, func(loc account.Location) bool {
		return true
	}); err != nil {
		return err
	}

	msg = "Running cdk"
	p.console.ShowSpinner(ctx, msg, input.Step)
	if err := p.generate(ctx, cdkProject); err != nil {
		return fmt.Errorf("generating infrastructure as code from cdk: %w", err)
	}
	p.console.StopSpinner(ctx, msg, input.GetStepResultFormat(err))

	options.Path = filepath.Join(options.Path, "out")
	return p.bicepProvider.Initialize(ctx, projectPath, options)
}

func (p *CdkProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	return p.bicepProvider.State(ctx, options)
}

func (p *CdkProvider) Deploy(ctx context.Context) (*DeployResult, error) {
	return p.bicepProvider.Deploy(ctx)
}

func (p *CdkProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	return p.bicepProvider.Preview(ctx)
}

func (p *CdkProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	return p.bicepProvider.Destroy(ctx, options)
}

func (p *CdkProvider) EnsureEnv(ctx context.Context) error {
	return p.bicepProvider.EnsureEnv(ctx)
}

func NewCdkProvider(bicepCli bicep.BicepCli,
	azCli azcli.AzCli,
	deploymentsService azapi.Deployments,
	deploymentOperations azapi.DeploymentOperations,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
	curPrincipal CurrentPrincipalIdProvider,
	alphaFeatureManager *alpha.FeatureManager,
	clock clock.Clock,
	dotNetCli dotnet.DotNetCli,
	envManager environment.Manager,
	subResolver account.SubscriptionTenantResolver) Provider {
	return &CdkProvider{
		bicepProvider: bicepProvider.NewBicepProvider(
			bicepCli,
			azCli,
			deploymentsService,
			deploymentOperations, envManager, env, console, prompters, curPrincipal, alphaFeatureManager, clock,
		),
		console:      console,
		dotNetCli:    dotNetCli,
		env:          env,
		curPrincipal: curPrincipal,
		subResolver:  subResolver,
		prompters:    prompters,
		envManager:   envManager,
	}
}
