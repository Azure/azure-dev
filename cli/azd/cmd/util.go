// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/pflag"
)

// CmdAnnotations on a command
type CmdAnnotations map[string]string

type Asker func(p survey.Prompt, response interface{}) error

func invalidEnvironmentNameMsg(environmentName string) string {
	return fmt.Sprintf(
		"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
		environmentName,
	)
}

// ensureValidEnvironmentName ensures the environment name is valid, if it is not, an error is printed
// and the user is prompted for a new name.
func ensureValidEnvironmentName(ctx context.Context, environmentName *string, suggest string, console input.Console) error {
	for !environment.IsValidEnvironmentName(*environmentName) {
		userInput, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Enter a new environment name:",
			Help: heredoc.Doc(`
			A unique string that can be used to differentiate copies of your application in Azure. 
			
			This value is typically used by the infrastructure as code templates to name the resource group that contains
			the infrastructure for your application and to generate a unique suffix that is applied to resources to prevent
			naming collisions.`),
			DefaultValue: suggest,
		})

		if err != nil {
			return fmt.Errorf("reading environment name: %w", err)
		}

		*environmentName = userInput

		if !environment.IsValidEnvironmentName(*environmentName) {
			fmt.Fprint(console.Handles().Stdout, invalidEnvironmentNameMsg(*environmentName))
		}
	}

	return nil
}

type environmentSpec struct {
	environmentName string
	subscription    string
	location        string
	// suggest is the name that is offered as a suggestion if we need to prompt the user for an environment name.
	suggest string
}

// createEnvironment creates a new named environment. If an environment with this name already
// exists, and error is returned.
func createEnvironment(
	ctx context.Context,
	envSpec environmentSpec,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
) (*environment.Environment, error) {
	if envSpec.environmentName != "" && !environment.IsValidEnvironmentName(envSpec.environmentName) {
		errMsg := invalidEnvironmentNameMsg(envSpec.environmentName)
		fmt.Fprint(console.Handles().Stdout, errMsg)
		return nil, fmt.Errorf(errMsg)
	}

	if err := ensureValidEnvironmentName(ctx, &envSpec.environmentName, envSpec.suggest, console); err != nil {
		return nil, err
	}

	// Ensure the environment does not already exist:
	env, err := environment.GetEnvironment(azdCtx, envSpec.environmentName)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("checking for existing environment: %w", err)
	case err == nil:
		return nil, fmt.Errorf("environment '%s' already exists", envSpec.environmentName)
	}

	env.SetEnvName(envSpec.environmentName)

	if envSpec.subscription != "" {
		env.SetSubscriptionId(envSpec.subscription)
	}

	if envSpec.location != "" {
		env.SetLocation(envSpec.location)
	}

	if err := env.Save(); err != nil {
		return nil, err
	}

	return env, nil
}

func loadEnvironmentIfAvailable() (*environment.Environment, error) {
	azdCtx, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, err
	}
	defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	return environment.GetEnvironment(azdCtx, defaultEnv)
}

