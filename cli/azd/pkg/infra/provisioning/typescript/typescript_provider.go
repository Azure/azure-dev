// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	defaultModule = "main"
	defaultPath   = "infra"
)

// TypeScriptProvider exposes infrastructure provisioning using TypeScript configuration
type TypeScriptProvider struct {
	envManager  environment.Manager
	env         *environment.Environment
	projectPath string
	options     provisioning.Options
	console     input.Console
	configPath  string
}

// Name gets the name of the infra provider
func (p *TypeScriptProvider) Name() string {
	return "typescript"
}

func (p *TypeScriptProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initialize initializes provider state from the options
func (p *TypeScriptProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	p.projectPath = projectPath
	p.options = options
	
	if p.options.Module == "" {
		p.options.Module = defaultModule
	}
	if p.options.Path == "" {
		p.options.Path = defaultPath
	}

	// Check for TypeScript config file
	configPath := filepath.Join(projectPath, p.options.Path, "deploy.ts")
	if _, err := os.Stat(configPath); err == nil {
		p.configPath = configPath
	} else {
		return fmt.Errorf("TypeScript configuration file not found at %s", configPath)
	}

	return p.EnsureEnv(ctx)
}

// EnsureEnv ensures that the environment is in a provision-ready state
func (p *TypeScriptProvider) EnsureEnv(ctx context.Context) error {
	return provisioning.EnsureSubscriptionAndLocation(
		ctx,
		p.envManager,
		p.env,
		nil,
		provisioning.EnsureSubscriptionAndLocationOptions{},
	)
}

// State returns the current state of the infrastructure
func (p *TypeScriptProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	// TODO: Implement state retrieval from Azure
	return &provisioning.StateResult{
		State: &provisioning.State{
			Outputs: make(map[string]provisioning.OutputParameter),
		},
	}, nil
}

// Deploy deploys the infrastructure
func (p *TypeScriptProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
// Run `npx ts-node deploy.ts` and parse outputs
cmd := "npx"
args := []string{"ts-node", p.configPath}

// Set up environment variables for the script
envVars := os.Environ()
envVars = append(envVars,
	"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
	"AZURE_ENV_NAME="+p.env.Name(),
	"AZURE_LOCATION="+p.env.GetLocation(),
)

out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
if err != nil {
	return nil, fmt.Errorf("failed to run deploy.ts: %w", err)
}

// Expect the script to print a JSON object with outputs
var outputs map[string]provisioning.OutputParameter
if err := json.Unmarshal([]byte(out), &outputs); err != nil {
	// fallback: try to parse as { outputs: { ... } }
	var wrapper struct {
		Outputs map[string]provisioning.OutputParameter `json:"outputs"`
	}
	if err2 := json.Unmarshal([]byte(out), &wrapper); err2 == nil {
		outputs = wrapper.Outputs
	} else {
		return nil, fmt.Errorf("failed to parse deploy.ts output: %w", err)
	}
}

return &provisioning.DeployResult{
	Deployment: &provisioning.Deployment{
		Outputs: outputs,
	},
}, nil
}

// Preview shows a preview of the changes
func (p *TypeScriptProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	   // Optionally, run `npx ts-node deploy.ts --preview` if supported
	   cmd := "npx"
	   args := []string{"ts-node", p.configPath, "--preview"}
	   envVars := os.Environ()
	   envVars = append(envVars,
			   "AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
			   "AZURE_ENV_NAME="+p.env.Name(),
			   "AZURE_LOCATION="+p.env.GetLocation(),
	   )
	   out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
	   if err != nil {
			   return nil, fmt.Errorf("failed to run deploy.ts preview: %w", err)
	   }

	   // Try to parse as a map of outputs (legacy shape, not supported for preview)
	   // (No Outputs field in DeploymentPreview)
	   // fallback: try to parse as { preview: { ... } }

	   var wrapper struct {
			   Preview provisioning.DeploymentPreview `json:"preview"`
	   }
	   if err := json.Unmarshal([]byte(out), &wrapper); err == nil {
			   return &provisioning.DeployPreviewResult{
					   Preview: &wrapper.Preview,
			   }, nil
	   }

	   // fallback: try to parse as full DeploymentPreview struct
	   var preview provisioning.DeploymentPreview
	   if err := json.Unmarshal([]byte(out), &preview); err != nil {
			   return nil, fmt.Errorf("failed to parse preview output: %w", err)
	   }
	   return &provisioning.DeployPreviewResult{
			   Preview: &preview,
	   }, nil
}

// Destroy destroys the infrastructure
func (p *TypeScriptProvider) Destroy(ctx context.Context, options provisioning.DestroyOptions) (*provisioning.DestroyResult, error) {
// Optionally, run `npx ts-node deploy.ts --destroy` if supported
cmd := "npx"
args := []string{"ts-node", p.configPath, "--destroy"}
envVars := os.Environ()
envVars = append(envVars,
	"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
	"AZURE_ENV_NAME="+p.env.Name(),
	"AZURE_LOCATION="+p.env.GetLocation(),
)
out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
if err != nil {
	return nil, fmt.Errorf("failed to run deploy.ts destroy: %w", err)
}
var result provisioning.DestroyResult
if err := json.Unmarshal([]byte(out), &result); err != nil {
	return nil, fmt.Errorf("failed to parse destroy output: %w", err)
}
return &result, nil
}

// Parameters returns the parameters for the infrastructure
func (p *TypeScriptProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
// Optionally, run `npx ts-node deploy.ts --parameters` if supported
cmd := "npx"
args := []string{"ts-node", p.configPath, "--parameters"}
envVars := os.Environ()
envVars = append(envVars,
	"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
	"AZURE_ENV_NAME="+p.env.Name(),
	"AZURE_LOCATION="+p.env.GetLocation(),
)
out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
if err != nil {
	return nil, fmt.Errorf("failed to run deploy.ts parameters: %w", err)
}
var params []provisioning.Parameter
if err := json.Unmarshal([]byte(out), &params); err != nil {
	return nil, fmt.Errorf("failed to parse parameters output: %w", err)
}
	return params, nil
}

// NewTypeScriptProvider creates a new instance of a TypeScript Infra provider
func NewTypeScriptProvider(
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
) provisioning.Provider {
	return &TypeScriptProvider{
		envManager: envManager,
		env:        env,
		console:    console,
	}
}
