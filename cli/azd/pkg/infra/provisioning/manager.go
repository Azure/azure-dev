// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	azCli       azcli.AzCli
	env         *environment.Environment
	provider    Provider
	writer      io.Writer
	console     input.Console
	interactive bool
}

// Prepares for an infrastructure provision operation
func (m *Manager) Plan(ctx context.Context) (*DeploymentPlan, error) {
	deploymentPlan, err := m.plan(ctx)
	if err != nil {
		return nil, err
	}

	return deploymentPlan, nil
}

// Gets the latest deployment details for the specified scope
func (m *Manager) State(ctx context.Context, scope infra.Scope) (*StateResult, error) {
	var stateResult *StateResult

	err := m.runAction(
		ctx,
		"Retrieving Infrastructure State",
		m.interactive,
		func(ctx context.Context, spinner *spin.Spinner) error {
			queryTask := m.provider.State(ctx, scope)

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

			stateResult = result

			return nil
		},
	)

	if err != nil {
		return nil, fmt.Errorf("error retrieving state: %w", err)
	}

	return stateResult, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context, plan *DeploymentPlan, scope infra.Scope) (*DeployResult, error) {
	// Ensure that a location has been set prior to provisioning
	location, err := m.ensureLocation(ctx, &plan.Deployment)
	if err != nil {
		return nil, err
	}

	// Apply the infrastructure deployment
	deployResult, err := m.deploy(ctx, location, plan, scope)
	if err != nil {
		return nil, err
	}

	if err := UpdateEnvironment(m.env, deployResult.Deployment.Outputs); err != nil {
		return nil, fmt.Errorf("updating environment with deployment outputs: %w", err)
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

// Plans the infrastructure provisioning and orchestrates interactive terminal operations
func (m *Manager) plan(ctx context.Context) (*DeploymentPlan, error) {

	planningTask := m.provider.Plan(ctx)
	go func() {
		for progress := range planningTask.Progress() {
			log.Println(progress.Message)
		}
	}()

	deploymentPlan, err := planningTask.Await()
	if err != nil {
		return nil, fmt.Errorf("planning infrastructure provisioning: %w", err)
	}

	return deploymentPlan, nil
}

// Applies the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) deploy(
	ctx context.Context,
	location string,
	plan *DeploymentPlan,
	scope infra.Scope,
) (*DeployResult, error) {

	deployTask := m.provider.Deploy(ctx, plan, scope)

	go func() {
		for progress := range deployTask.Progress() {
			log.Println(progress.Message)
		}
	}()

	deployResult, err := deployTask.Await()
	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	// make sure any spinner is stopped
	m.console.StopSpinner(ctx, "", input.StepDone)

	return deployResult, nil
}

// Destroys the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) (*DestroyResult, error) {
	var destroyResult *DestroyResult

	err := m.runAction(
		ctx,
		"Destroying Azure resources",
		m.interactive,
		func(ctx context.Context, spinner *spin.Spinner) error {
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
		},
	)

	if err != nil {
		return nil, fmt.Errorf("error destroying Azure resources: %w", err)
	}

	m.console.Message(ctx, output.WithSuccessFormat("\nDestroyed Azure resources"))

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
		selected, err := azureutil.PromptLocation(
			ctx,
			m.env,
			"Please select an Azure location to use to store deployment metadata:",
			"",
			m.console,
			m.azCli,
		)
		if err != nil {
			return "", fmt.Errorf("prompting for deployment metadata region: %w", err)
		}

		location = selected
	}

	return location, nil
}

func (m *Manager) runAction(
	ctx context.Context,
	title string,
	interactive bool,
	action func(ctx context.Context, spinner *spin.Spinner) error,
) error {
	var spinner *spin.Spinner

	if interactive {
		spinner, ctx = spin.GetOrCreateSpinner(ctx, m.console.Handles().Stdout, title)
		defer spinner.Stop()
		defer m.console.SetWriter(nil)

		spinner.Start()
		m.console.SetWriter(spinner)
	}

	return action(ctx, spinner)
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
func NewManager(
	ctx context.Context,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	interactive bool,
	azCli azcli.AzCli,
	console input.Console,
	commandRunner exec.CommandRunner,
) (*Manager, error) {
	infraProvider, err := NewProvider(ctx, console, azCli, commandRunner, env, projectPath, infraOptions)
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
		writer:      console.GetWriter(),
		console:     console,
		interactive: interactive,
	}, nil
}
