// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// TypeScriptProvider exposes infrastructure provisioning using TypeScript configuration
type TypeScriptProvider struct {
	envManager  environment.Manager
	env         *environment.Environment
	projectPath string
	options     provisioning.Options
	console     input.Console
	configPath  string
	prompters   prompt.Prompter // Added prompter for interactive subscription/location selection
}

func NewTypeScriptProvider(
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
) provisioning.Provider {
	if envManager == nil {
		panic("NewTypeScriptProvider: envManager must not be nil")
	}

	return &TypeScriptProvider{
		envManager: envManager,
		env:        env, // can be nil – handle this in methods
		console:    console,
		prompters:  prompters,
	}
}

// Name gets the name of the infra provider
func (p *TypeScriptProvider) Name() string {
	return "typescript"
}

// SimpleTool implements the tools.ExternalTool interface for basic commands
type SimpleTool struct {
	command string
	args    []string
	name    string
	installUrl string
}

func (t *SimpleTool) CheckInstalled(ctx context.Context) error {
	err := tools.ToolInPath(t.command)
	if err != nil {
		return err
	}
	return nil
}

func (t *SimpleTool) InstallUrl() string {
	return t.installUrl
}

func (t *SimpleTool) Name() string {
	return t.name
}

func (p *TypeScriptProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{
		&SimpleTool{
			command: "node",
			args:    []string{"--version"},
			name:    "Node.js",
			installUrl: "https://nodejs.org/",
		},
		&SimpleTool{
			command: "npm",
			args:    []string{"--version"},
			name:    "npm",
			installUrl: "https://www.npmjs.com/",
		},
		&SimpleTool{
			command: "docker",
			args:    []string{"--version"},
			name:    "Docker",
			installUrl: "https://docs.docker.com/get-docker/",
		},
	}
}

// Initialize initializes provider state from the options
func (p *TypeScriptProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	// Store project path for later use
	p.projectPath = projectPath
	log.Printf("TypeScript provider initialized with path: %s", p.projectPath)
	
	// Normalize path separators for cross-platform compatibility
	if filepath.Base(p.projectPath) == "infra" {
		// If the path already ends with the infra directory, use it as is
		log.Printf("Using project path as infra directory")
		p.configPath = filepath.Join(p.projectPath, "deploy.ts")
	} else {
		// Otherwise, this is the project root, so use the infra subdirectory
		log.Printf("Using infra subdirectory")
		p.configPath = filepath.Join(p.projectPath, "infra", "deploy.ts")
	}
	p.console.ShowSpinner(ctx, "Initialize bicep provider", input.Step)
	err := p.EnsureEnv(ctx)
	p.console.StopSpinner(ctx, "", input.Step)
	p.options = options

	return err
}

