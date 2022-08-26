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

type Manager struct {
	azCli       azcli.AzCli
	env         environment.Environment
	provider    Provider
	interactive bool
	console     input.Console
}

// Prepares for an infrastructure provision operation
func (m *Manager) Preview(ctx context.Context, interactive bool) (*PreviewResult, error) {
	previewResult, err := m.preview(ctx, interactive)
	if err != nil {
		return nil, err
	}

	updated, err := m.ensureParameters(ctx, &previewResult.Preview)
	if err != nil {
		return nil, err
	}

	if updated {
		if err := m.provider.UpdatePlan(ctx, previewResult.Preview); err != nil {
			return nil, fmt.Errorf("updating deployment parameters: %w", err)
		}

		if err := m.env.Save(); err != nil {
			return nil, fmt.Errorf("saving env file: %w", err)
		}
	}

	return previewResult, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context, preview *Preview, interactive bool) (*DeployResult, error) {
	// Ensure that a location has been set prior to provisioning
	location, err := m.ensureLocation(ctx, preview)
	if err != nil {
		return nil, err
	}

	// Apply the infrastructure deployment
	deployResult, err := m.deploy(ctx, location, preview, interactive)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (m *Manager) Destroy(ctx context.Context, preview *Preview, interactive bool) (*DestroyResult, error) {
	// Call provisioning provider to destroy the infrastructure
	destroyResult, err := m.destroy(ctx, preview, interactive)
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

	previewAndReportProgress := func(spinner *spin.Spinner) error {
		previewTask := m.provider.Preview(ctx)

		go func() {
			for progress := range previewTask.Progress() {
				spinner.Println(fmt.Sprintf("%s...", progress.Message))
			}
		}()

		go monitorInteraction(spinner, previewTask.Interactive())

		result, err := previewTask.Await()
		if err != nil {
			return err
		}

		previewResult = result

		return nil
	}

	spinner := spin.NewSpinner("Preparing infrastructure provisioning")
	defer spinner.Stop()

	err := previewAndReportProgress(spinner)

	if err != nil {
		return nil, fmt.Errorf("previewing infrastructure: %w", err)
	}

	spinner.Println("Prepared infrastructure provisioning")

	return previewResult, nil
}

// Applies the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) deploy(ctx context.Context, location string, preview *Preview, interactive bool) (*DeployResult, error) {
	var deployResult *DeployResult

	deployAndReportProgress := func(spinner *spin.Spinner) error {
		provisioningScope := NewSubscriptionProvisioningScope(m.azCli, location, m.env.GetSubscriptionId(), m.env.GetEnvName())
		deployTask := m.provider.Deploy(ctx, preview, provisioningScope)

		go func() {
			for progressReport := range deployTask.Progress() {
				if interactive {
					m.showDeployProgress(*progressReport, spinner)
				}
			}
		}()

		go monitorInteraction(spinner, deployTask.Interactive())

		result, err := deployTask.Await()
		if err != nil {
			return err
		}

		deployResult = result

		return nil
	}

	spinner := spin.NewSpinner("Deploying Azure Resources")
	defer spinner.Stop()

	err := deployAndReportProgress(spinner)

	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	spinner.Println("Azure resource deployment complete")

	return deployResult, nil
}

// Destroys the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) destroy(ctx context.Context, preview *Preview, interactive bool) (*DestroyResult, error) {
	var destroyResult *DestroyResult

	destroyWithProgress := func(spinner *spin.Spinner) error {
		destroyTask := m.provider.Destroy(ctx, preview)

		go func() {
			for destroyProgress := range destroyTask.Progress() {
				spinner.Title(fmt.Sprintf("%s...", destroyProgress.Message))
			}
		}()

		go monitorInteraction(spinner, destroyTask.Interactive())

		result, err := destroyTask.Await()
		if err != nil {
			return err
		}

		destroyResult = result

		return nil
	}

	spinner := spin.NewSpinner("Destroying Azure resources")
	defer spinner.Stop()

	err := destroyWithProgress(spinner)

	if err != nil {
		return nil, fmt.Errorf("error destroying Azure resources: %w", err)
	}

	spinner.Println("Destroyed Azure resources")

	return destroyResult, nil
}

// Creates a progress message from the provisioning progress report
func (m *Manager) showDeployProgress(progressReport DeployProgress, spinner *spin.Spinner) {
	succeededCount := 0

	for _, resourceOperation := range progressReport.Operations {
		if resourceOperation.Properties.ProvisioningState == "Succeeded" {
			succeededCount++
		}
	}

	status := fmt.Sprintf("Creating Azure resources (%d of ~%d completed) ", succeededCount, len(progressReport.Operations))
	spinner.Title(status)
}

// Ensures a provisioning location has been identified within the preview or prompts the user for input
func (m *Manager) ensureLocation(ctx context.Context, preview *Preview) (string, error) {
	var location string

	for key, param := range preview.Parameters {
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

// Ensures the provisioning parameters are valid and prompts the user for input as needed
func (m *Manager) ensureParameters(ctx context.Context, preview *Preview) (bool, error) {
	if len(preview.Parameters) == 0 {
		return false, nil
	}

	updatedParameters := false
	for key, param := range preview.Parameters {
		// If this parameter has a default, then there is no need for us to configure it
		if param.HasDefaultValue() {
			continue
		}
		if !param.HasValue() {
			userValue, err := m.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Please enter a value for the '%s' deployment parameter:", key),
			})

			if err != nil {
				return false, fmt.Errorf("prompting for deployment parameter: %w", err)
			}

			param.Value = userValue

			saveParameter, err := m.console.Confirm(ctx, input.ConsoleOptions{
				Message: "Save the value in the environment for future use",
			})

			if err != nil {
				return false, fmt.Errorf("prompting to save deployment parameter: %w", err)
			}

			if saveParameter {
				m.env.Values[key] = userValue
			}

			updatedParameters = true
		}
	}

	return updatedParameters, nil
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

func monitorInteraction(spinner *spin.Spinner, interactiveChannel <-chan bool) {
	for isInteractive := range interactiveChannel {
		if isInteractive {
			spinner.Stop()
		} else {
			spinner.Start()
		}
	}
}
