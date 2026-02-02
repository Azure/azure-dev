// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type serviceTasksFlags struct {
	serviceName string
	task        string
	*internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func newServiceTasksFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *serviceTasksFlags {
	flags := &serviceTasksFlags{
		EnvFlag: &internal.EnvFlag{},
	}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *serviceTasksFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(
		&f.serviceName,
		"service",
		"",
		"The name of the service to run the task on.",
	)

	local.StringVar(
		&f.task,
		"task",
		"",
		"The task to run on the service. Supports task arguments separated by semicolon (e.g., 'swap;src=staging;dst=production').",
	)
}

func newServiceTasksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "service-tasks",
		Short: "Manage service tasks.",
		Long: "Execute tasks on Azure services. Tasks are specific operations that can be performed " +
			"on a deployed service, such as swapping deployment slots for App Service.",
	}
}

type serviceTasksAction struct {
	flags           *serviceTasksFlags
	projectConfig   *project.ProjectConfig
	serviceManager  project.ServiceManager
	resourceManager project.ResourceManager
	importManager   *project.ImportManager
	env             *environment.Environment
	console         input.Console
}

func newServiceTasksAction(
	flags *serviceTasksFlags,
	projectConfig *project.ProjectConfig,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	importManager *project.ImportManager,
	env *environment.Environment,
	console input.Console,
) actions.Action {
	return &serviceTasksAction{
		flags:           flags,
		projectConfig:   projectConfig,
		serviceManager:  serviceManager,
		resourceManager: resourceManager,
		importManager:   importManager,
		env:             env,
		console:         console,
	}
}

func (a *serviceTasksAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Get the list of services
	stableServices, err := a.importManager.ServiceStable(ctx, a.projectConfig)
	if err != nil {
		return nil, err
	}

	if len(stableServices) == 0 {
		return nil, errors.New("no services found in the project")
	}

	// Determine which service to use
	var selectedService *project.ServiceConfig
	if a.flags.serviceName != "" {
		// Service specified via flag
		for _, svc := range stableServices {
			if svc.Name == a.flags.serviceName {
				selectedService = svc
				break
			}
		}
		if selectedService == nil {
			return nil, fmt.Errorf("service '%s' not found", a.flags.serviceName)
		}
	} else {
		// Prompt user to select a service
		serviceNames := make([]string, len(stableServices))
		for i, svc := range stableServices {
			serviceNames[i] = svc.Name
		}

		selectedIndex, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Select a service:",
			Options: serviceNames,
		})
		if err != nil {
			return nil, fmt.Errorf("selecting service: %w", err)
		}

		selectedService = stableServices[selectedIndex]
	}

	// Get the service target
	serviceTarget, err := a.serviceManager.GetServiceTarget(ctx, selectedService)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	// Get available tasks for this service
	tasks := serviceTarget.Tasks(ctx, selectedService)

	if len(tasks) == 0 {
		a.console.Message(ctx, fmt.Sprintf(
			"There are currently no tasks available for the service type '%s'.",
			selectedService.Host,
		))
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "No tasks available.",
			},
		}, nil
	}

	// Parse task name and arguments from the flag
	var selectedTask project.ServiceTask
	var taskArgs string

	if a.flags.task != "" {
		// Task specified via flag
		taskParts := strings.SplitN(a.flags.task, ";", 2)
		taskName := taskParts[0]
		if len(taskParts) > 1 {
			taskArgs = taskParts[1]
		}

		// Find the task
		taskFound := false
		for _, task := range tasks {
			if task.Name == taskName {
				selectedTask = task
				taskFound = true
				break
			}
		}

		if !taskFound {
			return nil, fmt.Errorf("task '%s' is not supported by service type '%s'", taskName, selectedService.Host)
		}
	} else {
		// Prompt user to select a task
		taskNames := make([]string, len(tasks))
		for i, task := range tasks {
			taskNames[i] = task.Name
		}

		selectedIndex, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Select a task:",
			Options: taskNames,
		})
		if err != nil {
			return nil, fmt.Errorf("selecting task: %w", err)
		}

		selectedTask = tasks[selectedIndex]
	}

	// Get the target resource
	targetResource, err := a.resourceManager.GetTargetResource(ctx, a.env.GetSubscriptionId(), selectedService)
	if err != nil {
		return nil, fmt.Errorf("getting target resource: %w", err)
	}

	// Execute the task
	if err := serviceTarget.Task(ctx, selectedService, targetResource, selectedTask, taskArgs); err != nil {
		return nil, fmt.Errorf("executing task '%s': %w", selectedTask.Name, err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Task '%s' completed successfully.", selectedTask.Name),
		},
	}, nil
}