func (p *TypeScriptProvider) EnsureEnv(ctx context.Context) error {
	if p.envManager == nil {
		return fmt.Errorf("envManager is nil")
	}
	if p.env == nil {
		return fmt.Errorf("env is nil")
	}

	// Pass the prompter to ensure proper interactive selection of subscription and location
	return provisioning.EnsureSubscriptionAndLocation(
		ctx,
		p.envManager,
		p.env,
		p.prompters,
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
	
	// Make sure we have subscription and location before proceeding
	if err := p.EnsureEnv(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure environment: %w", err)
	}
	
	// Add clear logging about the execution context
	log.Printf("TypeScript Provider - Deploy starting with projectPath: %s configPath: %s", p.projectPath, p.configPath)

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
	
	// First, check if TypeScript compilation is needed
	compileCmd := "npm"
	compileArgs := []string{"run", "build"}
	
	// Add clear logging of environment variables for debugging
	log.Printf("Environment Variables for deployment:")
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "AZURE_") {
			log.Printf("  %s", envVar)
		}
	}

	// Compile TypeScript to JavaScript - use infraPath to build in the right directory
	infraPath := p.getInfraPath()
	_, err := tools.RunCommand(ctx, compileCmd, compileArgs, envVars, infraPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compile TypeScript in %s: %w", infraPath, err)
	}

	// Check if the compiled JavaScript file exists - use helper methods for consistent paths
	compiledJsPath := p.getCompiledJsPath()
	
	// Log paths for debugging
	log.Printf("Infrastructure path: %s", infraPath)
	log.Printf("Dist folder: %s", p.getDistPath())
	
	// Log the paths for debugging
	log.Printf("Project path: %s", p.projectPath)
	log.Printf("Looking for compiled JavaScript at: %s", compiledJsPath)
	
	if _, err := os.Stat(compiledJsPath); os.IsNotExist(err) {
		// If not found, try building it now
		buildCmd := "npm"
		buildArgs := []string{"run", "build"}
		
		// We can use the infraPath that was already calculated correctly
		log.Printf("Running build command in: %s", p.getInfraPath())
		// Verify directory exists before trying to run npm
		if _, statErr := os.Stat(p.getInfraPath()); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("build directory %s does not exist", p.getInfraPath())
		}
		_, buildErr := tools.RunCommand(ctx, buildCmd, buildArgs, envVars, p.getInfraPath())
		if buildErr != nil {
			return nil, fmt.Errorf("failed to compile TypeScript at deploy time in %s: %w", p.getInfraPath(), buildErr)
		}
		
		// Check again after building
		if _, checkErr := os.Stat(compiledJsPath); checkErr != nil {
			return nil, fmt.Errorf("compiled deploy.js not found at %s after build: %w", compiledJsPath, checkErr)
		}
	}

	// Run the compiled JavaScript file instead of using ts-node
	cmd := "node"
	args := []string{compiledJsPath}
	
	// Now we already have infraPath calculated correctly, so we can use it directly for execution
	log.Printf("Executing Node.js in directory: %s", p.getInfraPath())
	// Verify directory exists before trying to execute
	if _, statErr := os.Stat(p.getInfraPath()); os.IsNotExist(statErr) {
		return nil, fmt.Errorf("execution directory %s does not exist", p.getInfraPath())
	}

	out, err := tools.RunCommand(ctx, cmd, args, envVars, p.getInfraPath())
	if err != nil {
		// Check for common errors in the output
		if strings.Contains(out, "InvalidSubscriptionId") {
			return nil, fmt.Errorf("invalid or missing subscription ID, please run 'azd auth login' and try again: %w", err)
		}
		if strings.Contains(out, "MissingSubscriptionRegistration") {
			return nil, fmt.Errorf("Azure subscription is not registered for the required resource providers: %w", err)
		}
		if strings.Contains(out, "AuthorizationFailed") {
			return nil, fmt.Errorf("authorization failed, please check your credentials with 'azd auth login': %w", err)
		}
		if strings.Contains(out, "InvalidResourceGroupLocation") {
			return nil, fmt.Errorf("resource group location conflict. The deploy.ts script has been updated to handle this automatically. Please try again: %w", err)
		}
		
		// Provide detailed error information to help with debugging
		return nil, fmt.Errorf("failed to run deploy.js: %w\nOutput: %s", err, out)
	}

	var outputs map[string]provisioning.OutputParameter
	if err := json.Unmarshal([]byte(out), &outputs); err != nil {
		var wrapper struct {
			Outputs map[string]provisioning.OutputParameter `json:"outputs"`
		}
		if err2 := json.Unmarshal([]byte(out), &wrapper); err2 == nil {
			outputs = wrapper.Outputs
		} else {
			return nil, fmt.Errorf("failed to parse deploy.js output: %w", err)
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
	
	// Make sure we have subscription and location before proceeding
	if err := p.EnsureEnv(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure environment: %w", err)
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
	
	// Make sure we have subscription and location before proceeding
	if err := p.EnsureEnv(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	compiledDestroyJsPath := filepath.Join(p.getDistPath(), "destroy.js")

	cmd := "node"
	args := []string{compiledDestroyJsPath}

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
		return nil, fmt.Errorf("failed to run destroy.ts: %w", err)
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

// DeployContainer deploys the container app
func (p *TypeScriptProvider) DeployContainer(ctx context.Context) (*provisioning.DeployResult, error) {
	if p.env == nil {
		return nil, fmt.Errorf("environment is not yet initialized; cannot deploy container")
	}

	// Make sure we have subscription and location before proceeding
	if err := p.EnsureEnv(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	// Add clear logging about the execution context
	log.Printf("TypeScript Provider - Deploy Container starting for projectPath: %s", p.projectPath)

	// Check for build-and-deploy.ts file
	buildDeployPath := filepath.Join(p.getInfraPath(), "build-and-deploy.ts")
	if _, err := os.Stat(buildDeployPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("build-and-deploy.ts file not found at %s", buildDeployPath)
	}

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

	// First compile the TypeScript build-and-deploy file
	compileCmd := "npm"
	compileArgs := []string{"run", "build"}

	// Compile TypeScript to JavaScript
	_, err := tools.RunCommand(ctx, compileCmd, compileArgs, envVars, p.getInfraPath())
	if err != nil {
		return nil, fmt.Errorf("failed to compile build-and-deploy.ts: %w", err)
	}

	// Run the build-and-deploy script
	buildDeployJsPath := filepath.Join(p.getDistPath(), "build-and-deploy.js")
	cmd := "node"
	args := []string{buildDeployJsPath}

	log.Printf("Executing build-and-deploy.js in directory: %s", p.getInfraPath())
	out, err := tools.RunCommand(ctx, cmd, args, envVars, p.getInfraPath())
	if err != nil {
		return nil, fmt.Errorf("failed to run build-and-deploy.js: %w\nOutput: %s", err, out)
	}

	var outputs map[string]provisioning.OutputParameter
	if err := json.Unmarshal([]byte(out), &outputs); err != nil {
		var wrapper struct {
			Outputs map[string]provisioning.OutputParameter `json:"outputs"`
		}
		if err2 := json.Unmarshal([]byte(out), &wrapper); err2 == nil {
			outputs = wrapper.Outputs
		} else {
			return nil, fmt.Errorf("failed to parse build-and-deploy.js output: %w", err)
		}
	}

	return &provisioning.DeployResult{
		Deployment: &provisioning.Deployment{
			Outputs: outputs,
		},
	}, nil
}

// getInfraPath returns the correct infrastructure directory path
// If the project path already ends with "/infra", it returns the project path as is
// Otherwise, it appends "/infra" to the project path
func (p *TypeScriptProvider) getInfraPath() string {
	// Use filepath.Base for cross-platform path handling
	if filepath.Base(p.projectPath) == "infra" {
		return p.projectPath
	}
	return filepath.Join(p.projectPath, "infra")
}

// getDistPath returns the path to the dist directory containing compiled JS
func (p *TypeScriptProvider) getDistPath() string {
	return filepath.Join(p.getInfraPath(), "dist")
}

// getCompiledJsPath returns the path to the compiled deploy.js file
func (p *TypeScriptProvider) getCompiledJsPath() string {
	return filepath.Join(p.getDistPath(), "deploy.js")
}