func loadOrCreateEnvironment(
	ctx context.Context,
	environmentName string,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
) (*environment.Environment, error) {
	loadOrCreateEnvironment := func() (*environment.Environment, bool, error) {
		// If there's a default environment, use that
		if environmentName == "" {
			var err error
			environmentName, err = azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				return nil, false, fmt.Errorf("getting default environment: %w", err)
			}
		}

		if environmentName != "" {
			env, err := environment.GetEnvironment(azdCtx, environmentName)
			switch {
			case errors.Is(err, os.ErrNotExist):
				msg := fmt.Sprintf("Environment '%s' does not exist, would you like to create it?", environmentName)
				shouldCreate, promptErr := console.Confirm(ctx, input.ConsoleOptions{
					Message:      msg,
					DefaultValue: true,
				})
				if promptErr != nil {
					return nil, false, fmt.Errorf("prompting to create environment '%s': %w", environmentName, promptErr)
				}
				if !shouldCreate {
					return nil, false, fmt.Errorf("environment '%s' not found: %w", environmentName, err)
				}
			case err != nil:
				return nil, false, fmt.Errorf("loading environment '%s': %w", environmentName, err)
			case err == nil:
				return env, false, nil
			}
		}

		// Two cases if we get to here:
		// - The user has not specified an environment name (and there was no default environment set)
		// - The user has specified an environment name, but the named environment didn't exist and they told us they would
		//   like us to create it.
		if environmentName != "" && !environment.IsValidEnvironmentName(environmentName) {
			fmt.Fprintf(
				console.Handles().Stdout,
				"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				environmentName)
			return nil, false, fmt.Errorf(
				"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)",
				environmentName)
		}

		if err := ensureValidEnvironmentName(ctx, &environmentName, "", console); err != nil {
			return nil, false, err
		}

		return environment.EmptyWithRoot(azdCtx.EnvironmentRoot(environmentName)), true, nil
	}

	env, isNew, err := loadOrCreateEnvironment()
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("environment %s does not exist", environmentName)
	case err != nil:
		return nil, err
	}

	if isNew {
		if env.GetEnvName() == "" {
			env.SetEnvName(environmentName)
		}

		if err := env.Save(); err != nil {
			return nil, err
		}

		if err := azdCtx.SetDefaultEnvironmentName(env.GetEnvName()); err != nil {
			return nil, fmt.Errorf("saving default environment: %w", err)
		}
	}

	return env, nil
}

const environmentNameFlag string = "environment"

type envFlag struct {
	environmentName string
}

func (e *envFlag) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(
		&e.environmentName,
		environmentNameFlag,
		"e",
		// Set the default value to AZURE_ENV_NAME value if available
		os.Getenv(environment.EnvNameEnvVarName),
		"The name of the environment to use.")
}

func getResourceGroupFollowUp(
	ctx context.Context,
	formatter output.Formatter,
	projectConfig *project.ProjectConfig,
	resourceManager project.ResourceManager,
	env *environment.Environment,
) (followUp string) {
	if formatter.Kind() != output.JsonFormat {
		subscriptionId := env.GetSubscriptionId()

		if resourceGroupName, err := resourceManager.GetResourceGroupName(ctx, subscriptionId, projectConfig); err == nil {
			followUp = fmt.Sprintf("You can view the resources created under the resource group %s in Azure Portal:\n%s",
				resourceGroupName, output.WithLinkFormat(fmt.Sprintf(
					"https://portal.azure.com/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
					subscriptionId,
					resourceGroupName)))
		}
	}
	return followUp
}

func serviceNameWarningCheck(console input.Console, serviceNameFlag string, commandName string) {
	if serviceNameFlag == "" {
		return
	}

	fmt.Fprintln(
		console.Handles().Stderr,
		output.WithWarningFormat("WARNING: The `--service` flag is deprecated and will be removed in a future release."),
	)
	fmt.Fprintf(console.Handles().Stderr, "Next time use `azd %s <service>`.\n\n", commandName)
}

func getTargetServiceName(
	ctx context.Context,
	projectManager project.ProjectManager,
	projectConfig *project.ProjectConfig,
	commandName string,
	targetServiceName string,
	allFlagValue bool,
) (string, error) {
	if allFlagValue && targetServiceName != "" {
		return "", fmt.Errorf("cannot specify both --all and <service>")
	}

	if !allFlagValue && targetServiceName == "" {
		targetService, err := projectManager.DefaultServiceFromWd(ctx, projectConfig)
		if errors.Is(err, project.ErrNoDefaultService) {
			return "", fmt.Errorf(
				"current working directory is not a project or service directory. Specify a service name to %s a service, "+
					"or specify --all to %s all services",
				commandName,
				commandName,
			)
		} else if err != nil {
			return "", err
		}

		if targetService != nil {
			targetServiceName = targetService.Name
		}
	}

	if targetServiceName != "" && !projectConfig.HasService(targetServiceName) {
		return "", fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
	}

	return targetServiceName, nil
}

// Calculate the total time since t, excluding user interaction time.
func since(t time.Time) time.Duration {
	userInteractTime := tracing.InteractTimeMs.Load()
	return time.Since(t) - time.Duration(userInteractTime)*time.Millisecond
}
