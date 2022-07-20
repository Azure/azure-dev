package provisioning

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/theckman/yacspin"
)

type ProvisioningManager struct {
	azCli    tools.AzCli
	asker    input.Asker
	env      environment.Environment
	provider InfraProvider
}

func (pm *ProvisioningManager) Create(ctx context.Context, interactive bool) (*ProvisionApplyResult, error) {
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

func (pm *ProvisioningManager) plan(ctx context.Context, interactive bool) (*ProvisionPlanResult, error) {
	var planResult *ProvisionPlanResult

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

func (pm *ProvisioningManager) apply(ctx context.Context, location string, plan *ProvisioningPlan, interactive bool) (*ProvisionApplyResult, error) {
	var applyResult *ProvisionApplyResult

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

func (pm *ProvisioningManager) showApplyProgress(progressReport ProvisionApplyProgress, showProgress func(string)) {
	succeededCount := 0

	for _, resourceOperation := range progressReport.Operations {
		if resourceOperation.Properties.ProvisioningState == "Succeeded" {
			succeededCount++
		}
	}

	status := fmt.Sprintf("Creating Azure resources (%d of ~%d completed) ", succeededCount, len(progressReport.Operations))
	showProgress(status)
}

func (pm *ProvisioningManager) ensureLocation(ctx context.Context, plan *ProvisioningPlan) (string, error) {
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
		selected, err := input.PromptLocation(ctx, "Please select an Azure location to use to store deployment metadata:", pm.asker)
		if err != nil {
			return "", fmt.Errorf("prompting for deployment metadata region: %w", err)
		}

		location = selected
	}

	return location, nil
}

func (pm *ProvisioningManager) ensureParameters(ctx context.Context, plan *ProvisioningPlan) (bool, error) {
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
			var val string
			if err := pm.asker(&survey.Input{
				Message: fmt.Sprintf("Please enter a value for the '%s' deployment parameter:", key),
			}, &val); err != nil {
				return false, fmt.Errorf("prompting for deployment parameter: %w", err)
			}

			param.Value = val

			saveParameter := true
			if err := pm.asker(&survey.Confirm{
				Message: "Save the value in the environment for future use",
			}, &saveParameter); err != nil {
				return false, fmt.Errorf("prompting to save deployment parameter: %w", err)
			}

			if saveParameter {
				pm.env.Values[key] = val
			}

			updatedParameters = true
		}
	}

	return updatedParameters, nil
}

func NewProvisioningManager(ctx context.Context, env environment.Environment, projectPath string, options InfrastructureOptions, azCli tools.AzCli) (*ProvisioningManager, error) {
	infraProvider, err := NewInfraProvider(&env, projectPath, options, azCli)
	if err != nil {
		return nil, fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return nil, err
	}

	return &ProvisioningManager{
		azCli:    azCli,
		env:      env,
		provider: infraProvider,
	}, nil
}
