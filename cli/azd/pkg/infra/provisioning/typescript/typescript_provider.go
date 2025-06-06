// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/joho/godotenv"
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


func NewTypeScriptProvider(
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
) provisioning.Provider {
	if envManager == nil {
		panic("NewTypeScriptProvider: envManager must not be nil")
	}

	return &TypeScriptProvider{
		envManager: envManager,
		env:        env, // can be nil – handle this in methods
		console:    console,
	}
}


// Name gets the name of the infra provider
func (p *TypeScriptProvider) Name() string {
	return "typescript"
}

func (p *TypeScriptProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initialize initializes provider state from the options
func (p *TypeScriptProvider) Initialize(ctx context.Context, envName string, config provisioning.Options) error {
    if p.env == nil {
        // First check if environment name is provided
        if envName == "" {
            // Try to get from OS environment variables
            envName = os.Getenv(environment.EnvNameEnvVarName)
            
            // If still not found, check project directory for .env file
            if envName == "" && config.Path != "" {
                dotEnvPath := fmt.Sprintf("%s/.env", config.Path)
                if info, err := os.Stat(dotEnvPath); err == nil && !info.IsDir() {
                    if envVars, err := godotenv.Read(dotEnvPath); err == nil {
                        if name, found := envVars[environment.EnvNameEnvVarName]; found && name != "" {
                            envName = name
                        }
                    }
                }
            }
        }
        
        if envName == "" {
            return fmt.Errorf("environment name is required but was empty. Set %s in environment or .env file", environment.EnvNameEnvVarName)
        }

        env, err := p.envManager.Get(ctx, envName)
        if err != nil {
            if err == environment.ErrNotFound {
                // Environment doesn't exist, attempt to create it
                spec := environment.Spec{
                    Name: envName,
                }
                
                // Try to get location and subscription from environment variables
                if loc := os.Getenv(environment.LocationEnvVarName); loc != "" {
                    spec.Location = loc
                }
                
                if sub := os.Getenv(environment.SubscriptionIdEnvVarName); sub != "" {
                    spec.Subscription = sub
                }
                
                p.console.Message(ctx, fmt.Sprintf("Creating new environment: %s", envName))
                env, err = p.envManager.Create(ctx, spec)
                if err != nil {
                    return fmt.Errorf("failed to create environment '%s': %w", envName, err)
                }
            } else {
                return fmt.Errorf("failed to load environment '%s': %w", envName, err)
            }
        }

        p.env = env
    }
    
    // Store project path for later use
    p.projectPath = config.Path
    p.configPath = fmt.Sprintf("%s/deploy.ts", config.Path)  // Default to deploy.ts in the path directory
    p.options = config

    return nil
}


func (p *TypeScriptProvider) EnsureEnv(ctx context.Context) error {
	if p.envManager == nil {
		return fmt.Errorf("envManager is nil")
	}
	if p.env == nil {
		return fmt.Errorf("env is nil")
	}

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
	if p.env == nil {
		return nil, fmt.Errorf("environment is not yet initialized; cannot deploy")
	}

	cmd := "npx"
	args := []string{"ts-node", p.configPath}

	// Get environment variables from the environment object first
	envVars := p.env.Environ()
	
	// Add the current OS environment variables
	envVars = append(envVars, os.Environ()...)
	
	// Ensure critical variables are set correctly
	envVars = append(envVars,
		"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
		"AZURE_ENV_NAME="+p.env.Name(),
		"AZURE_LOCATION="+p.env.GetLocation(),
	)
	
	// Add cloud configuration if tenant ID is available
	if tenantID := p.env.GetTenantId(); tenantID != "" {
		envVars = append(envVars, "AZURE_TENANT_ID="+tenantID)
	}

	out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to run deploy.ts: %w", err)
	}

	var outputs map[string]provisioning.OutputParameter
	if err := json.Unmarshal([]byte(out), &outputs); err != nil {
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
	if p.env == nil {
		return nil, fmt.Errorf("environment is not yet initialized; cannot preview")
	}

	cmd := "npx"
	args := []string{"ts-node", p.configPath, "--preview"}
	
	// Get environment variables from the environment object first
	envVars := p.env.Environ()
	
	// Add the current OS environment variables
	envVars = append(envVars, os.Environ()...)
	
	// Ensure critical variables are set correctly
	envVars = append(envVars,
		"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
		"AZURE_ENV_NAME="+p.env.Name(),
		"AZURE_LOCATION="+p.env.GetLocation(),
	)
	
	// Add cloud configuration if tenant ID is available
	if tenantID := p.env.GetTenantId(); tenantID != "" {
		envVars = append(envVars, "AZURE_TENANT_ID="+tenantID)
	}
	out, err := tools.RunCommand(ctx, cmd, args, envVars, p.projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to run deploy.ts preview: %w", err)
	}

	var wrapper struct {
		Preview provisioning.DeploymentPreview `json:"preview"`
	}
	if err := json.Unmarshal([]byte(out), &wrapper); err == nil {
		return &provisioning.DeployPreviewResult{
			Preview: &wrapper.Preview,
		}, nil
	}

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
	if p.env == nil {
		return nil, fmt.Errorf("environment is not yet initialized; cannot destroy")
	}

	cmd := "npx"
	args := []string{"ts-node", p.configPath, "--destroy"}
	
	// Get environment variables from the environment object first
	envVars := p.env.Environ()
	
	// Add the current OS environment variables
	envVars = append(envVars, os.Environ()...)
	
	// Ensure critical variables are set correctly
	envVars = append(envVars,
		"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
		"AZURE_ENV_NAME="+p.env.Name(),
		"AZURE_LOCATION="+p.env.GetLocation(),
	)
	
	// Add cloud configuration if tenant ID is available
	if tenantID := p.env.GetTenantId(); tenantID != "" {
		envVars = append(envVars, "AZURE_TENANT_ID="+tenantID)
	}
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

// Get environment variables from the environment object first
envVars := p.env.Environ()

// Add the current OS environment variables
envVars = append(envVars, os.Environ()...)

// Ensure critical variables are set correctly
envVars = append(envVars,
	"AZURE_SUBSCRIPTION_ID="+p.env.GetSubscriptionId(),
	"AZURE_ENV_NAME="+p.env.Name(),
	"AZURE_LOCATION="+p.env.GetLocation(),
)

// Add cloud configuration if tenant ID is available
if tenantID := p.env.GetTenantId(); tenantID != "" {
	envVars = append(envVars, "AZURE_TENANT_ID="+tenantID)
}
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
// (removed duplicate NewTypeScriptProvider definition)
