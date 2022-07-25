package provisioning

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/theckman/yacspin"
)

type Manager struct {
	azCli       tools.AzCli
	env         environment.Environment
	provider    Provider
	interactive bool
	console     input.Console
}

// Creates the Azure infrastructure for the specified project
func (pm *Manager) Create(ctx context.Context, interactive bool) (*ApplyResult, error) {
	planResult, err := pm.plan(ctx, interactive)
	if err != nil {
		return nil, err
	}

	updated, err := pm.ensureParameters(ctx, &planResult.Plan)
	if err != nil {
		return nil, err
	}

	location, err := pm.ensureLocation(ctx, &planResult.Plan)
	if err != nil {
		return nil, err
	}

	if updated {
		if err := pm.provider.UpdatePlan(ctx, planResult.Plan); err != nil {
			return nil, fmt.Errorf("updating deployment parameters: %w", err)
		}

		if err := pm.env.Save(); err != nil {
			return nil, fmt.Errorf("saving env file: %w", err)
		}
	}

	applyResult, err := pm.apply(ctx, location, &planResult.Plan, interactive)
	if err != nil {
		return nil, err
	}

	return applyResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (pm *Manager) Destroy(ctx context.Context, interactive bool) (*DestroyResult, error) {
	planResult, err := pm.plan(ctx, interactive)
	if err != nil {
		return nil, err
	}

	// Call provisioning provider to destroy the infrastructure
	destroyResult, err := pm.destroy(ctx, &planResult.Plan, interactive)
	if err != nil {
		return nil, err
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range destroyResult.Outputs {
		delete(pm.env.Values, outputName)
	}

	// Update environment files to remove invalid infrastructure parameters
	if err := pm.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

// Plans the infrastructure provisioning and orchestrates interactive terminal operations
func (pm *Manager) plan(ctx context.Context, interactive bool) (*PlanResult, error) {
	var planResult *PlanResult

	planAndReportProgress := func(showProgress func(string)) error {
		planTask := pm.provider.Plan(ctx)

		go func() {
			for progress := range planTask.Progress() {
				showProgress(fmt.Sprintf("%s...", progress.Message))
			}
		}()

		planResult = planTask.Result()
		if planTask.Error != nil {
			return fmt.Errorf("compiling infra template: %w", planTask.Error)
		}

		return nil
	}

	err := spin.RunWithUpdater("Planning infrastructure provisioning", planAndReportProgress,
		func(s *yacspin.Spinner, deploySuccess bool) {
			s.StopMessage("Created infrastructure provisioning plan\n")
		})

	if err != nil {
		return nil, fmt.Errorf("error planning infrastructure deployment: %w", err)
	}

	return planResult, nil
}

// Applies the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (pm *Manager) apply(ctx context.Context, location string, plan *Plan, interactive bool) (*ApplyResult, error) {
	var applyResult *ApplyResult

	deployAndReportProgress := func(showProgress func(string)) error {
		provisioningScope := NewSubscriptionProvisioningScope(pm.azCli, location, pm.env.GetSubscriptionId(), pm.env.GetEnvName())
		applyTask := pm.provider.Apply(ctx, plan, provisioningScope)

		go func() {
			for progressReport := range applyTask.Progress() {
				if interactive {
					pm.showApplyProgress(*progressReport, showProgress)
				}
			}
		}()

		applyResult = applyTask.Result()
		if applyTask.Error != nil {
			return applyTask.Error
		}

		return nil
	}

	err := spin.RunWithUpdater("Creating Azure resources ", deployAndReportProgress,
		func(s *yacspin.Spinner, deploySuccess bool) {
			s.StopMessage("Created Azure resources\n")
		})

	if err != nil {
		return nil, fmt.Errorf("error applying infrastructure: %w", err)
	}

	return applyResult, nil
}

// Destroys the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (pm *Manager) destroy(ctx context.Context, plan *Plan, interactive bool) (*DestroyResult, error) {
	var destroyResult *DestroyResult

	deleteWithProgress := func(showProgress func(string)) error {
		destroyTask := pm.provider.Destroy(ctx, plan)

		go func() {
			for destroyProgress := range destroyTask.Progress() {
				showProgress(fmt.Sprintf("%s...", destroyProgress.Message))
			}
		}()

		destroyResult = destroyTask.Result()
		if destroyTask.Error != nil {
			return fmt.Errorf("error destroying resources: %w", destroyTask.Error)
		}

		return nil
	}

	err := spin.RunWithUpdater("Destroying Azure resources ", deleteWithProgress,
		func(s *yacspin.Spinner, success bool) {
			var stopMessage string
			if success {
				stopMessage = "Destroyed Azure resources"
			} else {
				stopMessage = "Error while destroying Azure resources"
			}

			s.StopMessage(stopMessage)
		})

	if err != nil {
		return nil, fmt.Errorf("error destroying Azure resources: %w", err)
	}

	return destroyResult, nil
}

// Creates a progress message from the provisioning progress report
func (pm *Manager) showApplyProgress(progressReport ApplyProgress, showProgress func(string)) {
	succeededCount := 0

	for _, resourceOperation := range progressReport.Operations {
		if resourceOperation.Properties.ProvisioningState == "Succeeded" {
			succeededCount++
		}
	}

	status := fmt.Sprintf("Creating Azure resources (%d of ~%d completed) ", succeededCount, len(progressReport.Operations))
	showProgress(status)
}

// Ensures a provisioning location has been identified within the plan or prompts the user for input
func (pm *Manager) ensureLocation(ctx context.Context, plan *Plan) (string, error) {
	var location string

	for key, param := range plan.Parameters {
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
		selected, err := pm.console.PromptLocation(ctx, "Please select an Azure location to use to store deployment metadata:")
		if err != nil {
			return "", fmt.Errorf("prompting for deployment metadata region: %w", err)
		}

		location = selected
	}

	return location, nil
}

// Ensures the provisioning parameters are valid and prompts the user for input as needed
func (pm *Manager) ensureParameters(ctx context.Context, plan *Plan) (bool, error) {
	if len(plan.Parameters) == 0 {
		return false, nil
	}

	updatedParameters := false
	for key, param := range plan.Parameters {
		// If this parameter has a default, then there is no need for us to configure it
		if param.HasDefaultValue() {
			continue
		}
		if !param.HasValue() {
			userValue, err := pm.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Please enter a value for the '%s' deployment parameter:", key),
			})

			if err != nil {
				return false, fmt.Errorf("prompting for deployment parameter: %w", err)
			}

			param.Value = userValue

			saveParameter, err := pm.console.Confirm(ctx, input.ConsoleOptions{
				Message: "Save the value in the environment for future use",
			})

			if err != nil {
				return false, fmt.Errorf("prompting to save deployment parameter: %w", err)
			}

			if saveParameter {
				pm.env.Values[key] = userValue
			}

			updatedParameters = true
		}
	}

	return updatedParameters, nil
}

// Creates a new instance of the Provisioning Manager
func NewManager(ctx context.Context, env environment.Environment, projectPath string, options Options, interactive bool, azCli tools.AzCli) (*Manager, error) {
	infraProvider, err := NewProvider(&env, projectPath, options, azCli)
	if err != nil {
		return nil, fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return nil, err
	}

	return &Manager{
		azCli:       azCli,
		env:         env,
		provider:    infraProvider,
		interactive: interactive,
		console:     input.NewAskerConsole(interactive),
	}, nil
}
