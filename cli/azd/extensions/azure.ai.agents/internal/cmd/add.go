// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

type agentAddDependencyFlags struct {
	agent string
}

func newAgentAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <type> <service>",
		Short: "Add a typed service dependency to an agent.",
	}
	cmd.AddCommand(newAgentAddDependencyCommand(extCtx, "toolbox", AiToolboxHost))
	cmd.AddCommand(newAgentAddDependencyCommand(extCtx, "connection", AiConnectionHost))
	return cmd
}

func newAgentAddDependencyCommand(
	extCtx *azdext.ExtensionContext,
	dependencyType string,
	expectedHost string,
) *cobra.Command {
	flags := &agentAddDependencyFlags{}
	cmd := &cobra.Command{
		Use:   dependencyType + " <service>",
		Short: fmt.Sprintf("Add a %s service dependency to an agent service.", dependencyType),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentServiceName, dependencyServiceName := normalizeAgentDependencyNames(flags.agent, args[0])
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return exterrors.Internal(
					exterrors.CodeAzdClientFailed,
					fmt.Sprintf("failed to connect to azd: %s", err),
				)
			}
			defer azdClient.Close()

			added, err := addAgentServiceDependency(
				cmd.Context(), azdClient, agentServiceName, dependencyServiceName, dependencyType, expectedHost,
			)
			if err != nil {
				return err
			}
			if extCtx.OutputFormat == "json" {
				return emitJSON(map[string]any{
					"agent":      agentServiceName,
					"type":       dependencyType,
					"dependency": dependencyServiceName,
					"added":      added,
				})
			}
			if added {
				fmt.Printf("Added %s service %q to agent service %q.\n",
					dependencyType, dependencyServiceName, agentServiceName)
			} else {
				fmt.Printf("Agent service %q already uses %s service %q.\n",
					agentServiceName, dependencyType, dependencyServiceName)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Agent service name in azure.yaml.")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func addAgentServiceDependency(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentServiceName string,
	dependencyServiceName string,
	dependencyType string,
	expectedHost string,
) (bool, error) {
	agentServiceName, dependencyServiceName = normalizeAgentDependencyNames(agentServiceName, dependencyServiceName)
	if agentServiceName == "" {
		return false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"an agent service name is required",
			"pass --agent <service> with an azure.ai.agent service from azure.yaml",
		)
	}
	if dependencyServiceName == "" {
		return false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("a %s service name is required", dependencyType),
			fmt.Sprintf("pass a %s service name from azure.yaml", dependencyType),
		)
	}

	response, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return false, err
	}
	projectConfig := response.GetProject()
	if projectConfig == nil {
		return false, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"the current directory does not contain an azd project",
			"run the command from a directory containing azure.yaml",
		)
	}
	projectLock, err := acquireAgentAddProjectLock(ctx, projectConfig.GetPath())
	if err != nil {
		return false, err
	}
	if projectLock != nil {
		defer func() { _ = projectLock.Unlock() }()
	}

	response, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return false, err
	}
	projectConfig = response.GetProject()
	if projectConfig == nil {
		return false, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"the current directory does not contain an azd project",
			"run the command from a directory containing azure.yaml",
		)
	}
	services := projectConfig.GetServices()
	agentService, found := services[agentServiceName]
	if !found {
		return false, missingServiceDependencyError(agentServiceName, AiAgentHost)
	}
	if agentService.GetHost() != AiAgentHost {
		return false, invalidServiceHostError(agentServiceName, agentService.GetHost(), AiAgentHost)
	}
	dependencyService, found := services[dependencyServiceName]
	if !found {
		return false, missingServiceDependencyError(dependencyServiceName, expectedHost)
	}
	if dependencyService.GetHost() != expectedHost {
		return false, invalidServiceHostError(dependencyServiceName, dependencyService.GetHost(), expectedHost)
	}
	currentUses, err := getServiceUses(ctx, azdClient, agentServiceName)
	if err != nil {
		return false, err
	}
	if slices.Contains(currentUses, dependencyServiceName) {
		return false, nil
	}
	if serviceDependsOn(services, dependencyServiceName, agentServiceName, map[string]bool{}) {
		return false, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("adding service %q to agent service %q would create a dependency cycle",
				dependencyServiceName, agentServiceName),
			"remove the reverse dependency before adding this service",
		)
	}

	uses := append(currentUses, dependencyServiceName)
	if err := setServiceUses(ctx, azdClient, agentServiceName, uses); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeAgentDependencyNames(agentServiceName, dependencyServiceName string) (string, string) {
	return strings.TrimSpace(agentServiceName), strings.TrimSpace(dependencyServiceName)
}

func getServiceUses(ctx context.Context, azdClient *azdext.AzdClient, serviceName string) ([]string, error) {
	response, err := azdClient.Project().GetServiceConfigValue(ctx, &azdext.GetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "uses",
	})
	if err != nil {
		return nil, err
	}
	if !response.GetFound() {
		return nil, nil
	}
	value := response.GetValue()
	if value == nil {
		return nil, invalidServiceUsesError(serviceName, "has no value")
	}
	items, ok := value.AsInterface().([]any)
	if !ok {
		return nil, invalidServiceUsesError(serviceName, "is not a list")
	}
	uses := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, invalidServiceUsesError(serviceName, "contains a non-string value")
		}
		uses = append(uses, value)
	}
	return uses, nil
}

func invalidServiceUsesError(serviceName, reason string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("service %q has an invalid uses value that %s", serviceName, reason),
		"set uses to a list of service names in azure.yaml",
	)
}

func acquireAgentAddProjectLock(ctx context.Context, projectPath string) (*flock.Flock, error) {
	if projectPath == "" {
		return nil, nil
	}
	lockDirectory := filepath.Join(projectPath, ".azure")
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("creating agent dependency lock directory: %w", err)
	}
	projectLock := flock.New(filepath.Join(lockDirectory, "agent-add.lock"))
	locked, err := projectLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("locking agent dependency configuration: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("locking agent dependency configuration: %w", ctx.Err())
	}
	return projectLock, nil
}

func serviceDependsOn(
	services map[string]*azdext.ServiceConfig,
	serviceName string,
	targetName string,
	visited map[string]bool,
) bool {
	if serviceName == targetName {
		return true
	}
	if visited[serviceName] {
		return false
	}
	visited[serviceName] = true
	service := services[serviceName]
	if service == nil {
		return false
	}
	for _, dependency := range service.GetUses() {
		if serviceDependsOn(services, dependency, targetName, visited) {
			return true
		}
	}
	return false
}

func missingServiceDependencyError(serviceName, expectedHost string) error {
	return exterrors.Dependency(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("service %q was not found in azure.yaml", serviceName),
		fmt.Sprintf("add a service with host: %s before adding this dependency", expectedHost),
	)
}

func invalidServiceHostError(serviceName, actualHost, expectedHost string) error {
	return exterrors.Validation(
		exterrors.CodeUnsupportedHost,
		fmt.Sprintf("service %q has host %q; expected %q", serviceName, actualHost, expectedHost),
		"select a service with the expected host from azure.yaml",
	)
}
