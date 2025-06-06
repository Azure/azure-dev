// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package workflow

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/joho/godotenv"
)

// AzdCommandRunner abstracts the execution of an azd command given an set of arguments and context.
type AzdCommandRunner interface {
	SetArgs(args []string)
	ExecuteContext(ctx context.Context) error
}

// RunnerConfig contains configuration options for the workflow runner
type RunnerConfig struct {
	// ProjectRoot should be the directory containing azure.yaml
	ProjectRoot string
}

// Runner is responsible for executing a workflow
type Runner struct {
	azdRunner   AzdCommandRunner
	console     input.Console
	projectRoot string
	envManager  environment.Manager
}

// NewRunner creates a new instance of the Runner.
// config.ProjectRoot should be the directory containing azure.yaml
func NewRunner(azdRunner AzdCommandRunner, console input.Console, config RunnerConfig) *Runner {
	return &Runner{
		azdRunner:   azdRunner,
		console:     console,
		projectRoot: config.ProjectRoot,
	}
}

// SetEnvironmentManager sets the environment manager for the runner
// This allows workflows to access and create environments when needed
func (r *Runner) SetEnvironmentManager(envManager environment.Manager) {
	r.envManager = envManager
}

// Run executes the specified workflow against the root cobra command
func (r *Runner) Run(ctx context.Context, workflow *Workflow) error {
	// If environment manager is available, try to ensure environment is available
	if r.envManager != nil {
		if err := r.ensureEnvironment(ctx); err != nil {
			return fmt.Errorf("failed to ensure environment: %w", err)
		}
	}

	for _, step := range workflow.Steps {
		args := step.AzdCommand.Args
		hasCwd := false
		for _, arg := range args {
			if arg == "--cwd" || strings.HasPrefix(arg, "--cwd=") || arg == "-C" {
				hasCwd = true
				break
			}
		}
		if !hasCwd && r.projectRoot != "" {
			// Prepend --cwd <projectRoot>
			args = append([]string{"--cwd", r.projectRoot}, args...)
		}
		r.azdRunner.SetArgs(args)

		if err := r.azdRunner.ExecuteContext(ctx); err != nil {
			return fmt.Errorf("error executing step command '%s': %w", strings.Join(args, " "), err)
		}
	}

	return nil
}

// ensureEnvironment checks for the environment name in multiple locations
// and creates the environment if it doesn't exist
func (r *Runner) ensureEnvironment(ctx context.Context) error {
	// Priority order for finding environment name:
	// 1. AZURE_ENV_NAME from OS environment
	// 2. AZURE_ENV_NAME from .env file in the project directory
	// 3. Default environment name from project context

	envName := os.Getenv(environment.EnvNameEnvVarName)
	
	// If not in OS environment, try to load from .env file
	if envName == "" && r.projectRoot != "" {
		dotEnvPath := fmt.Sprintf("%s/.env", r.projectRoot)
		if fileExists(dotEnvPath) {
			envVars, err := godotenv.Read(dotEnvPath)
			if err == nil && envVars != nil {
				if name, ok := envVars[environment.EnvNameEnvVarName]; ok {
					envName = name
				}
			}
		}
	}

	// If we have an environment name, try to load it or create it
	if envName != "" {
		env, err := r.envManager.Get(ctx, envName)
		if err != nil {
			if err == environment.ErrNotFound {
				// Environment doesn't exist, create it
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

				env, err = r.envManager.Create(ctx, spec)
				if err != nil {
					return fmt.Errorf("failed to create environment '%s': %w", envName, err)
				}
				
				// Save the environment with the new configuration
				if err := r.envManager.Save(ctx, env); err != nil {
					return fmt.Errorf("failed to save environment: %w", err)
				}
			} else {
				return fmt.Errorf("failed to get environment '%s': %w", envName, err)
			}
		}
	}

	return nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
