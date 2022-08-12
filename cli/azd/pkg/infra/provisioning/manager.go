package provisioning

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	azCli       azcli.AzCli
	env         environment.Environment
	provider    Provider
	interactive bool
	console     input.Console
}

// Infrastructure deployment options
type DeployOptions struct {
	// Whether or not the session supports interactive user input
	Interactive bool
}

// Infrastructure destroy options
type DestroyOptions struct {
	// Whether or not the session supports interactive user input
	Interactive bool
	// Whether or not to force the deletion of resources without prompting the user
	Force bool
	// Whether or not to purge any key vaults associated with the deployment
	Purge bool
}

// Prepares for an infrastructure provision operation
func (m *Manager) Preview(ctx context.Context, interactive bool) (*PreviewResult, error) {
	previewResult, err := m.preview(ctx, interactive)
	if err != nil {
		return nil, err
	}

	return previewResult, nil
}

// Gets the latest deployment details for the specified scope
func (m *Manager) GetDeployment(ctx context.Context, scope Scope, interactive bool) (*DeployResult, error) {
	var deployResult *DeployResult

	err := m.runAction("Retrieving Azure Deployment", interactive, func(spinner *spin.Spinner) error {
		queryTask := m.provider.GetDeployment(ctx, scope)

		go func() {
			for progress := range queryTask.Progress() {
				m.updateSpinnerTitle(spinner, progress.Message)
			}
		}()

		go m.monitorInteraction(spinner, queryTask.Interactive())

		result, err := queryTask.Await()
		if err != nil {
			return err
		}

		deployResult = result

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error retrieving deployment: %w", err)
	}

	m.console.Message(ctx, "Azure env refresh completed 👍")

	return deployResult, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context, deployment *Deployment, scope Scope, options DeployOptions) (*DeployResult, error) {
	// Ensure that a location has been set prior to provisioning
	location, err := m.ensureLocation(ctx, deployment)
	if err != nil {
		return nil, err
	}

	// Apply the infrastructure deployment
	deployResult, err := m.deploy(ctx, location, deployment, scope, options)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (m *Manager) Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) (*DestroyResult, error) {
	// Call provisioning provider to destroy the infrastructure
	destroyResult, err := m.destroy(ctx, deployment, options)
	if err != nil {
		return nil, err
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range destroyResult.Outputs {
		delete(m.env.Values, outputName)
	}

	// Update environment files to remove invalid infrastructure parameters
	if err := m.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

// Previews the infrastructure provisioning and orchestrates interactive terminal operations
func (m *Manager) preview(ctx context.Context, interactive bool) (*PreviewResult, error) {
	var previewResult *PreviewResult

	err := m.runAction("Preparing infrastructure provisioning", interactive, func(spinner *spin.Spinner) error {
		previewTask := m.provider.Preview(ctx)

		go func() {
			for progress := range previewTask.Progress() {
				m.updateSpinnerTitle(spinner, progress.Message)
			}
		}()

		go m.monitorInteraction(spinner, previewTask.Interactive())

		result, err := previewTask.Await()
		if err != nil {
			return err
		}

		previewResult = result

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("previewing infrastructure: %w", err)
	}

	m.console.Message(ctx, "\nPrepared infrastructure provisioning 👍")

	return previewResult, nil
}

// Applies the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) deploy(ctx context.Context, location string, deployment *Deployment, scope Scope, options DeployOptions) (*DeployResult, error) {
	var deployResult *DeployResult

	err := m.runAction("🚀 Provisioning Azure resources", options.Interactive, func(spinner *spin.Spinner) error {
		deployTask := m.provider.Deploy(ctx, deployment, scope)

		go func() {
			for progress := range deployTask.Progress() {
				m.updateSpinnerTitle(spinner, progress.Message)
			}
		}()

		go m.monitorInteraction(spinner, deployTask.Interactive())

		result, err := deployTask.Await()
		if err != nil {
			return err
		}

		deployResult = result

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	m.console.Message(ctx, "\nAzure resource provisioning completed successfully 👍")

	return deployResult, nil
}

// Destroys the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) (*DestroyResult, error) {
	var destroyResult *DestroyResult

	err := m.runAction("Destroying Azure resources", options.Interactive, func(spinner *spin.Spinner) error {
		destroyTask := m.provider.Destroy(ctx, deployment, options)

		go func() {
			for progress := range destroyTask.Progress() {
				m.updateSpinnerTitle(spinner, progress.Message)
			}
		}()

		go m.monitorInteraction(spinner, destroyTask.Interactive())

		result, err := destroyTask.Await()
		if err != nil {
			return err
		}

		destroyResult = result

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error destroying Azure resources: %w", err)
	}

	m.console.Message(ctx, "\nDestroyed Azure resources 👍")

	return destroyResult, nil
}

// Ensures a provisioning location has been identified within the deployment or prompts the user for input
func (m *Manager) ensureLocation(ctx context.Context, deployment *Deployment) (string, error) {
	var location string

	for key, param := range deployment.Parameters {
		if key == "location" {
			location = fmt.Sprint(param.Value)
			if strings.TrimSpace(location) != "" {
				return location, nil
			}
		}
	}

	for location == "" {
		// TODO: We will want to store this information somewhere (so we don't have to prompt the
		// user on every deployment if they don't have a `location` parameter in their bicep file.
		// When we store it, we should store it /per environment/ not as a property of the entire
		// project.
		selected, err := m.console.PromptLocation(ctx, "Please select an Azure location to use to store deployment metadata:")
		if err != nil {
			return "", fmt.Errorf("prompting for deployment metadata region: %w", err)
		}

		location = selected
	}

	return location, nil
}

func (m *Manager) runAction(title string, interactive bool, action func(spinner *spin.Spinner) error) error {
	var spinner *spin.Spinner

	if interactive {
		spinner = spin.NewSpinner(title)
		defer spinner.Stop()
		defer m.console.SetWriter(nil)

		spinner.Start()
		m.console.SetWriter(spinner)
	}

	return action(spinner)
}

// Updates the spinner title during interactive console session
func (m *Manager) updateSpinnerTitle(spinner *spin.Spinner, message string) {
	if spinner == nil {
		return
	}

	spinner.Title(fmt.Sprintf("%s...", message))
}

// Monitors the interactive channel and starts/stops the terminal spinner as needed
func (m *Manager) monitorInteraction(spinner *spin.Spinner, interactiveChannel <-chan bool) {
	for isInteractive := range interactiveChannel {
		if spinner == nil {
			continue
		}

		if isInteractive {
			spinner.Stop()
		} else {
			spinner.Start()
		}
	}
}

// Creates a new instance of the Provisioning Manager
func NewManager(ctx context.Context, env environment.Environment, projectPath string, options Options, interactive bool, console input.Console, cliArgs bicep.NewBicepCliArgs) (*Manager, error) {
	infraProvider, err := NewProvider(&env, projectPath, options, console, cliArgs)
	if err != nil {
		return nil, fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return nil, err
	}

	if console == nil {
		console = input.NewConsole(interactive)
	}

	return &Manager{
		azCli:       cliArgs.AzCli,
		env:         env,
		provider:    infraProvider,
		interactive: interactive,
		console:     console,
	}, nil
}
